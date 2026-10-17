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

type heProvider struct {
	domain string
	owner  string

	password string
}

func newHE(data json.RawMessage, domain, owner string, ipVersion constants.IPVersion) (*heProvider, error) {
	var s struct {
		Password string `json:"password"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("decoding he settings: %w", err)
	}
	if s.Password == "" {
		return nil, fmt.Errorf("he: password is required")
	}
	return &heProvider{
		domain: domain,
		owner:  owner,

		password: s.Password,
	}, nil
}

func (p *heProvider) Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error) {
	fqdn := domainName(p.domain, p.owner)

	form := url.Values{}
	form.Set("hostname", fqdn)
	form.Set("password", p.password)
	form.Set("myip", ip.String())

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://dyn.dns.he.net/nic/update", strings.NewReader(form.Encode()))
	if err != nil {
		return netip.Addr{}, fmt.Errorf("creating http request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

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
	if strings.HasPrefix(s, "good ") || s == "good" || strings.HasPrefix(s, "nochg ") || s == "nochg" {
		return ip, nil
	}
	if s == "badauth" {
		return netip.Addr{}, fmt.Errorf("he: authentication failed")
	}
	return netip.Addr{}, fmt.Errorf("he update failed: %s", s)
}
