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

type changeipProvider struct {
	domain    string
	owner     string
	ipVersion constants.IPVersion
	username  string
	password  string
}

func newChangeIP(data json.RawMessage, domain, owner string, ipVersion constants.IPVersion) (Provider, error) {
	var extra struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(data, &extra); err != nil {
		return nil, err
	}
	if extra.Username == "" {
		return nil, fmt.Errorf("changeip: username is required")
	}
	if extra.Password == "" {
		return nil, fmt.Errorf("changeip: password is required")
	}
	return &changeipProvider{
		domain:    domain,
		owner:     owner,
		ipVersion: ipVersion,
		username:  extra.Username,
		password:  extra.Password,
	}, nil
}

func (p *changeipProvider) String() string                    { return string(constants.ChangeIP) }
func (p *changeipProvider) Domain() string                    { return p.domain }
func (p *changeipProvider) Owner() string                     { return p.owner }
func (p *changeipProvider) IPVersion() constants.IPVersion    { return p.ipVersion }
func (p *changeipProvider) Proxied() bool                     { return false }

func (p *changeipProvider) BuildDomainName() string {
	if p.owner == "@" || p.owner == "" {
		return p.domain
	}
	return p.owner + "." + p.domain
}

func (p *changeipProvider) Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error) {
	u := url.URL{
		Scheme: "https",
		Host:   "nic.changeip.com",
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
	req.SetBasicAuth(p.username, p.password)

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
		return netip.Addr{}, fmt.Errorf("changeip: status %d: %s", resp.StatusCode, s)
	}

	for _, prefix := range []string{"good", "nochg"} {
		if strings.HasPrefix(s, prefix) {
			return ip, nil
		}
	}
	if strings.Contains(s, "nohost") {
		return netip.Addr{}, fmt.Errorf("changeip: hostname not found")
	}
	if strings.Contains(s, "badauth") {
		return netip.Addr{}, fmt.Errorf("changeip: bad authentication")
	}
	return netip.Addr{}, fmt.Errorf("changeip: unexpected response: %q", s)
}
