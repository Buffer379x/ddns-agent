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

type nowdnsProvider struct {
	domain    string
	owner     string
	ipVersion constants.IPVersion
	username  string
	password  string
}

func newNowDNS(data json.RawMessage, domain, owner string, ipVersion constants.IPVersion) (Provider, error) {
	var extra struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(data, &extra); err != nil {
		return nil, err
	}
	if extra.Username == "" {
		return nil, fmt.Errorf("nowdns: username is required")
	}
	if extra.Password == "" {
		return nil, fmt.Errorf("nowdns: password is required")
	}
	return &nowdnsProvider{
		domain:    domain,
		owner:     owner,
		ipVersion: ipVersion,
		username:  extra.Username,
		password:  extra.Password,
	}, nil
}

func (p *nowdnsProvider) String() string                    { return string(constants.NowDNS) }
func (p *nowdnsProvider) Domain() string                    { return p.domain }
func (p *nowdnsProvider) Owner() string                     { return p.owner }
func (p *nowdnsProvider) IPVersion() constants.IPVersion     { return p.ipVersion }
func (p *nowdnsProvider) Proxied() bool                     { return false }

func (p *nowdnsProvider) BuildDomainName() string {
	if p.owner == "@" || p.owner == "" {
		return p.domain
	}
	return p.owner + "." + p.domain
}

func (p *nowdnsProvider) Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error) {
	u := url.URL{
		Scheme: "https",
		User:   url.UserPassword(p.username, p.password),
		Host:   "now-dns.com",
		Path:   "/nic/update",
	}
	q := url.Values{}
	q.Set("hostname", p.BuildDomainName())
	q.Set("myip", ip.String())
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

	switch {
	case strings.Contains(s, "good"), strings.Contains(s, "nochg"):
		return ip, nil
	case strings.Contains(s, "nohost"):
		return netip.Addr{}, fmt.Errorf("nowdns: hostname not found")
	case strings.Contains(s, "badauth"):
		return netip.Addr{}, fmt.Errorf("nowdns: authentication failed")
	default:
		return netip.Addr{}, fmt.Errorf("nowdns: unexpected response (status %d): %s", resp.StatusCode, s)
	}
}
