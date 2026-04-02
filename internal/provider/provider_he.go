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

type heProvider struct {
	domain    string
	owner     string
	ipVersion constants.IPVersion
	password  string
}

func newHE(data json.RawMessage, domain, owner string, ipVersion constants.IPVersion) (*heProvider, error) {
	var s struct {
		Password string `json:"password"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("decoding he settings: %w", err)
	}
	if s.Password == "" {
		return nil, fmt.Errorf("he: password is required")
	}
	return &heProvider{
		domain:    domain,
		owner:     owner,
		ipVersion: ipVersion,
		password:  s.Password,
	}, nil
}

func (p *heProvider) String() string                  { return string(constants.HE) }
func (p *heProvider) Domain() string                  { return p.domain }
func (p *heProvider) Owner() string                   { return p.owner }
func (p *heProvider) IPVersion() constants.IPVersion   { return p.ipVersion }
func (p *heProvider) Proxied() bool                   { return false }
func (p *heProvider) BuildDomainName() string {
	if p.owner == "@" || p.owner == "" {
		return p.domain
	}
	return p.owner + "." + p.domain
}

func (p *heProvider) Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error) {
	fqdn := p.BuildDomainName()

	form := url.Values{}
	form.Set("hostname", fqdn)
	form.Set("password", p.password)
	form.Set("myip", ip.String())

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://dyn.dns.he.net/nic/update", strings.NewReader(form.Encode()))
	if err != nil {
		return netip.Addr{}, fmt.Errorf("creating http request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

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
	if s == "badauth" {
		return netip.Addr{}, fmt.Errorf("he: authentication failed")
	}
	return netip.Addr{}, fmt.Errorf("he update failed: %s", s)
}
