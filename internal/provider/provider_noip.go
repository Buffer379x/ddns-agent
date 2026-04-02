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

type noipProvider struct {
	domain    string
	owner     string
	ipVersion constants.IPVersion
	username  string
	password  string
}

func newNoIP(data json.RawMessage, domain, owner string, ipVersion constants.IPVersion) (Provider, error) {
	var extra struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(data, &extra); err != nil {
		return nil, err
	}
	if extra.Username == "" || extra.Password == "" {
		return nil, fmt.Errorf("noip: username and password are required")
	}
	return &noipProvider{
		domain:    domain,
		owner:     owner,
		ipVersion: ipVersion,
		username:  extra.Username,
		password:  extra.Password,
	}, nil
}

func (p *noipProvider) String() string                  { return string(constants.NoIP) }
func (p *noipProvider) Domain() string                  { return p.domain }
func (p *noipProvider) Owner() string                   { return p.owner }
func (p *noipProvider) IPVersion() constants.IPVersion   { return p.ipVersion }
func (p *noipProvider) Proxied() bool                   { return false }

func (p *noipProvider) BuildDomainName() string {
	if p.owner == "@" || p.owner == "" {
		return p.domain
	}
	return p.owner + "." + p.domain
}

func (p *noipProvider) Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error) {
	u := url.URL{
		Scheme: "https",
		Host:   "dynupdate.no-ip.com",
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

	switch {
	case s == "":
		return netip.Addr{}, fmt.Errorf("noip: empty response")
	case s == "911":
		return netip.Addr{}, fmt.Errorf("noip: server-side error (911)")
	case s == "abuse":
		return netip.Addr{}, fmt.Errorf("noip: account blocked for abuse")
	case s == "badagent":
		return netip.Addr{}, fmt.Errorf("noip: bad user agent")
	case s == "badauth":
		return netip.Addr{}, fmt.Errorf("noip: authentication failed")
	case s == "nohost":
		return netip.Addr{}, fmt.Errorf("noip: hostname does not exist")
	case s == "!donator":
		return netip.Addr{}, fmt.Errorf("noip: feature not available")
	}

	if resp.StatusCode != http.StatusOK {
		return netip.Addr{}, fmt.Errorf("noip: status %d: %s", resp.StatusCode, s)
	}

	if !strings.HasPrefix(s, "good") && !strings.HasPrefix(s, "nochg") {
		return netip.Addr{}, fmt.Errorf("noip: unexpected response: %q", s)
	}

	parts := strings.Fields(s)
	if len(parts) >= 2 {
		newIP, err := netip.ParseAddr(parts[1])
		if err == nil {
			return newIP, nil
		}
	}

	return ip, nil
}
