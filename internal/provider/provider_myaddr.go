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

type myaddrProvider struct {
	domain    string
	owner     string
	ipVersion constants.IPVersion
	password  string
}

func newMyAddr(data json.RawMessage, domain, owner string, ipVersion constants.IPVersion) (Provider, error) {
	var extra struct {
		Password string `json:"password"`
	}
	if err := json.Unmarshal(data, &extra); err != nil {
		return nil, err
	}
	if extra.Password == "" {
		return nil, fmt.Errorf("myaddr: password is required")
	}
	return &myaddrProvider{
		domain:    domain,
		owner:     owner,
		ipVersion: ipVersion,
		password:  extra.Password,
	}, nil
}

func (p *myaddrProvider) String() string                    { return string(constants.MyAddr) }
func (p *myaddrProvider) Domain() string                    { return p.domain }
func (p *myaddrProvider) Owner() string                     { return p.owner }
func (p *myaddrProvider) IPVersion() constants.IPVersion     { return p.ipVersion }
func (p *myaddrProvider) Proxied() bool                     { return false }

func (p *myaddrProvider) BuildDomainName() string {
	if p.owner == "@" || p.owner == "" {
		return p.domain
	}
	return p.owner + "." + p.domain
}

func (p *myaddrProvider) Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error) {
	u := url.URL{
		Scheme: "https",
		Host:   "myaddr.tools",
		Path:   "/update",
	}
	q := url.Values{}
	q.Set("hostname", p.BuildDomainName())
	q.Set("password", p.password)
	q.Set("ip", ip.String())
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
		return netip.Addr{}, fmt.Errorf("myaddr: status %d: %s", resp.StatusCode, s)
	}

	switch {
	case strings.HasPrefix(s, "good"), strings.HasPrefix(s, "nochg"):
		return ip, nil
	case strings.HasPrefix(s, "nohost"):
		return netip.Addr{}, fmt.Errorf("myaddr: hostname not found")
	case strings.HasPrefix(s, "badauth"):
		return netip.Addr{}, fmt.Errorf("myaddr: authentication failed")
	default:
		return netip.Addr{}, fmt.Errorf("myaddr: unexpected response: %s", s)
	}
}
