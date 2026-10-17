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

type njallaProvider struct {
	domain string
	owner  string

	key string
}

func newNjalla(data json.RawMessage, domain, owner string, ipVersion constants.IPVersion) (*njallaProvider, error) {
	var s struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("decoding njalla settings: %w", err)
	}
	if s.Key == "" {
		return nil, fmt.Errorf("njalla: key is required")
	}
	return &njallaProvider{
		domain: domain,
		owner:  owner,

		key: s.Key,
	}, nil
}

func (p *njallaProvider) Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error) {
	q := url.Values{"h": {domainName(p.domain, p.owner)}, "k": {p.key}, "quiet": {""}}
	key := "a"
	if ip.Is6() {
		key = "aaaa"
	}
	q.Set(key, ip.String())
	req, err := http.NewRequestWithContext(ctx, "GET", "https://njal.la/update/?"+q.Encode(), nil)
	if err != nil {
		return netip.Addr{}, err
	}
	res, err := client.Do(req)
	if err != nil {
		return netip.Addr{}, err
	}
	defer res.Body.Close()
	if err = responseStatus(res); err != nil {
		return netip.Addr{}, err
	}
	body, err := readResponse(res.Body)
	if err != nil {
		return netip.Addr{}, err
	}
	if strings.TrimSpace(string(body)) != "" {
		return netip.Addr{}, fmt.Errorf("njalla: unexpected response: %.512s", body)
	}
	return ip, nil
}
