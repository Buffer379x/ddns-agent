package validation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"ddns-agent/internal/database"
	"ddns-agent/internal/provider"
	"ddns-agent/internal/provider/constants"

	"golang.org/x/net/idna"
)

var labelRE = regexp.MustCompile(`^[a-z0-9_](?:[a-z0-9_-]{0,61}[a-z0-9_])?$`)
var userRE = regexp.MustCompile(`^[\pL\pN][\pL\pN_.@-]{0,63}$`)
var localeRE = regexp.MustCompile(`^[a-z]{2}(-[A-Z]{2})?$`)

func User(name, role string) error {
	if !userRE.MatchString(name) {
		return errors.New("username must contain 1–64 letters, numbers or . _ @ -")
	}
	if role != "admin" && role != "viewer" {
		return errors.New("role must be admin or viewer")
	}
	return nil
}
func Settings(settings map[string]string) error {
	ranges := map[string][2]int{"refresh_interval": {60, 604800}, "cooldown_seconds": {0, 604800}, "http_timeout_seconds": {1, 900}, "backup_retention": {1, 365}, "log_archive_days": {1, 3650}, "data_retention_days": {1, 3650}}
	for key, value := range settings {
		if bounds, ok := ranges[key]; ok {
			n, err := strconv.Atoi(value)
			if err != nil || n < bounds[0] || n > bounds[1] {
				return fmt.Errorf("invalid %s (%d–%d)", key, bounds[0], bounds[1])
			}
			continue
		}
		switch key {
		case "theme":
			if value != "auto" && value != "light" && value != "dark" {
				return errors.New("invalid theme")
			}
		case "language":
			if !localeRE.MatchString(value) {
				return errors.New("invalid language")
			}
		case "app_timezone":
			if value != "" {
				if _, err := time.LoadLocation(value); err != nil {
					return errors.New("invalid IANA timezone")
				}
			}
		default:
			return fmt.Errorf("unknown setting: %s", key)
		}
	}
	return nil
}
func HTTPURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("a valid HTTP or HTTPS URL is required")
	}
	if len(raw) > 8192 {
		return errors.New("URL is too long")
	}
	return nil
}
func Webhook(h *database.Webhook) error {
	h.Name = strings.TrimSpace(h.Name)
	if h.Name == "" || len(h.Name) > 100 {
		return errors.New("webhook name is required (maximum 100 characters)")
	}
	switch h.Type {
	case "telegram":
		p := strings.SplitN(h.URL, "|", 2)
		if len(p) != 2 || strings.TrimSpace(p[0]) == "" || strings.TrimSpace(p[1]) == "" {
			return errors.New("telegram requires bot_token|chat_id")
		}
	case "discord", "slack", "gotify", "generic":
		if err := HTTPURL(h.URL); err != nil {
			return err
		}
	default:
		return errors.New("unsupported webhook type")
	}
	events := []string{}
	for _, event := range strings.Split(h.Events, ",") {
		event = strings.TrimSpace(event)
		switch event {
		case "ip_change", "error", "warning":
			events = append(events, event)
		default:
			return errors.New("invalid webhook event")
		}
	}
	if len(events) == 0 {
		return errors.New("select at least one event")
	}
	h.Events = strings.Join(events, ",")
	return nil
}
func IsSecret(f provider.ProviderField) bool {
	return f.Type == "password" || f.Name == "credentials" || f.Name == "url"
}
func Config(raw string) (map[string]any, error) {
	if !json.Valid([]byte(raw)) {
		return nil, errors.New("invalid provider configuration JSON")
	}
	m := map[string]any{}
	d := json.NewDecoder(bytes.NewBufferString(raw))
	d.UseNumber()
	if err := d.Decode(&m); err != nil || m == nil {
		return nil, errors.New("provider configuration must be a JSON object")
	}
	return m, nil
}

// Record converts browser field values once, then uses the actual provider constructor for validation.
func Record(rec *database.Record) error {
	domain, err := idna.Lookup.ToASCII(strings.TrimSuffix(strings.TrimSpace(rec.Domain), "."))
	if err != nil {
		return errors.New("invalid domain")
	}
	rec.Domain = strings.ToLower(domain)
	if len(rec.Domain) > 253 || !strings.Contains(rec.Domain, ".") {
		return errors.New("enter a complete domain name")
	}
	for _, label := range strings.Split(rec.Domain, ".") {
		if !labelRE.MatchString(label) {
			return errors.New("invalid domain label")
		}
	}
	rec.Owner = strings.ToLower(strings.TrimSpace(rec.Owner))
	if rec.Owner == "" {
		rec.Owner = "@"
	}
	if rec.Owner != "@" && rec.Owner != "*" {
		for _, label := range strings.Split(rec.Owner, ".") {
			if !labelRE.MatchString(label) {
				return errors.New("invalid host label")
			}
		}
	}
	if rec.Owner != "@" && len(rec.Owner)+len(rec.Domain)+1 > 253 {
		return errors.New("hostname is too long")
	}
	if rec.IPVersion == "both" {
		rec.IPVersion = "dual"
	}
	if rec.IPVersion != "ipv4" && rec.IPVersion != "ipv6" && rec.IPVersion != "dual" {
		return errors.New("invalid IP version")
	}
	if rec.Provider == "namecheap" && rec.IPVersion != "ipv4" {
		return errors.New("namecheap supports IPv4 only")
	}
	info := provider.GetProviderInfo(constants.Provider(rec.Provider))
	if info.Fields == nil {
		return errors.New("unknown provider")
	}
	config, err := Config(rec.ProviderConfig)
	if err != nil {
		return err
	}
	for _, f := range info.Fields {
		value, exists := config[f.Name]
		if f.Required && (!exists || value == "") {
			if rec.Provider != "spdyn" || config["token"] == nil {
				return fmt.Errorf("%s is required", f.Label)
			}
		}
		if !exists || value == "" {
			delete(config, f.Name)
			continue
		}
		if f.Type == "number" {
			if s, ok := value.(string); ok {
				n, err := strconv.ParseUint(s, 10, 32)
				if err != nil {
					return fmt.Errorf("invalid %s", f.Label)
				}
				config[f.Name] = n
			}
		}
		if f.Name == "credentials" {
			if s, ok := value.(string); ok {
				var obj map[string]any
				if json.Unmarshal([]byte(s), &obj) != nil || obj == nil {
					return errors.New("service account must be a JSON object")
				}
				config[f.Name] = obj
			}
		}
		if f.Name == "url" {
			s, ok := value.(string)
			if !ok {
				return errors.New("invalid URL")
			}
			if err := HTTPURL(s); err != nil {
				return err
			}
		}
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		return err
	}
	rec.ProviderConfig = string(encoded)
	_, err = provider.New(constants.Provider(rec.Provider), encoded, rec.Domain, rec.Owner, constants.IPVersion(rec.IPVersion))
	return err
}
func RedactedConfig(raw, providerName string) string {
	config, err := Config(raw)
	if err != nil {
		return "{}"
	}
	clean := map[string]any{}
	for _, field := range provider.GetProviderInfo(constants.Provider(providerName)).Fields {
		if !IsSecret(field) {
			if v, ok := config[field.Name]; ok {
				clean[field.Name] = v
			}
		}
	}
	encoded, _ := json.Marshal(clean)
	return string(encoded)
}
func MergeConfig(incoming, existing, providerName string) (string, error) {
	next, err := Config(incoming)
	if err != nil {
		return "", err
	}
	old, err := Config(existing)
	if err != nil {
		return "", err
	}
	for _, f := range provider.GetProviderInfo(constants.Provider(providerName)).Fields {
		if IsSecret(f) {
			value, ok := next[f.Name]
			if !ok || value == "" {
				if previous, ok := old[f.Name]; ok {
					next[f.Name] = previous
				}
			}
		}
	}
	// Preserve legacy optional settings that do not have an editable field yet.
	for k, v := range old {
		if _, ok := next[k]; !ok {
			next[k] = v
		}
	}
	data, err := json.Marshal(next)
	return string(data), err
}
func SecretValues(raw string) []string {
	var data map[string]any
	_ = json.Unmarshal([]byte(raw), &data)
	values := []string{}
	var collect func(any)
	collect = func(v any) {
		switch v := v.(type) {
		case string:
			values = append(values, v)
		case map[string]any:
			for _, x := range v {
				collect(x)
			}
		case []any:
			for _, x := range v {
				collect(x)
			}
		}
	}
	for key, value := range data {
		k := strings.ToLower(key)
		if strings.Contains(k, "key") || strings.Contains(k, "token") || strings.Contains(k, "password") || strings.Contains(k, "secret") || k == "url" || k == "credentials" {
			collect(value)
		}
	}
	return values
}
