package netutil

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestResponseLimitAndCredentialRedaction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, strings.Repeat("x", MaxResponse+1)) }))
	defer server.Close()
	res, err := NewClient(time.Second).Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	_, err = io.ReadAll(res.Body)
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("body not bounded: %v", err)
	}
	safe := Redact("failed https://user:password@example.net/update?token=abc\ninjected abc", "abc")
	if strings.Contains(safe, "password") || strings.Contains(safe, "abc") || strings.Contains(safe, "\n") {
		t.Fatal(safe)
	}
}
func TestCrossOriginRedirectBlocked(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("followed cross-origin redirect") }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer server.Close()
	if _, err := NewClient(time.Second).Get(server.URL); err == nil {
		t.Fatal("accepted redirect")
	}
}
