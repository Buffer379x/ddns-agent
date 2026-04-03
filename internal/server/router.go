package server

import (
	"io/fs"
	"net/http"
	"time"

	"ddns-agent/internal/auth"
	"ddns-agent/internal/backup"
	"ddns-agent/internal/config"
	"ddns-agent/internal/crypto"
	"ddns-agent/internal/database"
	"ddns-agent/internal/logger"
	"ddns-agent/internal/server/api"
	"ddns-agent/internal/server/sse"
	"ddns-agent/internal/updater"
	"ddns-agent/internal/webhook"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func NewRouter(
	db *database.DB,
	authSvc *auth.Service,
	updaterSvc *updater.Service,
	webhookSvc *webhook.Service,
	backupSvc *backup.Service,
	encryptor *crypto.Encryptor,
	sseBroker *sse.Broker,
	log *logger.Logger,
	webFS fs.FS,
	version string,
	cfg *config.Config,
) http.Handler {
	r := chi.NewRouter()

	// RealIP must only be trusted when a reverse proxy is in front of the
	// application. For direct-bind deployments it is a no-op because
	// X-Forwarded-For will not be present.
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Compress(5))
	r.Use(corsMiddleware)

	h := api.NewHandler(db, authSvc, updaterSvc, webhookSvc, backupSvc, encryptor, sseBroker, log, webFS, version, cfg)

	loginLimiter := auth.NewRateLimiter(5, time.Minute)
	apiLimiter := auth.NewRateLimiter(60, time.Minute)

	// Public routes — no auth required.
	r.Get("/health", h.HealthCheck)
	r.Get("/api/version", h.Version)
	r.Get("/api/lang", h.ListLanguages)
	r.Get("/api/lang/{locale}", h.GetLanguage)

	// Auth routes — rate-limited to slow down brute force.
	r.Group(func(r chi.Router) {
		r.Use(loginLimiter.Middleware)
		r.Post("/api/auth/login", h.Login)
		r.Post("/api/auth/setup", h.Setup)
	})

	// SSE stream — auth validated inside the handler via token query param
	// because EventSource does not support custom request headers.
	r.Get("/api/events", h.SSEEvents)

	// Protected API routes — all authenticated users.
	r.Group(func(r chi.Router) {
		r.Use(authSvc.AuthMiddleware)
		r.Use(apiLimiter.Middleware)

		r.Get("/api/auth/me", h.Me)
		r.Get("/api/records", h.ListRecords)
		r.Get("/api/providers", h.ListProviders)
		r.Get("/api/providers/{name}/fields", h.ProviderFields)
		r.Get("/api/logs", h.ListLogs)
		r.Get("/api/status", h.Status)
		r.Get("/api/settings", h.GetSettings)
	})

	// Admin-only API routes.
	r.Group(func(r chi.Router) {
		r.Use(authSvc.AuthMiddleware)
		r.Use(apiLimiter.Middleware)
		r.Use(api.AdminOnly)

		r.Post("/api/records", h.CreateRecord)
		r.Put("/api/records/{id}", h.UpdateRecord)
		r.Delete("/api/records/{id}", h.DeleteRecord)
		r.Post("/api/records/{id}/refresh", h.RefreshRecord)
		r.Post("/api/records/refresh-all", h.RefreshAll)

		r.Delete("/api/logs", h.DeleteLogs)

		r.Put("/api/settings", h.UpdateSettings)

		r.Get("/api/users", h.ListUsers)
		r.Post("/api/users", h.CreateUser)
		r.Put("/api/users/{id}", h.UpdateUser)
		r.Delete("/api/users/{id}", h.DeleteUser)

		r.Get("/api/webhooks", h.ListWebhooks)
		r.Post("/api/webhooks", h.CreateWebhook)
		r.Put("/api/webhooks/{id}", h.UpdateWebhook)
		r.Delete("/api/webhooks/{id}", h.DeleteWebhook)
		r.Post("/api/webhooks/{id}/test", h.TestWebhook)

		r.Get("/api/config/export", h.ExportConfig)
		r.Post("/api/config/import", h.ImportConfig)
	})

	// Serve embedded frontend with SPA fallback for client-side routing.
	fileServer := http.FileServer(http.FS(webFS))
	r.Get("/*", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path[1:]
		if path == "" {
			path = "index.html"
		}
		if _, err := fs.Stat(webFS, path); err == nil {
			fileServer.ServeHTTP(w, r)
			return
		}
		// Fall back to index.html for unknown paths (SPA client routing).
		data, err := fs.ReadFile(webFS, "index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	})

	return r
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}
