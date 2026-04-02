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

type easydnsProvider struct {
	domain    string
	owner     string
	ipVersion constants.IPVersion
	token     string
	key       string
}

func newEasyDNS(data json.RawMessage, domain, owner string, ipVersion constants.IPVersion) (*easydnsProvider, error) {
	var s struct {
		Token string `json:"token"`
		Key   string `json:"key"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("decoding easydns settings: %w", err)
	}
	if s.Token == "" {
		return nil, fmt.Errorf("easydns: token is required")
	}
	if s.Key == "" {
		return nil, fmt.Errorf("easydns: key is required")
	}
	return &easydnsProvider{
		domain:    domain,
		owner:     owner,
		ipVersion: ipVersion,
		token:     s.Token,
		key:       s.Key,
	}, nil
}

func (p *easydnsProvider) String() string                  { return string(constants.EasyDNS) }
func (p *easydnsProvider) Domain() string                  { return p.domain }
func (p *easydnsProvider) Owner() string                   { return p.owner }
func (p *easydnsProvider) IPVersion() constants.IPVersion   { return p.ipVersion }
func (p *easydnsProvider) Proxied() bool                   { return false }
func (p *easydnsProvider) BuildDomainName() string {
	if p.owner == "@" || p.owner == "" {
		return p.domain
	}
	return p.owner + "." + p.domain
}

func (p *easydnsProvider) Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error) {
	u := url.URL{
		Scheme: "https",
		Host:   "api.cp.easydns.com",
		Path:   "/dyn/generic.php",
	}
	values := url.Values{}
	values.Set("hostname", p.BuildDomainName())
	values.Set("myip", ip.String())
	values.Set("login", p.token)
	values.Set("password", p.key)
	u.RawQuery = values.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("creating http request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("doing http request: %w", err)
	}
	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("reading response: %w", err)
	}

	s := strings.TrimSpace(string(b))
	if strings.Contains(s, "NOERROR") || strings.Contains(s, "nochg") {
		return ip, nil
	}
	return netip.Addr{}, fmt.Errorf("easydns update failed: %s", s)
}
