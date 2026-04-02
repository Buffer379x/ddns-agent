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

type hetznerProvider struct {
	domain         string
	owner          string
	ipVersion      constants.IPVersion
	token          string
	zoneIdentifier string
}

func newHetzner(data json.RawMessage, domain, owner string, ipVersion constants.IPVersion) (Provider, error) {
	var extra struct {
		Token          string `json:"token"`
		ZoneIdentifier string `json:"zone_identifier"`
	}
	if err := json.Unmarshal(data, &extra); err != nil {
		return nil, err
	}
	if extra.Token == "" {
		return nil, fmt.Errorf("hetzner: token is required")
	}
	return &hetznerProvider{
		domain:         domain,
		owner:          owner,
		ipVersion:      ipVersion,
		token:          extra.Token,
		zoneIdentifier: extra.ZoneIdentifier,
	}, nil
}

func (p *hetznerProvider) String() string                  { return string(constants.Hetzner) }
func (p *hetznerProvider) Domain() string                  { return p.domain }
func (p *hetznerProvider) Owner() string                   { return p.owner }
func (p *hetznerProvider) IPVersion() constants.IPVersion   { return p.ipVersion }
func (p *hetznerProvider) Proxied() bool                   { return false }

func (p *hetznerProvider) BuildDomainName() string {
	if p.owner == "@" || p.owner == "" {
		return p.domain
	}
	return p.owner + "." + p.domain
}

func (p *hetznerProvider) setHeaders(req *http.Request) {
	req.Header.Set("Auth-API-Token", p.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
}

func (p *hetznerProvider) getZoneID(ctx context.Context, client *http.Client) (string, error) {
	if p.zoneIdentifier != "" {
		return p.zoneIdentifier, nil
	}

	u := url.URL{
		Scheme: "https",
		Host:   "dns.hetzner.com",
		Path:   "/api/v1/zones",
	}
	q := url.Values{}
	q.Set("name", p.domain)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	p.setHeaders(req)

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("hetzner list zones: status %d", resp.StatusCode)
	}

	var body struct {
		Zones []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"zones"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("decoding response: %w", err)
	}

	for _, z := range body.Zones {
		if z.Name == p.domain {
			p.zoneIdentifier = z.ID
			return z.ID, nil
		}
	}
	return "", fmt.Errorf("hetzner: zone not found for domain %s", p.domain)
}

func (p *hetznerProvider) getRecordID(ctx context.Context, client *http.Client, zoneID string, ip netip.Addr) (string, bool, error) {
	recordType := constants.A
	if ip.Is6() {
		recordType = constants.AAAA
	}

	u := url.URL{
		Scheme: "https",
		Host:   "dns.hetzner.com",
		Path:   "/api/v1/records",
	}
	q := url.Values{}
	q.Set("zone_id", zoneID)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", false, fmt.Errorf("creating request: %w", err)
	}
	p.setHeaders(req)

	resp, err := client.Do(req)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", false, fmt.Errorf("hetzner list records: status %d", resp.StatusCode)
	}

	name := p.owner
	if name == "" {
		name = "@"
	}

	var body struct {
		Records []struct {
			ID    string `json:"id"`
			Type  string `json:"type"`
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"records"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", false, fmt.Errorf("decoding response: %w", err)
	}

	for _, rec := range body.Records {
		if rec.Type == recordType && rec.Name == name {
			if rec.Value == ip.String() {
				return rec.ID, true, nil
			}
			return rec.ID, false, nil
		}
	}

	return "", false, nil
}

func (p *hetznerProvider) Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error) {
	zoneID, err := p.getZoneID(ctx, client)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("getting zone: %w", err)
	}

	recordID, upToDate, err := p.getRecordID(ctx, client, zoneID, ip)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("getting record: %w", err)
	}
	if upToDate {
		return ip, nil
	}

	recordType := constants.A
	if ip.Is6() {
		recordType = constants.AAAA
	}

	name := p.owner
	if name == "" {
		name = "@"
	}

	if recordID == "" {
		u := url.URL{
			Scheme: "https",
			Host:   "dns.hetzner.com",
			Path:   "/api/v1/records",
		}
		payload := struct {
			Type   string `json:"type"`
			Name   string `json:"name"`
			Value  string `json:"value"`
			ZoneID string `json:"zone_id"`
			TTL    int    `json:"ttl"`
		}{
			Type:   recordType,
			Name:   name,
			Value:  ip.String(),
			ZoneID: zoneID,
			TTL:    300,
		}
		buf := new(bytes.Buffer)
		if err := json.NewEncoder(buf).Encode(payload); err != nil {
			return netip.Addr{}, fmt.Errorf("encoding request: %w", err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), buf)
		if err != nil {
			return netip.Addr{}, fmt.Errorf("creating request: %w", err)
		}
		p.setHeaders(req)

		resp, err := client.Do(req)
		if err != nil {
			return netip.Addr{}, err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
			return netip.Addr{}, fmt.Errorf("hetzner create record: status %d", resp.StatusCode)
		}
		return ip, nil
	}

	u := url.URL{
		Scheme: "https",
		Host:   "dns.hetzner.com",
		Path:   "/api/v1/records/" + recordID,
	}

	payload := struct {
		Type   string `json:"type"`
		Name   string `json:"name"`
		Value  string `json:"value"`
		ZoneID string `json:"zone_id"`
		TTL    int    `json:"ttl"`
	}{
		Type:   recordType,
		Name:   name,
		Value:  ip.String(),
		ZoneID: zoneID,
		TTL:    300,
	}

	buf := new(bytes.Buffer)
	if err := json.NewEncoder(buf).Encode(payload); err != nil {
		return netip.Addr{}, fmt.Errorf("encoding request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u.String(), buf)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("creating request: %w", err)
	}
	p.setHeaders(req)

	resp, err := client.Do(req)
	if err != nil {
		return netip.Addr{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return netip.Addr{}, fmt.Errorf("hetzner update record: status %d", resp.StatusCode)
	}

	var respBody struct {
		Record struct {
			Value string `json:"value"`
		} `json:"record"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&respBody); err != nil {
		return netip.Addr{}, fmt.Errorf("decoding response: %w", err)
	}

	newIP, err := netip.ParseAddr(respBody.Record.Value)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("parsing returned IP: %w", err)
	}
	if newIP.Compare(ip) != 0 {
		return netip.Addr{}, fmt.Errorf("hetzner: sent %s but received %s", ip, newIP)
	}
	return newIP, nil
}
