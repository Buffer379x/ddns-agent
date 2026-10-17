package config

import (
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	DataDir         string
	Port            int
	UpdateInterval  time.Duration
	CooldownPeriod  time.Duration
	HTTPTimeout     time.Duration
	JWTSecret       string
	EncryptionKey   string
	BackupRetention int
	LogRetention    int // days of archived agent-YYYY-MM-DD.log files to keep under logs/
	Timezone        string
	TrustedProxies  string
}

func Load() *Config {
	return &Config{
		TrustedProxies: strings.TrimSpace(os.Getenv("DDNS_TRUSTED_PROXIES")),
		DataDir:        envOrDefault("DDNS_DATA_DIR", "/data"),
		Port:           envOrDefaultInt("DDNS_PORT", 8080),
		// Fallbacks when DB settings are missing or invalid (refresh_interval / cooldown_seconds / http_timeout_seconds from UI).
		UpdateInterval:  envOrDefaultDuration("DDNS_UPDATE_INTERVAL", 5*time.Minute),
		CooldownPeriod:  envOrDefaultDuration("DDNS_COOLDOWN", 5*time.Minute),
		HTTPTimeout:     envOrDefaultDuration("DDNS_HTTP_TIMEOUT", 10*time.Second),
		JWTSecret:       os.Getenv("DDNS_JWT_SECRET"),
		EncryptionKey:   os.Getenv("DDNS_ENCRYPTION_KEY"),
		BackupRetention: envOrDefaultInt("DDNS_BACKUP_RETENTION", 7), // fallback if backup_retention missing/invalid in DB
		LogRetention:    envOrDefaultInt("DDNS_LOG_RETENTION", 7),    // fallback if log_archive_days missing/invalid in DB
		Timezone:        strings.TrimSpace(os.Getenv("DDNS_TIMEZONE")),
	}
}

// TimeLocation resolves DDNS_TIMEZONE, then TZ env, then Go's default (often UTC in containers).
func (c *Config) TimeLocation() *time.Location {
	name := strings.TrimSpace(c.Timezone)
	if name == "" {
		name = strings.TrimSpace(os.Getenv("TZ"))
	}
	if name == "" {
		return time.Local
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.Local
	}
	return loc
}

func (c *Config) DBPath() string    { return c.DataDir + "/ddns-agent.db" }
func (c *Config) BackupDir() string { return c.DataDir + "/backups" }
func (c *Config) LogDir() string    { return c.DataDir + "/logs" }
func (c *Config) KeyFile() string   { return c.DataDir + "/.key" }

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envOrDefaultInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}

func envOrDefaultDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}

func (c *Config) Validate() error {
	for _, key := range []string{"DDNS_PORT", "DDNS_BACKUP_RETENTION", "DDNS_LOG_RETENTION"} {
		if raw := os.Getenv(key); raw != "" {
			if _, err := strconv.Atoi(raw); err != nil {
				return fmt.Errorf("invalid %s", key)
			}
		}
	}
	for _, key := range []string{"DDNS_UPDATE_INTERVAL", "DDNS_COOLDOWN", "DDNS_HTTP_TIMEOUT"} {
		if raw := os.Getenv(key); raw != "" {
			if _, err := time.ParseDuration(raw); err != nil {
				return fmt.Errorf("invalid %s", key)
			}
		}
	}
	if c.Timezone != "" {
		if _, err := time.LoadLocation(c.Timezone); err != nil {
			return fmt.Errorf("invalid DDNS_TIMEZONE")
		}
	}

	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("DDNS_PORT must be 1–65535")
	}
	if c.UpdateInterval < time.Minute || c.UpdateInterval > 7*24*time.Hour {
		return fmt.Errorf("DDNS_UPDATE_INTERVAL must be 1 minute–7 days")
	}
	if c.CooldownPeriod < 0 || c.CooldownPeriod > 7*24*time.Hour {
		return fmt.Errorf("invalid DDNS_COOLDOWN")
	}
	if c.HTTPTimeout < time.Second || c.HTTPTimeout > 15*time.Minute {
		return fmt.Errorf("invalid DDNS_HTTP_TIMEOUT")
	}
	if c.JWTSecret != "" && len(c.JWTSecret) < 32 {
		return fmt.Errorf("DDNS_JWT_SECRET must contain at least 32 bytes")
	}
	if c.BackupRetention < 1 || c.BackupRetention > 365 || c.LogRetention < 1 || c.LogRetention > 3650 {
		return fmt.Errorf("invalid retention configuration")
	}
	for _, raw := range strings.Split(c.TrustedProxies, ",") {
		if strings.TrimSpace(raw) != "" {
			if _, err := netip.ParsePrefix(strings.TrimSpace(raw)); err != nil {
				return fmt.Errorf("invalid trusted proxy CIDR: %s", raw)
			}
		}
	}
	return nil
}
