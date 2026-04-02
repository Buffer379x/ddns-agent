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

type dd24Provider struct {
	domain    string
	owner     string
	ipVersion constants.IPVersion
	password  string
}

func newDD24(data json.RawMessage, domain, owner string, ipVersion constants.IPVersion) (Provider, error) {
	var extra struct {
		Password string `json:"password"`
	}
	if err := json.Unmarshal(data, &extra); err != nil {
		return nil, err
	}
	if extra.Password == "" {
		return nil, fmt.Errorf("dd24: password is required")
	}
	return &dd24Provider{
		domain:    domain,
		owner:     owner,
		ipVersion: ipVersion,
		password:  extra.Password,
	}, nil
}

func (p *dd24Provider) String() string                    { return string(constants.DD24) }
func (p *dd24Provider) Domain() string                    { return p.domain }
func (p *dd24Provider) Owner() string                     { return p.owner }
func (p *dd24Provider) IPVersion() constants.IPVersion    { return p.ipVersion }
func (p *dd24Provider) Proxied() bool                     { return false }

func (p *dd24Provider) BuildDomainName() string {
	if p.owner == "@" || p.owner == "" {
		return p.domain
	}
	return p.owner + "." + p.domain
}

func (p *dd24Provider) Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error) {
	u := url.URL{
		Scheme: "https",
		Host:   "dynamicdns.key-systems.net",
		Path:   "/update.php",
	}
	q := url.Values{}
	q.Set("hostname", p.BuildDomainName())
	q.Set("password", p.password)
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
		return netip.Addr{}, fmt.Errorf("dd24: status %d: %s", resp.StatusCode, s)
	}

	for _, prefix := range []string{"good", "nochg"} {
		if strings.HasPrefix(s, prefix) {
			return ip, nil
		}
	}
	if strings.Contains(s, "nohost") {
		return netip.Addr{}, fmt.Errorf("dd24: hostname not found")
	}
	if strings.Contains(s, "badauth") {
		return netip.Addr{}, fmt.Errorf("dd24: bad authentication")
	}
	return netip.Addr{}, fmt.Errorf("dd24: unexpected response: %q", s)
}
