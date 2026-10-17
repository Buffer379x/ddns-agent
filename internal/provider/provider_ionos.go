package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"strings"

	"ddns-agent/internal/provider/constants"
)

type ionosProvider struct{ domain, owner, apiKey string }

func newIonos(raw json.RawMessage, domain, owner string, _ constants.IPVersion) (Provider, error) {
	var cfg struct {
		Key string `json:"api_key"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}
	if cfg.Key == "" {
		return nil, errors.New("ionos API key is required")
	}
	return &ionosProvider{domain: domain, owner: owner, apiKey: cfg.Key}, nil
}
func (p *ionosProvider) request(ctx context.Context, client *http.Client, method, path string, payload, out any) error {
	var body bytes.Buffer
	if payload != nil {
		if err := json.NewEncoder(&body).Encode(payload); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, "https://api.hosting.ionos.com/dns/v1"+path, &body)
	if err != nil {
		return err
	}
	req.Header.Set("X-API-Key", p.apiKey)
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	return decodeResponse(res, out)
}

// Use the documented DNS record API to update precisely one address family.
// Calling a DDNS registration endpoint on every cycle creates new bulk groups;
// source-address detection also cannot represent a separately discovered IPv6.
func (p *ionosProvider) Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error) {
	var zones []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := p.request(ctx, client, "GET", "/zones", nil, &zones); err != nil {
		return netip.Addr{}, err
	}
	zoneID := ""
	for _, z := range zones {
		if strings.EqualFold(strings.TrimSuffix(z.Name, "."), p.domain) {
			zoneID = z.ID
			break
		}
	}
	if zoneID == "" {
		return netip.Addr{}, errors.New("ionos: zone not found")
	}
	typ := "A"
	if ip.Is6() {
		typ = "AAAA"
	}
	name := domainName(p.domain, p.owner)
	var zone struct {
		Records []struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Type     string `json:"type"`
			Content  string `json:"content"`
			TTL      int    `json:"ttl"`
			Disabled bool   `json:"disabled"`
		} `json:"records"`
	}
	base := "/zones/" + url.PathEscape(zoneID)
	query := url.Values{"recordName": {name}, "recordType": {typ}}
	if err := p.request(ctx, client, "GET", base+"?"+query.Encode(), nil, &zone); err != nil {
		return netip.Addr{}, err
	}
	for _, r := range zone.Records {
		if r.Type != typ || !strings.EqualFold(strings.TrimSuffix(r.Name, "."), name) {
			continue
		}
		if r.Disabled {
			return netip.Addr{}, errors.New("ionos: matching DNS record is disabled")
		}
		if r.Content == ip.String() {
			return ip, nil
		}
		payload := map[string]any{"content": ip.String(), "ttl": r.TTL, "disabled": false}
		if err := p.request(ctx, client, "PUT", base+"/records/"+url.PathEscape(r.ID), payload, nil); err != nil {
			return netip.Addr{}, err
		}
		return ip, nil
	}
	// Creating a missing record is intentional only after a successful, exact lookup.
	payload := []map[string]any{{"name": name, "type": typ, "content": ip.String(), "ttl": 300, "disabled": false}}
	if err := p.request(ctx, client, "POST", base+"/records", payload, nil); err != nil {
		return netip.Addr{}, fmt.Errorf("ionos: creating record: %w", err)
	}
	return ip, nil
}
