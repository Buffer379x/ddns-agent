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

type infomaniakProvider struct {
	domain    string
	owner     string
	ipVersion constants.IPVersion
	username  string
	password  string
}

func newInfomaniak(data json.RawMessage, domain, owner string, ipVersion constants.IPVersion) (Provider, error) {
	var extra struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(data, &extra); err != nil {
		return nil, err
	}
	if extra.Username == "" {
		return nil, fmt.Errorf("infomaniak: username is required")
	}
	if extra.Password == "" {
		return nil, fmt.Errorf("infomaniak: password is required")
	}
	return &infomaniakProvider{
		domain:    domain,
		owner:     owner,
		ipVersion: ipVersion,
		username:  extra.Username,
		password:  extra.Password,
	}, nil
}

func (p *infomaniakProvider) String() string                    { return string(constants.Infomaniak) }
func (p *infomaniakProvider) Domain() string                    { return p.domain }
func (p *infomaniakProvider) Owner() string                     { return p.owner }
func (p *infomaniakProvider) IPVersion() constants.IPVersion    { return p.ipVersion }
func (p *infomaniakProvider) Proxied() bool                     { return false }

func (p *infomaniakProvider) BuildDomainName() string {
	if p.owner == "@" || p.owner == "" {
		return p.domain
	}
	return p.owner + "." + p.domain
}

func (p *infomaniakProvider) Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error) {
	u := url.URL{
		Scheme: "https",
		Host:   "infomaniak.com",
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
		return netip.Addr{}, fmt.Errorf("infomaniak: status %d: %s", resp.StatusCode, s)
	}

	for _, prefix := range []string{"good", "nochg"} {
		if strings.HasPrefix(s, prefix) {
			return ip, nil
		}
	}
	if strings.Contains(s, "nohost") {
		return netip.Addr{}, fmt.Errorf("infomaniak: hostname not found")
	}
	if strings.Contains(s, "badauth") {
		return netip.Addr{}, fmt.Errorf("infomaniak: bad authentication")
	}
	return netip.Addr{}, fmt.Errorf("infomaniak: unexpected response: %q", s)
}
