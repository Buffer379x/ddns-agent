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

type domeneshopProvider struct {
	domain    string
	owner     string
	ipVersion constants.IPVersion
	token     string
	secret    string
}

func newDomeneshop(data json.RawMessage, domain, owner string, ipVersion constants.IPVersion) (Provider, error) {
	var extra struct {
		Token  string `json:"token"`
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(data, &extra); err != nil {
		return nil, err
	}
	if extra.Token == "" {
		return nil, fmt.Errorf("domeneshop: token is required")
	}
	if extra.Secret == "" {
		return nil, fmt.Errorf("domeneshop: secret is required")
	}
	return &domeneshopProvider{
		domain:    domain,
		owner:     owner,
		ipVersion: ipVersion,
		token:     extra.Token,
		secret:    extra.Secret,
	}, nil
}

func (p *domeneshopProvider) String() string                    { return string(constants.Domeneshop) }
func (p *domeneshopProvider) Domain() string                    { return p.domain }
func (p *domeneshopProvider) Owner() string                     { return p.owner }
func (p *domeneshopProvider) IPVersion() constants.IPVersion    { return p.ipVersion }
func (p *domeneshopProvider) Proxied() bool                     { return false }

func (p *domeneshopProvider) BuildDomainName() string {
	if p.owner == "@" || p.owner == "" {
		return p.domain
	}
	return p.owner + "." + p.domain
}

func (p *domeneshopProvider) getDomainAndRecordID(ctx context.Context, client *http.Client, recordType string) (int, int, error) {
	// List domains to find the domain ID
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.domeneshop.no/v0/domains", nil)
	if err != nil {
		return 0, 0, fmt.Errorf("creating request: %w", err)
	}
	req.SetBasicAuth(p.token, p.secret)

	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, 0, fmt.Errorf("domeneshop list domains: status %d", resp.StatusCode)
	}

	var domains []struct {
		ID     int    `json:"id"`
		Domain string `json:"domain"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&domains); err != nil {
		return 0, 0, fmt.Errorf("decoding domains: %w", err)
	}

	var domainID int
	for _, d := range domains {
		if d.Domain == p.domain {
			domainID = d.ID
			break
		}
	}
	if domainID == 0 {
		return 0, 0, fmt.Errorf("domeneshop: domain %s not found", p.domain)
	}

	// List DNS records
	recordsURL := fmt.Sprintf("https://api.domeneshop.no/v0/domains/%d/dns", domainID)
	req2, err := http.NewRequestWithContext(ctx, http.MethodGet, recordsURL, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("creating request: %w", err)
	}
	req2.SetBasicAuth(p.token, p.secret)

	resp2, err := client.Do(req2)
	if err != nil {
		return 0, 0, err
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		return 0, 0, fmt.Errorf("domeneshop list records: status %d", resp2.StatusCode)
	}

	var records []struct {
		ID   int    `json:"id"`
		Host string `json:"host"`
		Type string `json:"type"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&records); err != nil {
		return 0, 0, fmt.Errorf("decoding records: %w", err)
	}

	host := p.owner
	if host == "@" {
		host = ""
	}
	for _, rec := range records {
		if rec.Host == host && rec.Type == recordType {
			return domainID, rec.ID, nil
		}
	}
	return 0, 0, fmt.Errorf("domeneshop: no %s record found for %s", recordType, p.owner)
}

func (p *domeneshopProvider) Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error) {
	recordType := constants.A
	if ip.Is6() {
		recordType = constants.AAAA
	}

	domainID, recordID, err := p.getDomainAndRecordID(ctx, client, recordType)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("getting record: %w", err)
	}

	u := url.URL{
		Scheme: "https",
		Host:   "api.domeneshop.no",
		Path:   fmt.Sprintf("/v0/domains/%d/dns/%d", domainID, recordID),
	}

	host := p.owner
	if host == "@" {
		host = ""
	}
	payload := struct {
		Host string `json:"host"`
		Type string `json:"type"`
		Data string `json:"data"`
	}{
		Host: host,
		Type: recordType,
		Data: ip.String(),
	}

	buf := new(bytes.Buffer)
	if err := json.NewEncoder(buf).Encode(payload); err != nil {
		return netip.Addr{}, fmt.Errorf("encoding request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u.String(), buf)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("creating request: %w", err)
	}
	req.SetBasicAuth(p.token, p.secret)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return netip.Addr{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return netip.Addr{}, fmt.Errorf("domeneshop update: status %d", resp.StatusCode)
	}

	return ip, nil
}
