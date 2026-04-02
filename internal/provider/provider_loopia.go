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

type loopiaProvider struct {
	domain    string
	owner     string
	ipVersion constants.IPVersion
	username  string
	password  string
}

func newLoopia(data json.RawMessage, domain, owner string, ipVersion constants.IPVersion) (Provider, error) {
	var extra struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(data, &extra); err != nil {
		return nil, err
	}
	if extra.Username == "" {
		return nil, fmt.Errorf("loopia: username is required")
	}
	if extra.Password == "" {
		return nil, fmt.Errorf("loopia: password is required")
	}
	return &loopiaProvider{
		domain:    domain,
		owner:     owner,
		ipVersion: ipVersion,
		username:  extra.Username,
		password:  extra.Password,
	}, nil
}

func (p *loopiaProvider) String() string                    { return string(constants.Loopia) }
func (p *loopiaProvider) Domain() string                    { return p.domain }
func (p *loopiaProvider) Owner() string                     { return p.owner }
func (p *loopiaProvider) IPVersion() constants.IPVersion     { return p.ipVersion }
func (p *loopiaProvider) Proxied() bool                     { return false }

func (p *loopiaProvider) BuildDomainName() string {
	if p.owner == "@" || p.owner == "" {
		return p.domain
	}
	return p.owner + "." + p.domain
}

func (p *loopiaProvider) Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error) {
	u := url.URL{
		Scheme: "https",
		User:   url.UserPassword(p.username, p.password),
		Host:   "dyndns.loopia.se",
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
		return netip.Addr{}, fmt.Errorf("loopia: status %d: %s", resp.StatusCode, s)
	}

	switch {
	case strings.HasPrefix(s, "good"), strings.HasPrefix(s, "nochg"):
		return ip, nil
	case strings.HasPrefix(s, "nohost"), strings.HasPrefix(s, "notfqdn"):
		return netip.Addr{}, fmt.Errorf("loopia: hostname not found: %s", s)
	case strings.HasPrefix(s, "badauth"):
		return netip.Addr{}, fmt.Errorf("loopia: authentication failed")
	case strings.HasPrefix(s, "911"):
		return netip.Addr{}, fmt.Errorf("loopia: server error: %s", s)
	default:
		return netip.Addr{}, fmt.Errorf("loopia: unexpected response: %s", s)
	}
}
