package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ddns-agent/internal/auth"
	"ddns-agent/internal/backup"
	"ddns-agent/internal/config"
	"ddns-agent/internal/crypto"
	"ddns-agent/internal/database"
	"ddns-agent/internal/ipcheck"
	"ddns-agent/internal/logger"
	"ddns-agent/internal/server/sse"
	"ddns-agent/internal/updater"
	"ddns-agent/internal/webhook"
	"ddns-agent/web"
)

func TestAPIValidationSecretsAndImportSafety(t *testing.T) {
	dir := t.TempDir()
	db, err := database.New(filepath.Join(dir, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	enc, _ := crypto.NewEncryptor(crypto.GenerateKey())
	svc := auth.NewService(strings.Repeat("s", 32), db)
	hash, _ := auth.HashPassword("correct-password")
	admin, _ := db.CreateUser("admin", hash, "admin")
	viewer, _ := db.CreateUser("viewer", hash, "viewer")
	token, _ := svc.GenerateToken(admin.ID, "admin", "admin")
	viewToken, _ := svc.GenerateToken(viewer.ID, "viewer", "viewer")
	log := logger.New()
	hooks := webhook.New(db, enc, nil)
	defer hooks.Close()
	updates := updater.New(db, ipcheck.New(time.Second), enc, log, hooks, time.Minute, time.Minute, time.Second)
	backups := backup.New(db, filepath.Join(dir, "backups"), 3, enc)
	router := NewRouter(db, svc, updates, hooks, backups, enc, sse.NewBroker(2), log, web.FS, "1.1.0", &config.Config{DataDir: dir})
	request := func(method, path string, body any, session string) *httptest.ResponseRecorder {
		t.Helper()
		var payload io.Reader
		if body != nil {
			data, _ := json.Marshal(body)
			payload = bytes.NewReader(data)
		}
		r := httptest.NewRequest(method, path, payload)
		r.RemoteAddr = "192.0.2.1:1234"
		if session != "" {
			r.Header.Set("Authorization", "Bearer "+session)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	payload := map[string]any{"provider": "cloudflare", "domain": "example.net", "owner": "home", "ip_version": "dual", "enabled": true, "provider_config": `{"zone_identifier":"zone","token":"private-api-secret","ttl":"300"}`}
	created := request("POST", "/api/records", payload, token)
	if created.Code != 201 {
		t.Fatal(created.Code, created.Body)
	}
	if strings.Contains(created.Body.String(), "private-api-secret") {
		t.Fatal("create response exposed token")
	}
	var rec database.Record
	json.Unmarshal(created.Body.Bytes(), &rec)
	stored, _ := db.GetRecord(rec.ID)
	if !strings.HasPrefix(stored.ProviderConfig, "v1:") {
		t.Fatal("unencrypted credential")
	}
	for _, path := range []string{"/api/records", "/api/records/1"} {
		result := request("GET", path, nil, token)
		if result.Code != 200 || strings.Contains(result.Body.String(), "private-api-secret") {
			t.Fatalf("credential read leak: %d %s", result.Code, result.Body)
		}
	}
	if request("GET", "/api/records/1", nil, viewToken).Code != 403 || request("POST", "/api/records", payload, viewToken).Code != 403 {
		t.Fatal("viewer modified config")
	}
	payload["provider_config"] = `{"ttl":"600"}`
	updated := request("PUT", "/api/records/1", payload, token)
	if updated.Code != 200 {
		t.Fatal(updated.Body)
	}
	stored, _ = db.GetRecord(1)
	plain, _ := enc.OpenConfig(stored.ProviderConfig)
	if !strings.Contains(plain, "private-api-secret") || !strings.Contains(plain, "600") {
		t.Fatal("blank secret erased stored token")
	}
	if request("PUT", "/api/settings", map[string]string{"refresh_interval": "0"}, token).Code != 400 {
		t.Fatal("invalid interval accepted")
	}
	if request("POST", "/api/config/import?records=replace", map[string]any{}, token).Code != 400 {
		t.Fatal("empty import accepted")
	}
	records, _ := db.ListRecords()
	if len(records) != 1 {
		t.Fatal("failed import erased records")
	}
	hook := request("POST", "/api/webhooks", map[string]any{"name": "alerts", "type": "generic", "url": "https://notify.example.net/path?token=private-webhook", "events": "error", "enabled": true}, token)
	if hook.Code != 201 {
		t.Fatal(hook.Body)
	}
	listed := request("GET", "/api/webhooks", nil, token)
	if strings.Contains(listed.Body.String(), "private-webhook") {
		t.Fatal("webhook token exposed")
	}
	storedHook, _ := db.GetWebhook(1)
	if !strings.HasPrefix(storedHook.URL, "v1:") {
		t.Fatal("webhook not encrypted")
	}
	// Cookie sessions require Origin for mutation; bearer clients remain supported.
	r := httptest.NewRequest("POST", "http://app.example/api/records", bytes.NewReader([]byte(`{}`)))
	r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cookie CSRF accepted")
	}
}
