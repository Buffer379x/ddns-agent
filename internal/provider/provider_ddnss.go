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

type ddnssProvider struct {
	domain string
	owner  string

	username string
	password string
}

func newDDNSS(data json.RawMessage, domain, owner string, ipVersion constants.IPVersion) (Provider, error) {
	var extra struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(data, &extra); err != nil {
		return nil, err
	}
	if extra.Username == "" {
		return nil, fmt.Errorf("ddnss: username is required")
	}
	if extra.Password == "" {
		return nil, fmt.Errorf("ddnss: password is required")
	}
	return &ddnssProvider{
		domain: domain,
		owner:  owner,

		username: extra.Username,
		password: extra.Password,
	}, nil
}

func (p *ddnssProvider) Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error) {
	u := url.URL{
		Scheme: "https",
		Host:   "www.ddnss.de",
		Path:   "/upd.php",
	}
	q := url.Values{}
	q.Set("user", p.username)
	q.Set("pwd", p.password)
	q.Set("host", domainName(p.domain, p.owner))
	if ip.Is6() {
		q.Set("ip6", ip.String())
	} else {
		q.Set("ip", ip.String())
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("creating request: %w", err)
	}

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

	if resp.StatusCode != http.StatusOK {
		return netip.Addr{}, fmt.Errorf("ddnss: status %d: %s", resp.StatusCode, s)
	}

	if strings.Contains(s, "Updated") || strings.HasPrefix(s, "good ") || s == "good" || strings.HasPrefix(s, "nochg ") || s == "nochg" {
		return ip, nil
	}
	if strings.Contains(s, "badauth") {
		return netip.Addr{}, fmt.Errorf("ddnss: bad authentication")
	}
	return netip.Addr{}, fmt.Errorf("ddnss: unexpected response: %q", s)
}
