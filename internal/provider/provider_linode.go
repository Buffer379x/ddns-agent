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

type linodeProvider struct {
	domain    string
	owner     string
	ipVersion constants.IPVersion
	token     string
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
		domain:    domain,
		owner:     owner,
		ipVersion: ipVersion,
		token:     s.Token,
	}, nil
}

func (p *linodeProvider) String() string                  { return string(constants.Linode) }
func (p *linodeProvider) Domain() string                  { return p.domain }
func (p *linodeProvider) Owner() string                   { return p.owner }
func (p *linodeProvider) IPVersion() constants.IPVersion   { return p.ipVersion }
func (p *linodeProvider) Proxied() bool                   { return false }
func (p *linodeProvider) BuildDomainName() string {
	if p.owner == "@" || p.owner == "" {
		return p.domain
	}
	return p.owner + "." + p.domain
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
	if err != nil {
		if err := p.createLinodeRecord(ctx, client, domainID, recordType, ip); err != nil {
			return netip.Addr{}, fmt.Errorf("creating linode record: %w", err)
		}
		return ip, nil
	}

	if err := p.updateLinodeRecord(ctx, client, domainID, recordID, ip); err != nil {
		return netip.Addr{}, fmt.Errorf("updating linode record: %w", err)
	}
	return ip, nil
}

func (p *linodeProvider) getDomainID(ctx context.Context, client *http.Client) (int, error) {
	req, err := p.linodeRequest(ctx, http.MethodGet, "https://api.linode.com/v4/domains", nil)
	if err != nil {
		return 0, fmt.Errorf("creating http request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("doing http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("linode list domains: HTTP %d: %s", resp.StatusCode, string(b))
	}

	var result struct {
		Data []struct {
			ID     int    `json:"id"`
			Domain string `json:"domain"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("decoding linode response: %w", err)
	}

	for _, d := range result.Data {
		if d.Domain == p.domain {
			return d.ID, nil
		}
	}
	return 0, fmt.Errorf("linode: domain %q not found", p.domain)
}

func (p *linodeProvider) getRecordID(ctx context.Context, client *http.Client, domainID int, recordType string) (int, error) {
	url := fmt.Sprintf("https://api.linode.com/v4/domains/%d/records", domainID)
	req, err := p.linodeRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, fmt.Errorf("creating http request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("doing http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("linode list records: HTTP %d: %s", resp.StatusCode, string(b))
	}

	var result struct {
		Data []struct {
			ID   int    `json:"id"`
			Type string `json:"type"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("decoding linode response: %w", err)
	}

	for _, r := range result.Data {
		if r.Type == recordType && r.Name == p.owner {
			return r.ID, nil
		}
	}
	return 0, fmt.Errorf("linode: record not found")
}

func (p *linodeProvider) createLinodeRecord(ctx context.Context, client *http.Client, domainID int, recordType string, ip netip.Addr) error {
	url := fmt.Sprintf("https://api.linode.com/v4/domains/%d/records", domainID)
	payload, _ := json.Marshal(map[string]interface{}{
		"type":   recordType,
		"name":   p.owner,
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
		b, _ := io.ReadAll(resp.Body)
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
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("linode update record: HTTP %d: %s", resp.StatusCode, string(b))
	}
	return nil
}
