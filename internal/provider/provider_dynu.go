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

type dynuProvider struct {
	domain    string
	owner     string
	ipVersion constants.IPVersion
	username  string
	password  string
}

func newDynu(data json.RawMessage, domain, owner string, ipVersion constants.IPVersion) (*dynuProvider, error) {
	var s struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("decoding dynu settings: %w", err)
	}
	if s.Username == "" {
		return nil, fmt.Errorf("dynu: username is required")
	}
	if s.Password == "" {
		return nil, fmt.Errorf("dynu: password is required")
	}
	return &dynuProvider{
		domain:    domain,
		owner:     owner,
		ipVersion: ipVersion,
		username:  s.Username,
		password:  s.Password,
	}, nil
}

func (p *dynuProvider) String() string                  { return string(constants.Dynu) }
func (p *dynuProvider) Domain() string                  { return p.domain }
func (p *dynuProvider) Owner() string                   { return p.owner }
func (p *dynuProvider) IPVersion() constants.IPVersion   { return p.ipVersion }
func (p *dynuProvider) Proxied() bool                   { return false }
func (p *dynuProvider) BuildDomainName() string {
	if p.owner == "@" || p.owner == "" {
		return p.domain
	}
	return p.owner + "." + p.domain
}

func (p *dynuProvider) Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error) {
	u := url.URL{
		Scheme: "https",
		Host:   "api.dynu.com",
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
	return netip.Addr{}, fmt.Errorf("dynu update failed: %s", s)
}
