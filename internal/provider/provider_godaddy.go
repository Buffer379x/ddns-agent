package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"

	"ddns-agent/internal/provider/constants"
)

type godaddyProvider struct {
	domain    string
	owner     string
	ipVersion constants.IPVersion
	key       string
	secret    string
}

func newGoDaddy(data json.RawMessage, domain, owner string, ipVersion constants.IPVersion) (Provider, error) {
	var extra struct {
		Key    string `json:"key"`
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(data, &extra); err != nil {
		return nil, err
	}
	if extra.Key == "" || extra.Secret == "" {
		return nil, fmt.Errorf("godaddy: key and secret are required")
	}
	return &godaddyProvider{
		domain:    domain,
		owner:     owner,
		ipVersion: ipVersion,
		key:       extra.Key,
		secret:    extra.Secret,
	}, nil
}

func (p *godaddyProvider) String() string                  { return string(constants.GoDaddy) }
func (p *godaddyProvider) Domain() string                  { return p.domain }
func (p *godaddyProvider) Owner() string                   { return p.owner }
func (p *godaddyProvider) IPVersion() constants.IPVersion   { return p.ipVersion }
func (p *godaddyProvider) Proxied() bool                   { return false }

func (p *godaddyProvider) BuildDomainName() string {
	if p.owner == "@" || p.owner == "" {
		return p.domain
	}
	return p.owner + "." + p.domain
}

func (p *godaddyProvider) Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error) {
	recordType := constants.A
	if ip.Is6() {
		recordType = constants.AAAA
	}

	name := p.owner
	if name == "" {
		name = "@"
	}

	u := url.URL{
		Scheme: "https",
		Host:   "api.godaddy.com",
		Path:   fmt.Sprintf("/v1/domains/%s/records/%s/%s", p.domain, recordType, name),
	}

	type record struct {
		Data string `json:"data"`
		TTL  int    `json:"ttl"`
	}
	payload := []record{{Data: ip.String(), TTL: 600}}

	buf := new(bytes.Buffer)
	if err := json.NewEncoder(buf).Encode(payload); err != nil {
		return netip.Addr{}, fmt.Errorf("encoding request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u.String(), buf)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", fmt.Sprintf("sso-key %s:%s", p.key, p.secret))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return netip.Addr{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		var apiErr struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&apiErr)
		return netip.Addr{}, fmt.Errorf("godaddy: status %d: %s - %s", resp.StatusCode, apiErr.Code, apiErr.Message)
	}

	return ip, nil
}
