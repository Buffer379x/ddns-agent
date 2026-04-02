package config

import (
	"os"
	"strconv"
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
}

func Load() *Config {
	return &Config{
		DataDir:         envOrDefault("DDNS_DATA_DIR", "/data"),
		Port:            envOrDefaultInt("DDNS_PORT", 8080),
		UpdateInterval:  envOrDefaultDuration("DDNS_UPDATE_INTERVAL", 5*time.Minute),
		CooldownPeriod:  envOrDefaultDuration("DDNS_COOLDOWN", 5*time.Minute),
		HTTPTimeout:     envOrDefaultDuration("DDNS_HTTP_TIMEOUT", 10*time.Second),
		JWTSecret:       os.Getenv("DDNS_JWT_SECRET"),
		EncryptionKey:   os.Getenv("DDNS_ENCRYPTION_KEY"),
		BackupRetention: envOrDefaultInt("DDNS_BACKUP_RETENTION", 7),
		LogRetention:    envOrDefaultInt("DDNS_LOG_RETENTION", 7),
	}
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
