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

type dnsomaticProvider struct {
	domain    string
	owner     string
	ipVersion constants.IPVersion
	username  string
	password  string
}

func newDNSOMatic(data json.RawMessage, domain, owner string, ipVersion constants.IPVersion) (*dnsomaticProvider, error) {
	var s struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("decoding dnsomatic settings: %w", err)
	}
	if s.Username == "" {
		return nil, fmt.Errorf("dnsomatic: username is required")
	}
	if s.Password == "" {
		return nil, fmt.Errorf("dnsomatic: password is required")
	}
	return &dnsomaticProvider{
		domain:    domain,
		owner:     owner,
		ipVersion: ipVersion,
		username:  s.Username,
		password:  s.Password,
	}, nil
}

func (p *dnsomaticProvider) String() string                  { return string(constants.DNSOMatic) }
func (p *dnsomaticProvider) Domain() string                  { return p.domain }
func (p *dnsomaticProvider) Owner() string                   { return p.owner }
func (p *dnsomaticProvider) IPVersion() constants.IPVersion   { return p.ipVersion }
func (p *dnsomaticProvider) Proxied() bool                   { return false }
func (p *dnsomaticProvider) BuildDomainName() string {
	if p.owner == "@" || p.owner == "" {
		return p.domain
	}
	return p.owner + "." + p.domain
}

func (p *dnsomaticProvider) Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error) {
	u := url.URL{
		Scheme: "https",
		Host:   "updates.dnsomatic.com",
		Path:   "/nic/update",
	}
	values := url.Values{}
	values.Set("hostname", p.BuildDomainName())
	values.Set("myip", ip.String())
	u.RawQuery = values.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("creating http request: %w", err)
	}
	req.SetBasicAuth(p.username, p.password)

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
	if strings.Contains(s, "good") || strings.Contains(s, "nochg") {
		return ip, nil
	}
	return netip.Addr{}, fmt.Errorf("dnsomatic update failed: %s", s)
}
