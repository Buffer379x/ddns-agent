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

type selfhostdeProvider struct {
	domain    string
	owner     string
	ipVersion constants.IPVersion
	username  string
	password  string
}

func newSelfhostDe(data json.RawMessage, domain, owner string, ipVersion constants.IPVersion) (Provider, error) {
	var extra struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(data, &extra); err != nil {
		return nil, err
	}
	if extra.Username == "" {
		return nil, fmt.Errorf("selfhost.de: username is required")
	}
	if extra.Password == "" {
		return nil, fmt.Errorf("selfhost.de: password is required")
	}
	return &selfhostdeProvider{
		domain:    domain,
		owner:     owner,
		ipVersion: ipVersion,
		username:  extra.Username,
		password:  extra.Password,
	}, nil
}

func (p *selfhostdeProvider) String() string                    { return string(constants.SelfhostDe) }
func (p *selfhostdeProvider) Domain() string                    { return p.domain }
func (p *selfhostdeProvider) Owner() string                     { return p.owner }
func (p *selfhostdeProvider) IPVersion() constants.IPVersion     { return p.ipVersion }
func (p *selfhostdeProvider) Proxied() bool                     { return false }

func (p *selfhostdeProvider) BuildDomainName() string {
	if p.owner == "@" || p.owner == "" {
		return p.domain
	}
	return p.owner + "." + p.domain
}

func (p *selfhostdeProvider) Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error) {
	u := url.URL{
		Scheme: "https",
		User:   url.UserPassword(p.username, p.password),
		Host:   "carol.selfhost.de",
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
		return netip.Addr{}, fmt.Errorf("selfhost.de: status %d: %s", resp.StatusCode, s)
	}

	switch {
	case strings.HasPrefix(s, "good"), strings.HasPrefix(s, "nochg"):
		return ip, nil
	case strings.HasPrefix(s, "nohost"), strings.HasPrefix(s, "notfqdn"):
		return netip.Addr{}, fmt.Errorf("selfhost.de: hostname not found: %s", s)
	case strings.HasPrefix(s, "badauth"):
		return netip.Addr{}, fmt.Errorf("selfhost.de: authentication failed")
	case strings.HasPrefix(s, "abuse"):
		return netip.Addr{}, fmt.Errorf("selfhost.de: account blocked for abuse")
	default:
		return netip.Addr{}, fmt.Errorf("selfhost.de: unexpected response: %s", s)
	}
}
