package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"

	"ddns-agent/internal/provider/constants"
)

type dondominioProv struct {
	domain string
	owner  string

	username string
	password string
	name     string
}

func newDonDominio(data json.RawMessage, domain, owner string, ipVersion constants.IPVersion) (Provider, error) {
	var extra struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Name     string `json:"name"`
	}
	if err := json.Unmarshal(data, &extra); err != nil {
		return nil, err
	}
	if extra.Username == "" {
		return nil, fmt.Errorf("dondominio: username is required")
	}
	if extra.Password == "" {
		return nil, fmt.Errorf("dondominio: password is required")
	}
	return &dondominioProv{
		domain: domain,
		owner:  owner,

		username: extra.Username,
		password: extra.Password,
		name:     extra.Name,
	}, nil
}

func (p *dondominioProv) Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error) {
	u := url.URL{
		Scheme: "https",
		Host:   "dondns.dondominio.com",
		Path:   "/json/",
	}

	form := url.Values{}
	form.Set("user", p.username)
	form.Set("password", p.password)
	form.Set("host", domainName(p.domain, p.owner))
	if ip.Is6() {
		form.Set("ip6", ip.String())
	} else {
		form.Set("ip", ip.String())
	}
	if p.name != "" {
		form.Set("name", p.name)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), strings.NewReader(form.Encode()))
	if err != nil {
		return netip.Addr{}, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		return netip.Addr{}, err
	}
	defer resp.Body.Close()
	if err := responseStatus(resp); err != nil {
		return netip.Addr{}, err
	}

	if resp.StatusCode != http.StatusOK {
		return netip.Addr{}, fmt.Errorf("dondominio: status %d", resp.StatusCode)
	}

	var body struct {
		Success  bool   `json:"success"`
		ErrorMsg string `json:"errorMsg"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&body); err != nil {
		return netip.Addr{}, fmt.Errorf("decoding response: %w", err)
	}
	if !body.Success {
		return netip.Addr{}, fmt.Errorf("dondominio: %s", body.ErrorMsg)
	}

	return ip, nil
}
