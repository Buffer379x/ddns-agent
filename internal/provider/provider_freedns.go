package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"strings"

	"ddns-agent/internal/provider/constants"
)

type freednsProvider struct {
	domain string
	owner  string

	token string
}

func newFreeDNS(data json.RawMessage, domain, owner string, ipVersion constants.IPVersion) (*freednsProvider, error) {
	var s struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("decoding freedns settings: %w", err)
	}
	if s.Token == "" {
		return nil, fmt.Errorf("freedns: token is required")
	}
	return &freednsProvider{
		domain: domain,
		owner:  owner,

		token: s.Token,
	}, nil
}

func (p *freednsProvider) Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error) {
	u := url.URL{
		Scheme:   "https",
		Host:     "freedns.afraid.org",
		Path:     "/dynamic/update.php",
		RawQuery: p.token + "&address=" + ip.String(),
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("creating http request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("doing http request: %w", err)
	}
	defer resp.Body.Close()
	if err := responseStatus(resp); err != nil {
		return netip.Addr{}, err
	}

	b, err := readResponse(resp.Body)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("reading response: %w", err)
	}

	s := strings.TrimSpace(string(b))
	if strings.Contains(s, "Updated") || strings.Contains(s, "has not changed") {
		return ip, nil
	}
	return netip.Addr{}, fmt.Errorf("freedns update failed: %s", s)
}
