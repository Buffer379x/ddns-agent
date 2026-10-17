package config

import (
	"testing"
)

func TestInvalidEnvironmentDoesNotSilentlyFallBack(t *testing.T) {
	for _, key := range []string{"DDNS_PORT", "DDNS_BACKUP_RETENTION", "DDNS_LOG_RETENTION", "DDNS_UPDATE_INTERVAL", "DDNS_COOLDOWN", "DDNS_HTTP_TIMEOUT", "DDNS_TRUSTED_PROXIES", "DDNS_TIMEZONE"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, "invalid-value")
			if err := Load().Validate(); err == nil {
				t.Fatal("invalid environment accepted")
			}
		})
	}
}
