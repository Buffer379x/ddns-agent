package webhook

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ddns-agent/internal/crypto"
	"ddns-agent/internal/database"
)

func TestSlackPayloadAndGotifyQueryPreserved(t *testing.T) {
	enc, _ := crypto.NewEncryptor(crypto.GenerateKey())
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var data map[string]any
		if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
			t.Fatal(err)
		}
		if requests == 1 {
			if data["text"] == nil || data["embeds"] != nil {
				t.Error("Slack received Discord payload")
			}
		} else {
			if r.URL.Path != "/base/message" || r.URL.Query().Get("token") != "private" {
				t.Error("Gotify URL lost its token")
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	s := New(nil, enc, nil)
	cipher, _ := enc.Encrypt(server.URL)
	if err := s.SendTest(database.Webhook{Type: "slack", URL: cipher}); err != nil {
		t.Fatal(err)
	}
	if err := s.SendTest(database.Webhook{Type: "gotify", URL: server.URL + "/base?token=private"}); err != nil {
		t.Fatal(err)
	}
	s.Close()
}
func TestDeliveryErrorsNeverRevealWebhookSecret(t *testing.T) {
	enc, _ := crypto.NewEncryptor(crypto.GenerateKey())
	s := New(nil, enc, nil)
	err := s.SendTest(database.Webhook{Type: "generic", URL: "http://127.0.0.1:1/private?token=do-not-log"})
	if err == nil {
		t.Fatal("expected delivery failure")
	}
	if strings.Contains(err.Error(), "do-not-log") {
		t.Fatal("secret leaked")
	}
}

func TestNotifyQueuesMoreThanEightSubscriptions(t *testing.T) {
	db, err := database.New(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	enc, _ := crypto.NewEncryptor(crypto.GenerateKey())
	received := make(chan struct{}, 12)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received <- struct{}{}; w.WriteHeader(http.StatusOK) }))
	defer server.Close()
	cipher, _ := enc.Encrypt(server.URL)
	for i := 0; i < 12; i++ {
		if _, err = db.CreateWebhook(&database.Webhook{Name: fmt.Sprint(i), Type: "generic", URL: cipher, Events: "error", Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	s := New(db, enc, func(message string) { t.Error(message) })
	defer s.Close()
	s.Notify("error", "test")
	deadline := time.After(2 * time.Second)
	for i := 0; i < 12; i++ {
		select {
		case <-received:
		case <-deadline:
			t.Fatalf("only %d subscriptions received the event", i)
		}
	}
}
