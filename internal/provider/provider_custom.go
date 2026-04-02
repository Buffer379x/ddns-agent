package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strings"

	"ddns-agent/internal/provider/constants"
)

type customProvider struct {
	domain    string
	owner     string
	ipVersion constants.IPVersion
	url       string
}

func newCustom(data json.RawMessage, domain, owner string, ipVersion constants.IPVersion) (Provider, error) {
	var extra struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(data, &extra); err != nil {
		return nil, err
	}
	if extra.URL == "" {
		return nil, fmt.Errorf("custom: url is required")
	}
	return &customProvider{
		domain:    domain,
		owner:     owner,
		ipVersion: ipVersion,
		url:       extra.URL,
	}, nil
}

func (p *customProvider) String() string                    { return string(constants.Custom) }
func (p *customProvider) Domain() string                    { return p.domain }
func (p *customProvider) Owner() string                     { return p.owner }
func (p *customProvider) IPVersion() constants.IPVersion     { return p.ipVersion }
func (p *customProvider) Proxied() bool                     { return false }

func (p *customProvider) BuildDomainName() string {
	if p.owner == "@" || p.owner == "" {
		return p.domain
	}
	return p.owner + "." + p.domain
}

func (p *customProvider) Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error) {
	ipv4 := ""
	ipv6 := ""
	if ip.Is4() {
		ipv4 = ip.String()
	} else {
		ipv6 = ip.String()
	}

	r := strings.NewReplacer(
		"{ip}", ip.String(),
		"{domain}", p.domain,
		"{owner}", p.owner,
		"{ipv4}", ipv4,
		"{ipv6}", ipv6,
	)
	targetURL := r.Replace(p.url)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
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
		return netip.Addr{}, fmt.Errorf("custom: status %d: %s", resp.StatusCode, s)
	}

	return ip, nil
}
