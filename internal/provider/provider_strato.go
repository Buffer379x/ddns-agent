package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
)

type stratoProvider struct {
	domain   string
	owner    string
	password string
}

func newStrato(data json.RawMessage, domain, owner string) (Provider, error) {
	var extra struct {
		Password string `json:"password"`
	}
	if err := json.Unmarshal(data, &extra); err != nil {
		return nil, err
	}
	if extra.Password == "" {
		return nil, fmt.Errorf("strato: password is required")
	}
	return &stratoProvider{
		domain:   domain,
		owner:    owner,
		password: extra.Password,
	}, nil
}

func (p *stratoProvider) Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error) {
	u := url.URL{
		Scheme: "https",
		Host:   "dyndns.strato.com",
		Path:   "/nic/update",
	}
	q := url.Values{}
	q.Set("hostname", domainName(p.domain, p.owner))
	q.Set("myip", ip.String())
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("creating request: %w", err)
	}
	req.SetBasicAuth(domainName(p.domain, p.owner), p.password)

	resp, err := client.Do(req)
	if err != nil {
		return netip.Addr{}, err
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

	return parseDynDNSResponse(s, ip, resp.StatusCode, "strato")
}
