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

type spdynProvider struct {
	domain string
	owner  string

	username string
	password string
	token    string
}

func newSpdyn(data json.RawMessage, domain, owner string, ipVersion constants.IPVersion) (Provider, error) {
	var extra struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Token    string `json:"token"`
	}
	if err := json.Unmarshal(data, &extra); err != nil {
		return nil, err
	}
	if extra.Token == "" && (extra.Username == "" || extra.Password == "") {
		return nil, fmt.Errorf("spdyn: either token or username+password must be provided")
	}
	return &spdynProvider{
		domain: domain,
		owner:  owner,

		username: extra.Username,
		password: extra.Password,
		token:    extra.Token,
	}, nil
}

func (p *spdynProvider) Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error) {
	u := url.URL{
		Scheme: "https",
		Host:   "update.spdyn.de",
		Path:   "/nic/update",
	}

	hostname := domainName(p.domain, p.owner)
	q := url.Values{}
	q.Set("hostname", hostname)
	q.Set("myip", ip.String())
	if p.token != "" {
		q.Set("user", hostname)
		q.Set("pass", p.token)
	} else {
		q.Set("user", p.username)
		q.Set("pass", p.password)
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
		return netip.Addr{}, fmt.Errorf("spdyn: status %d: %s", resp.StatusCode, s)
	}

	switch {
	case responseWord(s, "good"), responseWord(s, "nochg"):
		return ip, nil
	case s == "badauth" || s == "!yours":
		return netip.Addr{}, fmt.Errorf("spdyn: authentication failed")
	case s == "abuse" || s == "numhost":
		return netip.Addr{}, fmt.Errorf("spdyn: account blocked for abuse")
	case s == "notfqdn":
		return netip.Addr{}, fmt.Errorf("spdyn: not a valid FQDN")
	case s == "nohost" || s == "fatal":
		return netip.Addr{}, fmt.Errorf("spdyn: hostname not found")
	default:
		return netip.Addr{}, fmt.Errorf("spdyn: unexpected response: %s", s)
	}
}
