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

type ovhProvider struct {
	domain string
	owner  string

	username string
	password string
}

func newOVH(data json.RawMessage, domain, owner string, ipVersion constants.IPVersion) (Provider, error) {
	var extra struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(data, &extra); err != nil {
		return nil, err
	}
	if extra.Username == "" || extra.Password == "" {
		return nil, fmt.Errorf("ovh: username and password are required")
	}
	return &ovhProvider{
		domain: domain,
		owner:  owner,

		username: extra.Username,
		password: extra.Password,
	}, nil
}

func (p *ovhProvider) Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error) {
	u := url.URL{
		Scheme: "https",
		Host:   "www.ovh.com",
		Path:   "/nic/update",
	}
	q := url.Values{}
	q.Set("system", "dyndns")
	q.Set("hostname", domainName(p.domain, p.owner))
	q.Set("myip", ip.String())
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("creating request: %w", err)
	}
	req.SetBasicAuth(p.username, p.password)

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

	return parseDynDNSResponse(s, ip, resp.StatusCode, "ovh")
}
