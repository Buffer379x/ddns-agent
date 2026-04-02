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

type ionosProvider struct {
	domain    string
	owner     string
	ipVersion constants.IPVersion
	apiKey    string
}

func newIonos(data json.RawMessage, domain, owner string, ipVersion constants.IPVersion) (Provider, error) {
	var extra struct {
		APIKey string `json:"api_key"`
	}
	if err := json.Unmarshal(data, &extra); err != nil {
		return nil, err
	}
	if extra.APIKey == "" {
		return nil, fmt.Errorf("ionos: api_key is required")
	}
	return &ionosProvider{
		domain:    domain,
		owner:     owner,
		ipVersion: ipVersion,
		apiKey:    extra.APIKey,
	}, nil
}

func (p *ionosProvider) String() string                  { return string(constants.Ionos) }
func (p *ionosProvider) Domain() string                  { return p.domain }
func (p *ionosProvider) Owner() string                   { return p.owner }
func (p *ionosProvider) IPVersion() constants.IPVersion   { return p.ipVersion }
func (p *ionosProvider) Proxied() bool                   { return false }

func (p *ionosProvider) BuildDomainName() string {
	if p.owner == "@" || p.owner == "" {
		return p.domain
	}
	return p.owner + "." + p.domain
}

func (p *ionosProvider) Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error) {
	u := url.URL{
		Scheme: "https",
		Host:   "api.hosting.ionos.com",
		Path:   "/dns/v1/dyndns",
	}

	type domainEntry struct {
		Name string `json:"name"`
	}
	payload := struct {
		Domains []domainEntry `json:"domains"`
		// IONOS dyndns API uses description field for the IP
	}{
		Domains: []domainEntry{{Name: p.BuildDomainName()}},
	}

	buf := new(bytes.Buffer)
	if err := json.NewEncoder(buf).Encode(payload); err != nil {
		return netip.Addr{}, fmt.Errorf("encoding request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u.String(), buf)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("X-API-Key", p.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	// IONOS DynDNS uses a bulk update URL with the IP as a query parameter
	q := url.Values{}
	if ip.Is6() {
		q.Set("ipv6", ip.String())
	} else {
		q.Set("ipv4", ip.String())
	}
	req.URL.RawQuery = q.Encode()

	resp, err := client.Do(req)
	if err != nil {
		return netip.Addr{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		var apiErr struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&apiErr)
		return netip.Addr{}, fmt.Errorf("ionos: status %d: %s", resp.StatusCode, apiErr.Message)
	}

	return ip, nil
}
