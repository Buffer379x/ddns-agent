package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"

	"ddns-agent/internal/provider/constants"
)

type porkbunProvider struct {
	domain string
	owner  string

	apiKey       string
	secretAPIKey string
}

func newPorkbun(data json.RawMessage, domain, owner string, ipVersion constants.IPVersion) (Provider, error) {
	var extra struct {
		APIKey       string `json:"api_key"`
		SecretAPIKey string `json:"secret_api_key"`
	}
	if err := json.Unmarshal(data, &extra); err != nil {
		return nil, err
	}
	if extra.APIKey == "" || extra.SecretAPIKey == "" {
		return nil, fmt.Errorf("porkbun: api_key and secret_api_key are required")
	}
	return &porkbunProvider{
		domain: domain,
		owner:  owner,

		apiKey:       extra.APIKey,
		secretAPIKey: extra.SecretAPIKey,
	}, nil
}

func (p *porkbunProvider) Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error) {
	recordType := constants.A
	if ip.Is6() {
		recordType = constants.AAAA
	}

	subdomain := p.owner
	if subdomain == "@" {
		subdomain = ""
	}

	pathParts := fmt.Sprintf("/api/json/v3/dns/editByNameType/%s/%s", p.domain, recordType)
	if subdomain != "" {
		pathParts += "/" + subdomain
	}

	u := url.URL{
		Scheme: "https",
		Host:   "api.porkbun.com",
		Path:   pathParts,
	}

	payload := struct {
		SecretAPIKey string `json:"secretapikey"`
		APIKey       string `json:"apikey"`
		Content      string `json:"content"`
		TTL          string `json:"ttl,omitempty"`
	}{
		SecretAPIKey: p.secretAPIKey,
		APIKey:       p.apiKey,
		Content:      ip.String(),
		TTL:          "300",
	}

	buf := new(bytes.Buffer)
	if err := json.NewEncoder(buf).Encode(payload); err != nil {
		return netip.Addr{}, fmt.Errorf("encoding request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), buf)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return netip.Addr{}, err
	}
	defer resp.Body.Close()
	if err := responseStatus(resp); err != nil {
		return netip.Addr{}, err
	}

	var body struct {
		Status  string `json:"status"`
		Message string `json:"message,omitempty"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&body); err != nil {
		return netip.Addr{}, fmt.Errorf("decoding response: %w", err)
	}

	if body.Status != "SUCCESS" {
		return netip.Addr{}, fmt.Errorf("porkbun: %s", body.Message)
	}

	return ip, nil
}
