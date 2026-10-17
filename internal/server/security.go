package server

import (
	"net"
	"net/http"
	"net/netip"
	"strings"

	"ddns-agent/internal/config"
)

func security(cfg *config.Config) func(http.Handler) http.Handler {
	trusted := []netip.Prefix{}
	for _, raw := range strings.Split(cfg.TrustedProxies, ",") {
		if p, err := netip.ParsePrefix(strings.TrimSpace(raw)); err == nil {
			trusted = append(trusted, p)
		}
	}
	isTrusted := func(ip netip.Addr) bool {
		for _, p := range trusted {
			if p.Contains(ip.Unmap()) {
				return true
			}
		}
		return false
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host, _, _ := net.SplitHostPort(r.RemoteAddr)
			peer, err := netip.ParseAddr(host)
			if err == nil && isTrusted(peer) {
				chain := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
				ip := peer
				for i := len(chain) - 1; i >= 0 && isTrusted(ip); i-- {
					candidate, e := netip.ParseAddr(strings.TrimSpace(chain[i]))
					if e != nil {
						break
					}
					ip = candidate
				}
				r.RemoteAddr = ip.String()
				if r.Header.Get("X-Forwarded-Proto") == "https" {
					r.URL.Scheme = "https"
				}
			}
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("X-Frame-Options", "DENY")
			w.Header().Set("Referrer-Policy", "no-referrer")
			w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
			w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
			if strings.HasPrefix(r.URL.Path, "/api/") {
				w.Header().Set("Cache-Control", "no-store")
			}
			if r.Body != nil {
				limit := int64(1 << 20)
				if strings.HasPrefix(r.URL.Path, "/api/config/") {
					limit = 16 << 20
				}
				r.Body = http.MaxBytesReader(w, r.Body, limit)
			}
			next.ServeHTTP(w, r)
		})
	}
}
