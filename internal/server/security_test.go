package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"ddns-agent/internal/config"
)

func TestOnlyExplicitProxyNetworksMayForwardIdentity(t *testing.T) {
	for _, test := range []struct{ trusted, peer, chain, want string }{{"", "192.0.2.1:123", "198.51.100.9", "192.0.2.1:123"}, {"10.0.0.0/8", "10.1.2.3:123", "198.51.100.9, 10.2.3.4", "198.51.100.9"}, {"10.0.0.0/8", "10.1.2.3:123", "192.0.2.66, 198.51.100.9", "198.51.100.9"}} {
		handler := security(&config.Config{TrustedProxies: test.trusted})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.RemoteAddr != test.want {
				t.Fatalf("got %s want %s", r.RemoteAddr, test.want)
			}
		}))
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = test.peer
		r.Header.Set("X-Forwarded-For", test.chain)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Header().Get("Content-Security-Policy") == "" {
			t.Fatal("missing CSP")
		}
	}
}
