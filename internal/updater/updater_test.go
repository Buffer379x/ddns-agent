package updater

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ddns-agent/internal/crypto"
	"ddns-agent/internal/database"
	"ddns-agent/internal/logger"
)

type fakeFetcher struct{ fail6 bool }

func (f *fakeFetcher) IPv4(context.Context) (netip.Addr, error) {
	return netip.MustParseAddr("203.0.113.5"), nil
}
func (f *fakeFetcher) IPv6(context.Context) (netip.Addr, error) {
	if f.fail6 {
		return netip.Addr{}, errors.New("no IPv6 route")
	}
	return netip.MustParseAddr("2001:db8::5"), nil
}
func (f *fakeFetcher) SetHTTPTimeout(time.Duration) {}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestBothFamiliesAndFailureRetryWithoutCooldown(t *testing.T) {
	db, err := database.New(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	enc, _ := crypto.NewEncryptor(crypto.GenerateKey())
	cipher, _ := enc.Encrypt(`{"token":"private-token"}`)
	id, err := db.CreateRecord(&database.Record{Provider: "duckdns", Domain: "example.duckdns.org", Owner: "@", IPVersion: "dual", ProviderConfig: cipher, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	fetcher := &fakeFetcher{fail6: true}
	s := New(db, fetcher, enc, logger.New(), nil, time.Minute, time.Hour, time.Second)
	var mu sync.Mutex
	calls := map[string]int{}
	s.providerClient.Store(&http.Client{Timeout: time.Second, Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		family := "ipv4"
		if r.URL.Query().Get("ipv6") != "" {
			family = "ipv6"
		}
		mu.Lock()
		calls[family]++
		mu.Unlock()
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("OK\n")), Header: http.Header{}}, nil
	})})
	s.updateAll(context.Background())
	r, _ := db.GetRecord(int(id))
	if r.Status != "error" || r.StatusIPv4 != "success" || r.StatusIPv6 != "error" {
		t.Fatalf("partial family status: %+v", r)
	}
	fetcher.fail6 = false
	s.updateAll(context.Background())
	r, _ = db.GetRecord(int(id))
	if r.Status != "success" || r.CurrentIPv6 == nil || *r.CurrentIPv6 != "2001:db8::5" {
		t.Fatalf("family did not retry: %+v", r)
	}
	if calls["ipv4"] != 1 || calls["ipv6"] != 1 {
		t.Fatalf("unexpected calls: %v", calls)
	}
	s.updateAll(context.Background())
	if calls["ipv4"] != 1 || calls["ipv6"] != 1 {
		t.Fatal("updated unchanged addresses")
	}
}
