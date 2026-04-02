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

type zoneeditProvider struct {
	domain    string
	owner     string
	ipVersion constants.IPVersion
	username  string
	token     string
}

func newZoneEdit(data json.RawMessage, domain, owner string, ipVersion constants.IPVersion) (Provider, error) {
	var extra struct {
		Username string `json:"username"`
		Token    string `json:"token"`
	}
	if err := json.Unmarshal(data, &extra); err != nil {
		return nil, err
	}
	if extra.Username == "" {
		return nil, fmt.Errorf("zoneedit: username is required")
	}
	if extra.Token == "" {
		return nil, fmt.Errorf("zoneedit: token is required")
	}
	return &zoneeditProvider{
		domain:    domain,
		owner:     owner,
		ipVersion: ipVersion,
		username:  extra.Username,
		token:     extra.Token,
	}, nil
}

func (p *zoneeditProvider) String() string                    { return string(constants.ZoneEdit) }
func (p *zoneeditProvider) Domain() string                    { return p.domain }
func (p *zoneeditProvider) Owner() string                     { return p.owner }
func (p *zoneeditProvider) IPVersion() constants.IPVersion     { return p.ipVersion }
func (p *zoneeditProvider) Proxied() bool                     { return false }

func (p *zoneeditProvider) BuildDomainName() string {
	if p.owner == "@" || p.owner == "" {
		return p.domain
	}
	return p.owner + "." + p.domain
}

func (p *zoneeditProvider) Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error) {
	u := url.URL{
		Scheme: "https",
		User:   url.UserPassword(p.username, p.token),
		Host:   "dynamic.zoneedit.com",
		Path:   "/auth/dynamic.html",
	}
	q := url.Values{}
	q.Set("host", p.BuildDomainName())
	q.Set("dnsto", ip.String())
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
		return netip.Addr{}, fmt.Errorf("zoneedit: status %d: %s", resp.StatusCode, s)
	}

	switch {
	case strings.Contains(s, `success_code="200"`):
		return ip, nil
	case strings.Contains(s, `error code="702"`),
		strings.Contains(s, "minimum 600 seconds between requests"):
		return netip.Addr{}, fmt.Errorf("zoneedit: rate limited (10 minutes between requests)")
	case strings.Contains(s, `error code="709"`),
		strings.Contains(s, "invalid hostname"):
		return netip.Addr{}, fmt.Errorf("zoneedit: invalid hostname")
	case strings.Contains(s, `error code="708"`),
		strings.Contains(s, "failed login"):
		return netip.Addr{}, fmt.Errorf("zoneedit: authentication failed")
	case s == "":
		return netip.Addr{}, fmt.Errorf("zoneedit: empty response")
	default:
		return netip.Addr{}, fmt.Errorf("zoneedit: unexpected response: %s", s)
	}
}
