package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"

	"ddns-agent/internal/provider/constants"
)

type gandiProvider struct {
	domain string
	owner  string

	personalAccessToken string
}

func newGandi(data json.RawMessage, domain, owner string, ipVersion constants.IPVersion) (*gandiProvider, error) {
	var s struct {
		PersonalAccessToken string `json:"personal_access_token"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("decoding gandi settings: %w", err)
	}
	if s.PersonalAccessToken == "" {
		return nil, fmt.Errorf("gandi: personal_access_token is required")
	}
	return &gandiProvider{
		domain: domain,
		owner:  owner,

		personalAccessToken: s.PersonalAccessToken,
	}, nil
}

func (p *gandiProvider) Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error) {
	recordType := constants.A
	if ip.Is6() {
		recordType = constants.AAAA
	}

	owner := p.owner
	if owner == "" {
		owner = "@"
	}

	url := fmt.Sprintf("https://api.gandi.net/v5/livedns/domains/%s/records/%s/%s",
		p.domain, owner, recordType)

	payload, _ := json.Marshal(map[string]interface{}{
		"rrset_values": []string{ip.String()},
		"rrset_ttl":    300,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(payload))
	if err != nil {
		return netip.Addr{}, fmt.Errorf("creating http request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.personalAccessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("doing http request: %w", err)
	}
	defer resp.Body.Close()
	if err := responseStatus(resp); err != nil {
		return netip.Addr{}, err
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		b, _ := readResponse(resp.Body)
		return netip.Addr{}, fmt.Errorf("gandi update record: HTTP %d: %s", resp.StatusCode, string(b))
	}
	return ip, nil
}
