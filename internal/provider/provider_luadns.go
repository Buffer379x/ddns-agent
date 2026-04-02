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

type luadnsProvider struct {
	domain    string
	owner     string
	ipVersion constants.IPVersion
	email     string
	token     string
}

func newLuaDNS(data json.RawMessage, domain, owner string, ipVersion constants.IPVersion) (*luadnsProvider, error) {
	var s struct {
		Email string `json:"email"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("decoding luadns settings: %w", err)
	}
	if s.Email == "" {
		return nil, fmt.Errorf("luadns: email is required")
	}
	if s.Token == "" {
		return nil, fmt.Errorf("luadns: token is required")
	}
	return &luadnsProvider{
		domain:    domain,
		owner:     owner,
		ipVersion: ipVersion,
		email:     s.Email,
		token:     s.Token,
	}, nil
}

func (p *luadnsProvider) String() string                  { return string(constants.LuaDNS) }
func (p *luadnsProvider) Domain() string                  { return p.domain }
func (p *luadnsProvider) Owner() string                   { return p.owner }
func (p *luadnsProvider) IPVersion() constants.IPVersion   { return p.ipVersion }
func (p *luadnsProvider) Proxied() bool                   { return false }
func (p *luadnsProvider) BuildDomainName() string {
	if p.owner == "@" || p.owner == "" {
		return p.domain
	}
	return p.owner + "." + p.domain
}

type luadnsRecord struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
}

func (p *luadnsProvider) luadnsURL(path string) string {
	u := url.URL{
		Scheme: "https",
		Host:   "api.luadns.com",
		Path:   path,
		User:   url.UserPassword(p.email, p.token),
	}
	return u.String()
}

func (p *luadnsProvider) Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error) {
	zoneID, err := p.getZoneID(ctx, client)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("getting luadns zone: %w", err)
	}

	recordType := constants.A
	if ip.Is6() {
		recordType = constants.AAAA
	}

	record, err := p.findRecord(ctx, client, zoneID, recordType)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("getting luadns record: %w", err)
	}

	record.Content = ip.String()
	if err := p.updateLuaDNSRecord(ctx, client, zoneID, record); err != nil {
		return netip.Addr{}, fmt.Errorf("updating luadns record: %w", err)
	}
	return ip, nil
}

func (p *luadnsProvider) getZoneID(ctx context.Context, client *http.Client) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.luadnsURL("/v1/zones"), nil)
	if err != nil {
		return 0, fmt.Errorf("creating http request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("doing http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("luadns list zones: HTTP %d: %s", resp.StatusCode, string(b))
	}

	var zones []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&zones); err != nil {
		return 0, fmt.Errorf("decoding luadns zones: %w", err)
	}
	for _, z := range zones {
		if z.Name == p.domain {
			return z.ID, nil
		}
	}
	return 0, fmt.Errorf("luadns: zone %q not found", p.domain)
}

func (p *luadnsProvider) findRecord(ctx context.Context, client *http.Client, zoneID int, recordType string) (luadnsRecord, error) {
	path := fmt.Sprintf("/v1/zones/%d/records", zoneID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.luadnsURL(path), nil)
	if err != nil {
		return luadnsRecord{}, fmt.Errorf("creating http request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return luadnsRecord{}, fmt.Errorf("doing http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return luadnsRecord{}, fmt.Errorf("luadns list records: HTTP %d: %s", resp.StatusCode, string(b))
	}

	var records []luadnsRecord
	if err := json.NewDecoder(resp.Body).Decode(&records); err != nil {
		return luadnsRecord{}, fmt.Errorf("decoding luadns records: %w", err)
	}

	fqdn := p.BuildDomainName() + "."
	for _, r := range records {
		if r.Type == recordType && r.Name == fqdn {
			return r, nil
		}
	}
	return luadnsRecord{}, fmt.Errorf("luadns: %s record for %s not found in zone %d", recordType, fqdn, zoneID)
}

func (p *luadnsProvider) updateLuaDNSRecord(ctx context.Context, client *http.Client, zoneID int, record luadnsRecord) error {
	path := fmt.Sprintf("/v1/zones/%d/records/%d", zoneID, record.ID)
	payload, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encoding luadns record: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, p.luadnsURL(path), bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("creating http request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("doing http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("luadns update record: HTTP %d: %s", resp.StatusCode, string(b))
	}
	return nil
}
