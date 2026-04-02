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

type goipProvider struct {
	domain    string
	owner     string
	ipVersion constants.IPVersion
	username  string
	password  string
}

func newGoIP(data json.RawMessage, domain, owner string, ipVersion constants.IPVersion) (Provider, error) {
	var extra struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(data, &extra); err != nil {
		return nil, err
	}
	if extra.Username == "" {
		return nil, fmt.Errorf("goip: username is required")
	}
	if extra.Password == "" {
		return nil, fmt.Errorf("goip: password is required")
	}
	return &goipProvider{
		domain:    domain,
		owner:     owner,
		ipVersion: ipVersion,
		username:  extra.Username,
		password:  extra.Password,
	}, nil
}

func (p *goipProvider) String() string                    { return string(constants.GoIP) }
func (p *goipProvider) Domain() string                    { return p.domain }
func (p *goipProvider) Owner() string                     { return p.owner }
func (p *goipProvider) IPVersion() constants.IPVersion    { return p.ipVersion }
func (p *goipProvider) Proxied() bool                     { return false }

func (p *goipProvider) BuildDomainName() string {
	if p.owner == "@" || p.owner == "" {
		return p.domain
	}
	return p.owner + "." + p.domain
}

func (p *goipProvider) Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error) {
	u := url.URL{
		Scheme: "https",
		Host:   "www.goip.de",
		Path:   "/setip",
	}
	q := url.Values{}
	q.Set("username", p.username)
	q.Set("password", p.password)
	q.Set("subdomain", p.owner)
	if ip.Is6() {
		q.Set("ip6", ip.String())
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
		return netip.Addr{}, fmt.Errorf("goip: status %d: %s", resp.StatusCode, s)
	}

	for _, prefix := range []string{"good", "nochg"} {
		if strings.HasPrefix(s, prefix) {
			return ip, nil
		}
	}
	if strings.Contains(s, "nohost") {
		return netip.Addr{}, fmt.Errorf("goip: hostname not found")
	}
	if strings.Contains(s, "badauth") {
		return netip.Addr{}, fmt.Errorf("goip: bad authentication")
	}
	return netip.Addr{}, fmt.Errorf("goip: unexpected response: %q", s)
}
