package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"

	"ddns-agent/internal/provider/constants"
)

type vultrProvider struct {
	domain    string
	owner     string
	ipVersion constants.IPVersion
	token     string
}

func newVultr(data json.RawMessage, domain, owner string, ipVersion constants.IPVersion) (*vultrProvider, error) {
	var s struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("decoding vultr settings: %w", err)
	}
	if s.Token == "" {
		return nil, fmt.Errorf("vultr: token is required")
	}
	return &vultrProvider{
		domain:    domain,
		owner:     owner,
		ipVersion: ipVersion,
		token:     s.Token,
	}, nil
}

func (p *vultrProvider) String() string                  { return string(constants.Vultr) }
func (p *vultrProvider) Domain() string                  { return p.domain }
func (p *vultrProvider) Owner() string                   { return p.owner }
func (p *vultrProvider) IPVersion() constants.IPVersion   { return p.ipVersion }
func (p *vultrProvider) Proxied() bool                   { return false }
func (p *vultrProvider) BuildDomainName() string {
	if p.owner == "@" || p.owner == "" {
		return p.domain
	}
	return p.owner + "." + p.domain
}

func (p *vultrProvider) Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error) {
	recordType := constants.A
	if ip.Is6() {
		recordType = constants.AAAA
	}

	recordID, err := p.getRecordID(ctx, client, recordType)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("getting vultr record: %w", err)
	}

	if recordID == "" {
		if err := p.createRecord(ctx, client, recordType, ip); err != nil {
			return netip.Addr{}, fmt.Errorf("creating vultr record: %w", err)
		}
		return ip, nil
	}

	if err := p.updateRecord(ctx, client, recordID, ip); err != nil {
		return netip.Addr{}, fmt.Errorf("updating vultr record: %w", err)
	}
	return ip, nil
}

func (p *vultrProvider) vultrRequest(ctx context.Context, method, url string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func (p *vultrProvider) getRecordID(ctx context.Context, client *http.Client, recordType string) (string, error) {
	url := fmt.Sprintf("https://api.vultr.com/v2/domains/%s/records", p.domain)
	req, err := p.vultrRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("creating http request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("doing http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("vultr list records: HTTP %d: %s", resp.StatusCode, string(b))
	}

	var result struct {
		Records []struct {
			ID   string `json:"id"`
			Type string `json:"type"`
			Name string `json:"name"`
			Data string `json:"data"`
		} `json:"records"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding vultr response: %w", err)
	}

	for _, r := range result.Records {
		if r.Type == recordType && r.Name == p.owner {
			return r.ID, nil
		}
	}
	return "", nil
}

func (p *vultrProvider) createRecord(ctx context.Context, client *http.Client, recordType string, ip netip.Addr) error {
	url := fmt.Sprintf("https://api.vultr.com/v2/domains/%s/records", p.domain)
	payload, _ := json.Marshal(map[string]interface{}{
		"name": p.owner,
		"type": recordType,
		"data": ip.String(),
		"ttl":  300,
	})
	req, err := p.vultrRequest(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("creating http request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("doing http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("vultr create record: HTTP %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

func (p *vultrProvider) updateRecord(ctx context.Context, client *http.Client, recordID string, ip netip.Addr) error {
	url := fmt.Sprintf("https://api.vultr.com/v2/domains/%s/records/%s", p.domain, recordID)
	payload, _ := json.Marshal(map[string]string{
		"data": ip.String(),
	})
	req, err := p.vultrRequest(ctx, http.MethodPatch, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("creating http request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("doing http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("vultr update record: HTTP %d: %s", resp.StatusCode, string(b))
	}
	return nil
}
