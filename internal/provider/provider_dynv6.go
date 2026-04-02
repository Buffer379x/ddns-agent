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

type dynv6Provider struct {
	domain    string
	owner     string
	ipVersion constants.IPVersion
	token     string
}

func newDynV6(data json.RawMessage, domain, owner string, ipVersion constants.IPVersion) (*dynv6Provider, error) {
	var s struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("decoding dynv6 settings: %w", err)
	}
	if s.Token == "" {
		return nil, fmt.Errorf("dynv6: token is required")
	}
	return &dynv6Provider{
		domain:    domain,
		owner:     owner,
		ipVersion: ipVersion,
		token:     s.Token,
	}, nil
}

func (p *dynv6Provider) String() string                  { return string(constants.DynV6) }
func (p *dynv6Provider) Domain() string                  { return p.domain }
func (p *dynv6Provider) Owner() string                   { return p.owner }
func (p *dynv6Provider) IPVersion() constants.IPVersion   { return p.ipVersion }
func (p *dynv6Provider) Proxied() bool                   { return false }
func (p *dynv6Provider) BuildDomainName() string {
	if p.owner == "@" || p.owner == "" {
		return p.domain
	}
	return p.owner + "." + p.domain
}

func (p *dynv6Provider) Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error) {
	u := url.URL{
		Scheme: "https",
		Host:   "dynv6.com",
		Path:   "/api/update",
	}
	values := url.Values{}
	values.Set("hostname", p.BuildDomainName())
	values.Set("token", p.token)
	if ip.Is6() {
		values.Set("ipv6", ip.String())
	} else {
		values.Set("ipv4", ip.String())
	}
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
	if strings.Contains(s, "good") || strings.Contains(s, "nochg") || strings.HasPrefix(s, "addresses updated") {
		return ip, nil
	}
	return netip.Addr{}, fmt.Errorf("dynv6 update failed: %s", s)
}
