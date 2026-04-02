package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

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

var version = "1.0.0"

func main() {
	// Health check mode for Docker HEALTHCHECK
	if len(os.Args) > 1 && os.Args[1] == "--health" {
		resp, err := http.Get("http://localhost:8080/health")
		if err != nil || resp.StatusCode != 200 {
			os.Exit(1)
		}
		os.Exit(0)
	}

	cfg := config.Load()
	log := logger.New()

	log.Info("main", "DDNS Agent v%s starting...", version)
	log.Info("main", "data directory: %s", cfg.DataDir)

	// Ensure data directory
	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		log.Error("main", "creating data directory: %v", err)
		os.Exit(1)
	}

	// Database
	db, err := database.New(cfg.DBPath())
	if err != nil {
		log.Error("main", "opening database: %v", err)
		os.Exit(1)
	}
	defer db.Close()
	log.SetStore(db)

	// Encryption key
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

	// JWT secret
	jwtSecret := cfg.JWTSecret
	if jwtSecret == "" {
		jwtSecret = crypto.GenerateKey()
		log.Warn("main", "no JWT secret configured, generated ephemeral key (sessions lost on restart)")
	}

	// Services
	authSvc := auth.NewService(db, jwtSecret)
	ipFetcher := ipcheck.New(cfg.HTTPTimeout)
	sseBroker := sse.NewBroker(100)
	webhookSvc := webhook.New(db)
	backupSvc := backup.New(db, cfg.BackupDir(), cfg.BackupRetention)
	updaterSvc := updater.New(db, ipFetcher, encryptor, log, webhookSvc,
		cfg.UpdateInterval, cfg.CooldownPeriod, cfg.HTTPTimeout)

	log.SetSSE(sseBroker)

	// Default admin user (admin/admin)
	ensureDefaultAdmin(db, log)

	// Default settings
	initDefaults(db, cfg)

	// Auto-backup
	stopBackup := make(chan struct{})
	backupSvc.StartAutoBackup(stopBackup)

	// Router
	handler := server.NewRouter(db, authSvc, updaterSvc, webhookSvc, backupSvc, encryptor, sseBroker, log, web.FS, version)

	// HTTP server
	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Start updater in background
	ctx, cancel := context.WithCancel(context.Background())
	go updaterSvc.Start(ctx)

	// Start HTTP server
	go func() {
		log.Info("main", "web panel available at http://0.0.0.0:%d", cfg.Port)
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			log.Error("main", "server error: %v", err)
			os.Exit(1)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info("main", "shutting down gracefully...")
	cancel()
	close(stopBackup)

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	srv.Shutdown(shutdownCtx)
	log.Info("main", "goodbye")
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
