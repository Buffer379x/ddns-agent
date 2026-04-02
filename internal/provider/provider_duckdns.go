package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"

	"ddns-agent/internal/provider/constants"
)

type duckdnsProvider struct {
	domain    string
	owner     string
	ipVersion constants.IPVersion
	token     string
}

func newDuckDNS(data json.RawMessage, domain, owner string, ipVersion constants.IPVersion) (Provider, error) {
	var extra struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(data, &extra); err != nil {
		return nil, err
	}
	if extra.Token == "" {
		return nil, fmt.Errorf("duckdns: token is required")
	}
	return &duckdnsProvider{
		domain:    domain,
		owner:     owner,
		ipVersion: ipVersion,
		token:     extra.Token,
	}, nil
}

func (p *duckdnsProvider) String() string                  { return string(constants.DuckDNS) }
func (p *duckdnsProvider) Domain() string                  { return p.domain }
func (p *duckdnsProvider) Owner() string                   { return p.owner }
func (p *duckdnsProvider) IPVersion() constants.IPVersion   { return p.ipVersion }
func (p *duckdnsProvider) Proxied() bool                   { return false }

func (p *duckdnsProvider) BuildDomainName() string {
	if p.owner == "@" || p.owner == "" {
		return p.domain
	}
	return p.owner + "." + p.domain
}

func (p *duckdnsProvider) Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error) {
	u := url.URL{
		Scheme: "https",
		Host:   "www.duckdns.org",
		Path:   "/update",
	}
	q := url.Values{}
	q.Set("domains", p.BuildDomainName())
	q.Set("token", p.token)
	q.Set("verbose", "true")
	if ip.Is6() {
		q.Set("ipv6", ip.String())
	} else {
		q.Set("ip", ip.String())
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("creating request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return netip.Addr{}, err
	}
	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("reading response: %w", err)
	}
	s := strings.TrimSpace(string(b))

	if resp.StatusCode != http.StatusOK {
		return netip.Addr{}, fmt.Errorf("duckdns: status %d: %s", resp.StatusCode, s)
	}

	if len(s) < 2 {
		return netip.Addr{}, fmt.Errorf("duckdns: response too short: %q", s)
	}
	if strings.HasPrefix(s, "KO") || strings.HasPrefix(s, "ko") {
		return netip.Addr{}, fmt.Errorf("duckdns: authentication failed")
	}
	if !strings.HasPrefix(s, "OK") && !strings.HasPrefix(s, "ok") {
		return netip.Addr{}, fmt.Errorf("duckdns: unexpected response: %q", s)
	}

	return ip, nil
}
