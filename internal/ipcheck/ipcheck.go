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

// Fetcher retrieves the machine's public IP address from well-known external services
// using a round-robin strategy with per-attempt retry backoff.
type Fetcher struct {
	mu         sync.RWMutex
	client     *http.Client // default (IPv4 / dual-stack) client
	ipv6Client *http.Client // forces connections over IPv6 only
	index      atomic.Uint64
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

// dialTimeoutForHTTP returns a dial timeout that is at most 5 seconds, capped
// below the overall HTTP timeout so a slow DNS lookup doesn't consume the budget.
func dialTimeoutForHTTP(d time.Duration) time.Duration {
	if d < 5*time.Second {
		return d
	}
	return 5 * time.Second
}

func newIPv6Transport(timeout time.Duration) *http.Transport {
	return &http.Transport{
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			// Force IPv6 by dialing "tcp6" instead of the default "tcp".
			return (&net.Dialer{Timeout: dialTimeoutForHTTP(timeout)}).DialContext(ctx, "tcp6", addr)
		},
	}
}

func New(timeout time.Duration) *Fetcher {
	return &Fetcher{
		client: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				DialContext: (&net.Dialer{Timeout: dialTimeoutForHTTP(timeout)}).DialContext,
			},
		},
		ipv6Client: &http.Client{
			Timeout:   timeout,
			Transport: newIPv6Transport(timeout),
		},
	}
}

// SetHTTPTimeout hot-reloads the HTTP client timeout (called from settings update).
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
	f.ipv6Client = &http.Client{
		Timeout:   d,
		Transport: newIPv6Transport(d),
	}
}

func (f *Fetcher) IPv4(ctx context.Context) (netip.Addr, error) {
	return f.fetchWithRetry(ctx, defaultProviders, 3)
}

func (f *Fetcher) IPv6(ctx context.Context) (netip.Addr, error) {
	f.mu.RLock()
	client := f.ipv6Client
	f.mu.RUnlock()

	addr, err := f.fetchWithRetryClient(ctx, ipv6Providers, 3, client)
	if err != nil {
		return netip.Addr{}, err
	}
	// Validate that we actually got a real IPv6 address, not an IPv4 or
	// IPv4-mapped address (e.g. ::ffff:188.252.205.13).
	if addr.Is4() || addr.Is4In6() {
		return netip.Addr{}, fmt.Errorf("no IPv6 address available (got IPv4 %s)", addr)
	}
	return addr, nil
}

func (f *Fetcher) fetchWithRetry(ctx context.Context, providers []provider, retries int) (netip.Addr, error) {
	f.mu.RLock()
	client := f.client
	f.mu.RUnlock()
	return f.fetchWithRetryClient(ctx, providers, retries, client)
}

func (f *Fetcher) fetchWithRetryClient(ctx context.Context, providers []provider, retries int, client *http.Client) (netip.Addr, error) {
	var lastErr error
	for i := 0; i < retries; i++ {
		idx := f.index.Add(1) - 1
		p := providers[idx%uint64(len(providers))]

		ip, err := f.fetchFromClient(ctx, p.url, client)
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

func (f *Fetcher) fetchFromClient(ctx context.Context, url string, client *http.Client) (netip.Addr, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return netip.Addr{}, err
	}

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
