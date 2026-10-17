package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
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
		port := config.Load().Port
		client := &http.Client{Timeout: 4 * time.Second}
		resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/health", port))
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
	if err := cfg.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

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
		jwtSecret, err = crypto.LoadOrCreateKey(filepath.Join(cfg.DataDir, ".jwt-key"))
		if err != nil {
			log.Error("main", "loading session key: %v", err)
			os.Exit(1)
		}
	}

	authSvc := auth.NewService(jwtSecret, db)
	ipFetcher := ipcheck.New(httpTimeout)
	sseBroker := sse.NewBroker(100)
	webhookSvc := webhook.New(db, encryptor, func(message string) { log.Warn("webhook", "%s", message) })
	backupSvc := backup.New(db, cfg.BackupDir(), backupRetention, encryptor)
	backupSvc.SetReporter(func(message string) { log.Error("backup", "%s", message) })
	updaterSvc := updater.New(db, ipFetcher, encryptor, log, webhookSvc,
		updateInterval, cooldown, httpTimeout)

	log.SetSSE(sseBroker)
	updaterSvc.SetChanged(sseBroker.BroadcastRecords)
	if err := migrateSecrets(db, encryptor); err != nil {
		log.Error("main", "credential migration: %v", err)
		os.Exit(1)
	}

	ensureDefaultAdmin(db, log, cfg.DataDir)

	stopBackup := make(chan struct{})
	backupSvc.StartAutoBackup(stopBackup)

	handler := server.NewRouter(db, authSvc, updaterSvc, webhookSvc, backupSvc, encryptor, sseBroker, log, web.FS, version, cfg)

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           handler,
		ReadTimeout:       30 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		MaxHeaderBytes:    16 << 10,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	ctx, cancel := context.WithCancel(context.Background())
	updaterDone := make(chan struct{})
	go func() { defer close(updaterDone); updaterSvc.Start(ctx) }()

	go func() {
		log.Info("main", "web panel available at http://%s:%d", localIP(), cfg.Port)
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
	if err := srv.Shutdown(shutdownCtx); err != nil {
		_ = srv.Close()
	}
	<-updaterDone
	webhookSvc.Close()
	backupSvc.Wait()
	log.Info("main", "goodbye")
	_ = log.CloseFileLog()
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
func ensureDefaultAdmin(db *database.DB, log *logger.Logger, dataDir string) {
	count, err := db.UserCount()
	if err != nil {
		log.Error("main", "checking accounts: %v", err)
		os.Exit(1)
	}
	if count > 0 {
		return
	}
	password := os.Getenv("DDNS_ADMIN_PASSWORD")
	if password == "" {
		password = crypto.GenerateKey()[:24]
	}
	if err = auth.ValidatePassword(password); err != nil {
		log.Error("main", "initial admin password: %v", err)
		os.Exit(1)
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		log.Error("main", "hashing initial password: %v", err)
		os.Exit(1)
	}
	// Persist a recoverable bootstrap secret before creating the account; never write it to shared logs.
	path := filepath.Join(dataDir, "bootstrap-password")
	if err = os.WriteFile(path, []byte(password), 0600); err != nil {
		log.Error("main", "writing bootstrap password: %v", err)
		os.Exit(1)
	}
	user, err := db.CreateUser("admin", hash, "admin")
	if err != nil {
		log.Error("main", "creating administrator: %v", err)
		os.Exit(1)
	}
	if err = db.RequirePasswordChange(user.ID); err != nil {
		log.Error("main", "marking initial account: %v", err)
		os.Exit(1)
	}
	log.Info("main", "initial administrator created; read %s and change the password after signing in", path)
}

// initDefaults seeds the settings table with sensible values on first install
// and migrates the legacy "update_interval" Go-duration string to "refresh_interval" seconds.
func initDefaults(db *database.DB, cfg *config.Config) {
	if _, err := db.GetSetting("refresh_interval"); err != nil {
		if legacy, err := db.GetSetting("update_interval"); err == nil {
			if d, err := time.ParseDuration(legacy); err == nil && d >= time.Minute {
				_ = db.SetSetting("refresh_interval", strconv.Itoa(int(d/time.Second)))
			}
		}
	}
	defaults := map[string]string{"refresh_interval": strconv.Itoa(int(cfg.UpdateInterval / time.Second)), "cooldown_seconds": strconv.Itoa(int(cfg.CooldownPeriod / time.Second)), "http_timeout_seconds": strconv.Itoa(int(cfg.HTTPTimeout / time.Second)), "backup_retention": strconv.Itoa(cfg.BackupRetention), "log_archive_days": strconv.Itoa(cfg.LogRetention), "data_retention_days": "30", "theme": "auto", "language": "en"}
	for key, value := range defaults {
		if _, err := db.GetSetting(key); err != nil {
			if err := db.SetSetting(key, value); err != nil {
				fmt.Fprintln(os.Stderr, "initializing settings:", err)
				os.Exit(1)
			}
		}
	}
}

// localIP returns the first non-loopback IPv4 address of the host.
// This lets the startup log show the real address under which the panel is reachable.
func localIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "0.0.0.0"
	}
	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() && ipNet.IP.To4() != nil {
			return ipNet.IP.String()
		}
	}
	return "0.0.0.0"
}

func migrateSecrets(db *database.DB, enc *crypto.Encryptor) error {
	records, err := db.ListRecords()
	if err != nil {
		return err
	}
	for _, rec := range records {
		if strings.HasPrefix(strings.TrimSpace(rec.ProviderConfig), "{") && json.Valid([]byte(rec.ProviderConfig)) {
			rec.ProviderConfig, err = enc.Encrypt(rec.ProviderConfig)
			if err != nil {
				return err
			}
			if err = db.UpdateRecord(&rec); err != nil {
				return err
			}
		}
	}
	hooks, err := db.ListWebhooks()
	if err != nil {
		return err
	}
	for _, hook := range hooks {
		if !strings.HasPrefix(hook.URL, "v1:") {
			hook.URL, err = enc.Encrypt(hook.URL)
			if err != nil {
				return err
			}
			if err = db.UpdateWebhook(&hook); err != nil {
				return err
			}
		}
	}
	return nil
}
