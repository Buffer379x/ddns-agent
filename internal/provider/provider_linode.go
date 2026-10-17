package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"

	"ddns-agent/internal/provider/constants"
)

type linodeProvider struct {
	domain string
	owner  string

	token string
}

func newLinode(data json.RawMessage, domain, owner string, ipVersion constants.IPVersion) (*linodeProvider, error) {
	var s struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("decoding linode settings: %w", err)
	}
	if s.Token == "" {
		return nil, fmt.Errorf("linode: token is required")
	}
	return &linodeProvider{
		domain: domain,
		owner:  owner,

		token: s.Token,
	}, nil
}

func (p *linodeProvider) linodeRequest(ctx context.Context, method, url string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func (p *linodeProvider) Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error) {
	domainID, err := p.getDomainID(ctx, client)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("getting linode domain id: %w", err)
	}

	recordType := constants.A
	if ip.Is6() {
		recordType = constants.AAAA
	}

	recordID, err := p.getRecordID(ctx, client, domainID, recordType)
	if errors.Is(err, errRecordNotFound) {
		if err := p.createLinodeRecord(ctx, client, domainID, recordType, ip); err != nil {
			return netip.Addr{}, fmt.Errorf("creating linode record: %w", err)
		}
		return ip, nil
	}

	if err != nil {
		return netip.Addr{}, err
	}
	if err := p.updateLinodeRecord(ctx, client, domainID, recordID, ip); err != nil {
		return netip.Addr{}, fmt.Errorf("updating linode record: %w", err)
	}
	return ip, nil
}

func (p *linodeProvider) getDomainID(ctx context.Context, client *http.Client) (int, error) {
	req, err := p.linodeRequest(ctx, "GET", "https://api.linode.com/v4/domains?page_size=100", nil)
	if err != nil {
		return 0, err
	}
	type domain struct {
		ID     int    `json:"id"`
		Domain string `json:"domain"`
	}
	domains, err := listPaged[domain](ctx, client, req, "data")
	if err != nil {
		return 0, err
	}
	for _, d := range domains {
		if d.Domain == p.domain {
			return d.ID, nil
		}
	}
	return 0, fmt.Errorf("linode: domain %s not found", p.domain)
}

func (p *linodeProvider) getRecordID(ctx context.Context, client *http.Client, domainID int, recordType string) (int, error) {
	req, err := p.linodeRequest(ctx, "GET", fmt.Sprintf("https://api.linode.com/v4/domains/%d/records?page_size=100", domainID), nil)
	if err != nil {
		return 0, err
	}
	type record struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
		Type string `json:"type"`
	}
	records, err := listPaged[record](ctx, client, req, "data")
	if err != nil {
		return 0, err
	}
	name := p.owner
	if name == "@" {
		name = ""
	}
	for _, r := range records {
		if r.Type == recordType && r.Name == name {
			return r.ID, nil
		}
	}
	return 0, errRecordNotFound
}

func (p *linodeProvider) createLinodeRecord(ctx context.Context, client *http.Client, domainID int, recordType string, ip netip.Addr) error {
	url := fmt.Sprintf("https://api.linode.com/v4/domains/%d/records", domainID)
	payload, _ := json.Marshal(map[string]interface{}{
		"type":   recordType,
		"name":   rootName(p.owner),
		"target": ip.String(),
	})
	req, err := p.linodeRequest(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("creating http request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("doing http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := readResponse(resp.Body)
		return fmt.Errorf("linode create record: HTTP %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

func (p *linodeProvider) updateLinodeRecord(ctx context.Context, client *http.Client, domainID, recordID int, ip netip.Addr) error {
	url := fmt.Sprintf("https://api.linode.com/v4/domains/%d/records/%d", domainID, recordID)
	payload, _ := json.Marshal(map[string]string{
		"target": ip.String(),
	})
	req, err := p.linodeRequest(ctx, http.MethodPut, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("creating http request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("doing http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := readResponse(resp.Body)
		return fmt.Errorf("linode update record: HTTP %d: %s", resp.StatusCode, string(b))
	}
	return nil
}
