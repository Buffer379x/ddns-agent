package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	_ "time/tzdata" // embed IANA time zones; required in scratch Docker images

	"ddns-agent/internal/auth"
	"ddns-agent/internal/backup"
	"ddns-agent/internal/config"
	"ddns-agent/internal/crypto"
	"ddns-agent/internal/database"
	"ddns-agent/internal/ipcheck"
	"ddns-agent/internal/logger"
	"ddns-agent/internal/server"
	"ddns-agent/internal/server/sse"
	"ddns-agent/internal/updater"
	"ddns-agent/internal/webhook"
	"ddns-agent/web"
)

// version is injected at link time from the VERSION file (see Dockerfile).
var version = "dev"

func main() {
	// Built-in health-check subcommand used by Docker HEALTHCHECK.
	if len(os.Args) > 1 && os.Args[1] == "--health" {
		resp, err := http.Get("http://localhost:8080/health")
		if err != nil {
			os.Exit(1)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		os.Exit(0)
	}

	cfg := config.Load()

	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "ddns-agent: creating data directory: %v\n", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(cfg.LogDir(), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "ddns-agent: creating logs directory: %v\n", err)
		os.Exit(1)
	}

	db, err := database.New(cfg.DBPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "ddns-agent: opening database: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	initDefaults(db)

	updateInterval := cfg.UpdateInterval
	if s, err := db.GetSetting("refresh_interval"); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && n >= 60 {
			updateInterval = time.Duration(n) * time.Second
		}
	}

	cooldown := cfg.CooldownPeriod
	if s, err := db.GetSetting("cooldown_seconds"); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && n >= 0 {
			cooldown = time.Duration(n) * time.Second
		}
	}

	httpTimeout := cfg.HTTPTimeout
	if s, err := db.GetSetting("http_timeout_seconds"); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && n >= 1 {
			httpTimeout = time.Duration(n) * time.Second
		}
	}

	backupRetention := cfg.BackupRetention
	if s, err := db.GetSetting("backup_retention"); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && n >= 1 {
			backupRetention = n
		}
	}

	logRetention := cfg.LogRetention
	if s, err := db.GetSetting("log_archive_days"); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && n >= 1 {
			logRetention = n
		}
	}

	tzLoc := appTimeLocation(cfg, db)
	log := logger.New()
	log.SetTimeLocation(tzLoc)
	log.SetFileLog(cfg.LogDir(), logRetention, tzLoc)
	log.SetStore(db)

	log.Info("main", "DDNS Agent v%s starting...", version)
	log.Info("main", "app timezone (logs): %s", tzLoc.String())
	log.Info("main", "data directory: %s", cfg.DataDir)
	log.Info("main", "file log: %s/agent.log (daily rotation, archives kept %d days)", cfg.LogDir(), logRetention)

	encKey := cfg.EncryptionKey
	if encKey == "" {
		encKey, err = crypto.LoadOrCreateKey(cfg.KeyFile())
		if err != nil {
			log.Error("main", "loading encryption key: %v", err)
			os.Exit(1)
		}
	}
	encryptor, err := crypto.NewEncryptor(encKey)
	if err != nil {
		log.Error("main", "creating encryptor: %v", err)
		os.Exit(1)
	}

	jwtSecret := cfg.JWTSecret
	if jwtSecret == "" {
		jwtSecret = crypto.GenerateKey()
		log.Warn("main", "no JWT secret configured, generated ephemeral key (sessions lost on restart)")
	}

	authSvc := auth.NewService(jwtSecret)
	ipFetcher := ipcheck.New(httpTimeout)
	sseBroker := sse.NewBroker(100)
	webhookSvc := webhook.New(db)
	backupSvc := backup.New(db, cfg.BackupDir(), backupRetention)
	updaterSvc := updater.New(db, ipFetcher, encryptor, log, webhookSvc,
		updateInterval, cooldown, httpTimeout)

	log.SetSSE(sseBroker)

	ensureDefaultAdmin(db, log)

	stopBackup := make(chan struct{})
	backupSvc.StartAutoBackup(stopBackup)

	handler := server.NewRouter(db, authSvc, updaterSvc, webhookSvc, backupSvc, encryptor, sseBroker, log, web.FS, version, cfg)

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	ctx, cancel := context.WithCancel(context.Background())
	go updaterSvc.Start(ctx)

	go func() {
		log.Info("main", "web panel available at http://0.0.0.0:%d", cfg.Port)
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			log.Error("main", "server error: %v", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info("main", "shutting down gracefully...")
	cancel()
	close(stopBackup)

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	srv.Shutdown(shutdownCtx)
	_ = log.CloseFileLog()
	log.Info("main", "goodbye")
}

// appTimeLocation resolves the effective log timezone. The DB setting
// "app_timezone" takes precedence over DDNS_TIMEZONE / TZ env vars.
func appTimeLocation(cfg *config.Config, db *database.DB) *time.Location {
	if s, err := db.GetSetting("app_timezone"); err == nil {
		if t := strings.TrimSpace(s); t != "" {
			if loc, err := time.LoadLocation(t); err == nil {
				return loc
			}
		}
	}
	return cfg.TimeLocation()
}

// ensureDefaultAdmin creates the initial admin account when no users exist.
// The application exits on any error to prevent starting with a broken auth state.
func ensureDefaultAdmin(db *database.DB, log *logger.Logger) {
	count, err := db.UserCount()
	if err != nil {
		log.Error("main", "checking user count: %v", err)
		os.Exit(1)
	}
	if count > 0 {
		return
	}
	hash, err := auth.HashPassword("admin")
	if err != nil {
		log.Error("main", "hashing default admin password: %v", err)
		os.Exit(1)
	}
	if _, err := db.CreateUser("admin", hash, "admin"); err != nil {
		log.Error("main", "creating default admin user: %v", err)
		os.Exit(1)
	}
	log.Info("main", "default admin user created (username: admin, password: admin)")
}

// initDefaults seeds the settings table with sensible values on first install
// and migrates the legacy "update_interval" Go-duration string to "refresh_interval" seconds.
func initDefaults(db *database.DB) {
	// Legacy migration: Go duration string → integer seconds used by the web UI.
	if _, err := db.GetSetting("refresh_interval"); err != nil {
		if legacy, err2 := db.GetSetting("update_interval"); err2 == nil && strings.TrimSpace(legacy) != "" {
			if d, err3 := time.ParseDuration(strings.TrimSpace(legacy)); err3 == nil {
				_ = db.SetSetting("refresh_interval", strconv.Itoa(int(d/time.Second)))
			}
		}
	}

	// Ordered slice ensures deterministic initialization and makes intent clear.
	defaults := []struct{ key, value string }{
		{"refresh_interval", "300"},
		{"cooldown_seconds", "300"},
		{"http_timeout_seconds", "10"},
		{"backup_retention", "7"},
		{"log_archive_days", "7"},
		{"theme", "auto"},
		{"language", "en"},
	}
	for _, d := range defaults {
		if _, err := db.GetSetting(d.key); err != nil {
			db.SetSetting(d.key, d.value)
		}
	}
}
