package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	_ "time/tzdata" // IANA zones embedded; required for time.LoadLocation in scratch Docker (no system zoneinfo)

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

// version is set at link time from the VERSION file (see Dockerfile).
var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--health" {
		resp, err := http.Get("http://localhost:8080/health")
		if err != nil || resp.StatusCode != 200 {
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

	initDefaults(db, cfg)

	tzLoc := appTimeLocation(cfg, db)
	log := logger.New()
	log.SetTimeLocation(tzLoc)
	log.SetFileLog(cfg.LogDir(), cfg.LogRetention, tzLoc)
	log.SetStore(db)

	log.Info("main", "DDNS Agent v%s starting...", version)
	log.Info("main", "app timezone (logs): %s", tzLoc.String())
	log.Info("main", "data directory: %s", cfg.DataDir)
	log.Info("main", "file log: %s/agent.log (daily rotation, archives kept %d days)", cfg.LogDir(), cfg.LogRetention)

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

	authSvc := auth.NewService(db, jwtSecret)
	ipFetcher := ipcheck.New(cfg.HTTPTimeout)
	sseBroker := sse.NewBroker(100)
	webhookSvc := webhook.New(db)
	backupSvc := backup.New(db, cfg.BackupDir(), cfg.BackupRetention)
	updaterSvc := updater.New(db, ipFetcher, encryptor, log, webhookSvc,
		cfg.UpdateInterval, cfg.CooldownPeriod, cfg.HTTPTimeout)

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

// Setting app_timezone (IANA) overrides DDNS_TIMEZONE / TZ for log timestamps and rotation.
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

func ensureDefaultAdmin(db *database.DB, log *logger.Logger) {
	count, _ := db.UserCount()
	if count > 0 {
		return
	}
	hash, _ := auth.HashPassword("admin")
	db.CreateUser("admin", hash, "admin")
	log.Info("main", "default admin user created (username: admin, password: admin)")
}

func initDefaults(db *database.DB, cfg *config.Config) {
	defaults := map[string]string{
		"update_interval": cfg.UpdateInterval.String(),
		"theme":           "auto",
		"language":        "en",
	}
	for k, v := range defaults {
		if _, err := db.GetSetting(k); err != nil {
			db.SetSetting(k, v)
		}
	}
}
