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

type variomediaProvider struct {
	domain    string
	owner     string
	ipVersion constants.IPVersion
	email     string
	password  string
}

func newVariomedia(data json.RawMessage, domain, owner string, ipVersion constants.IPVersion) (Provider, error) {
	var extra struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(data, &extra); err != nil {
		return nil, err
	}
	if extra.Email == "" {
		return nil, fmt.Errorf("variomedia: email is required")
	}
	if extra.Password == "" {
		return nil, fmt.Errorf("variomedia: api token (password) is required")
	}
	return &variomediaProvider{
		domain:    domain,
		owner:     owner,
		ipVersion: ipVersion,
		email:     extra.Email,
		password:  extra.Password,
	}, nil
}

func (p *variomediaProvider) String() string                    { return string(constants.Variomedia) }
func (p *variomediaProvider) Domain() string                    { return p.domain }
func (p *variomediaProvider) Owner() string                     { return p.owner }
func (p *variomediaProvider) IPVersion() constants.IPVersion     { return p.ipVersion }
func (p *variomediaProvider) Proxied() bool                     { return false }

func (p *variomediaProvider) BuildDomainName() string {
	if p.owner == "@" || p.owner == "" {
		return p.domain
	}
	return p.owner + "." + p.domain
}

func (p *variomediaProvider) Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error) {
	host := "dyndns4.variomedia.de"
	if ip.Is6() {
		host = "dyndns6.variomedia.de"
	}

	u := url.URL{
		Scheme: "https",
		User:   url.UserPassword(p.email, p.password),
		Host:   host,
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

	if resp.StatusCode != http.StatusOK {
		return netip.Addr{}, fmt.Errorf("variomedia: status %d: %s", resp.StatusCode, s)
	}

	switch {
	case strings.HasPrefix(s, "good"):
		return ip, nil
	case strings.HasPrefix(s, "notfqdn"):
		return netip.Addr{}, fmt.Errorf("variomedia: hostname not found")
	case strings.HasPrefix(s, "badrequest"):
		return netip.Addr{}, fmt.Errorf("variomedia: bad request")
	default:
		return netip.Addr{}, fmt.Errorf("variomedia: unexpected response: %s", s)
	}
}
