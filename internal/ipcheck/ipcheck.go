package ipcheck

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Fetcher struct {
	mu        sync.RWMutex
	client    *http.Client
	providers []provider
	index     atomic.Uint64
}

type provider struct {
	name string
	url  string
}

var defaultProviders = []provider{
	{name: "ipify4", url: "https://api.ipify.org"},
	{name: "icanhazip4", url: "https://ipv4.icanhazip.com"},
	{name: "ifconfig4", url: "https://ifconfig.me/ip"},
	{name: "ipinfo4", url: "https://ipinfo.io/ip"},
	{name: "myip4", url: "https://api.my-ip.io/v2/ip.txt"},
}

var ipv6Providers = []provider{
	{name: "ipify6", url: "https://api6.ipify.org"},
	{name: "icanhazip6", url: "https://ipv6.icanhazip.com"},
	{name: "ifconfig6", url: "https://ifconfig.co/ip"},
}

func dialTimeoutForHTTP(d time.Duration) time.Duration {
	if d < 5*time.Second {
		return d
	}
	return 5 * time.Second
}

func New(timeout time.Duration) *Fetcher {
	return &Fetcher{
		client: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				DialContext: (&net.Dialer{Timeout: dialTimeoutForHTTP(timeout)}).DialContext,
			},
		},
		providers: defaultProviders,
	}
}

// SetHTTPTimeout replaces the HTTP client used for public IP lookups (hot-reload from settings).
func (f *Fetcher) SetHTTPTimeout(d time.Duration) {
	if d < time.Second {
		d = time.Second
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.client = &http.Client{
		Timeout: d,
		Transport: &http.Transport{
			DialContext: (&net.Dialer{Timeout: dialTimeoutForHTTP(d)}).DialContext,
		},
	}
}

func (f *Fetcher) IP(ctx context.Context) (netip.Addr, error) {
	return f.fetchWithRetry(ctx, f.providers, 3)
}

func (f *Fetcher) IPv4(ctx context.Context) (netip.Addr, error) {
	return f.fetchWithRetry(ctx, defaultProviders, 3)
}

func (f *Fetcher) IPv6(ctx context.Context) (netip.Addr, error) {
	return f.fetchWithRetry(ctx, ipv6Providers, 3)
}

func (f *Fetcher) fetchWithRetry(ctx context.Context, providers []provider, retries int) (netip.Addr, error) {
	var lastErr error
	for i := 0; i < retries; i++ {
		idx := f.index.Add(1) - 1
		p := providers[idx%uint64(len(providers))]

		ip, err := f.fetchFrom(ctx, p.url)
		if err == nil {
			return ip, nil
		}
		lastErr = fmt.Errorf("%s: %w", p.name, err)
		if i < retries-1 {
			select {
			case <-ctx.Done():
				return netip.Addr{}, ctx.Err()
			case <-time.After(time.Duration(i+1) * time.Second):
			}
		}
	}
	return netip.Addr{}, fmt.Errorf("all %d attempts failed, last: %w", retries, lastErr)
}

func (f *Fetcher) fetchFrom(ctx context.Context, url string) (netip.Addr, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return netip.Addr{}, err
	}

	f.mu.RLock()
	client := f.client
	f.mu.RUnlock()
	resp, err := client.Do(req)
	if err != nil {
		return netip.Addr{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return netip.Addr{}, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 256))
	if err != nil {
		return netip.Addr{}, err
	}

	ipStr := strings.TrimSpace(string(body))
	addr, err := netip.ParseAddr(ipStr)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("parsing %q: %w", ipStr, err)
	}
	return addr, nil
}
