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
	domain string
	owner  string

	token string
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
		domain: domain,
		owner:  owner,

		token: s.Token,
	}, nil
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
	req, err := p.vultrRequest(ctx, "GET", fmt.Sprintf("https://api.vultr.com/v2/domains/%s/records?per_page=100", p.domain), nil)
	if err != nil {
		return "", err
	}
	type record struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Type string `json:"type"`
	}
	records, err := listPaged[record](ctx, client, req, "records")
	if err != nil {
		return "", err
	}
	for _, r := range records {
		if r.Type == recordType && rootName(r.Name) == rootName(p.owner) {
			return r.ID, nil
		}
	}
	return "", nil
}

func (p *vultrProvider) createRecord(ctx context.Context, client *http.Client, recordType string, ip netip.Addr) error {
	url := fmt.Sprintf("https://api.vultr.com/v2/domains/%s/records", p.domain)
	payload, _ := json.Marshal(map[string]interface{}{
		"name": rootName(p.owner),
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
		b, _ := readResponse(resp.Body)
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
		b, _ := readResponse(resp.Body)
		return fmt.Errorf("vultr update record: HTTP %d: %s", resp.StatusCode, string(b))
	}
	return nil
}
