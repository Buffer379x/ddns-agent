package api

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"strconv"
	"time"

	"ddns-agent/internal/auth"
	"ddns-agent/internal/backup"
	"ddns-agent/internal/config"
	"ddns-agent/internal/crypto"
	"ddns-agent/internal/database"
	"ddns-agent/internal/logger"
	"ddns-agent/internal/server/sse"
	"ddns-agent/internal/updater"
	"ddns-agent/internal/webhook"

	"github.com/go-chi/chi/v5"
)

type Handler struct {
	db            *database.DB
	auth          *auth.Service
	updater       *updater.Service
	webhooks      *webhook.Service
	backup        *backup.Service
	encryptor     *crypto.Encryptor
	sse           *sse.Broker
	log           *logger.Logger
	webFS         fs.FS
	version       string
	cfg           *config.Config
	loginAccounts *auth.RateLimiter
	loginGlobal   *auth.RateLimiter
}

func NewHandler(
	db *database.DB, authSvc *auth.Service, updaterSvc *updater.Service,
	webhookSvc *webhook.Service, backupSvc *backup.Service,
	encryptor *crypto.Encryptor, sseBroker *sse.Broker, log *logger.Logger,
	webFS fs.FS, version string, cfg *config.Config,
) *Handler {
	return &Handler{
		db: db, auth: authSvc, updater: updaterSvc, webhooks: webhookSvc,
		backup: backupSvc, encryptor: encryptor, sse: sseBroker, log: log,
		webFS: webFS, version: version, cfg: cfg,
		loginAccounts: auth.NewRateLimiter(10, time.Minute), loginGlobal: auth.NewRateLimiter(100, time.Minute),
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func readJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(v); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("only one JSON value is allowed")
	}
	return nil
}

func paramInt(r *http.Request, name string) int {
	v, _ := strconv.Atoi(chi.URLParam(r, name))
	if v < 1 {
		return 0
	}
	return v
}

func queryInt(r *http.Request, name string, fallback int) int {
	v := r.URL.Query().Get(name)
	if v == "" {
		return fallback
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return i
}

func (h *Handler) Version(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"version": h.version})
}

func (h *Handler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	if err := h.db.Ping(); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unhealthy", "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "healthy"})
}

func (h *Handler) Status(w http.ResponseWriter, r *http.Request) {
	total, active, successful, errs, err := h.db.RecordCounts()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"total_records":      total,
		"active_records":     active,
		"successful_records": successful,
		"error_records":      errs,
	})
}

// --- Database Maintenance ---

func (h *Handler) SSEEvents(w http.ResponseWriter, r *http.Request) {
	token := auth.RequestToken(r)
	h.sse.Handler(func() bool { _, err := h.auth.ValidateToken(token); return err == nil })(w, r)
}
