package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"

	"ddns-agent/internal/provider/constants"
)

type servercowProvider struct {
	domain    string
	owner     string
	ipVersion constants.IPVersion
	username  string
	password  string
}

func newServercow(data json.RawMessage, domain, owner string, ipVersion constants.IPVersion) (Provider, error) {
	var extra struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(data, &extra); err != nil {
		return nil, err
	}
	if extra.Username == "" {
		return nil, fmt.Errorf("servercow: username is required")
	}
	if extra.Password == "" {
		return nil, fmt.Errorf("servercow: password is required")
	}
	return &servercowProvider{
		domain:    domain,
		owner:     owner,
		ipVersion: ipVersion,
		username:  extra.Username,
		password:  extra.Password,
	}, nil
}

func (p *servercowProvider) String() string                    { return string(constants.Servercow) }
func (p *servercowProvider) Domain() string                    { return p.domain }
func (p *servercowProvider) Owner() string                     { return p.owner }
func (p *servercowProvider) IPVersion() constants.IPVersion     { return p.ipVersion }
func (p *servercowProvider) Proxied() bool                     { return false }

func (p *servercowProvider) BuildDomainName() string {
	if p.owner == "@" || p.owner == "" {
		return p.domain
	}
	return p.owner + "." + p.domain
}

func (p *servercowProvider) Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error) {
	recordType := constants.A
	if ip.Is6() {
		recordType = constants.AAAA
	}

	u := url.URL{
		Scheme: "https",
		Host:   "api.servercow.de",
		Path:   "/dns/v1/domains/" + p.domain,
	}

	name := p.owner
	if name == "@" {
		name = ""
	}

	payload := struct {
		Type    string `json:"type"`
		Name    string `json:"name"`
		Content string `json:"content"`
		TTL     int    `json:"ttl"`
	}{
		Type:    recordType,
		Name:    name,
		Content: ip.String(),
		TTL:     300,
	}

	buf := new(bytes.Buffer)
	if err := json.NewEncoder(buf).Encode(payload); err != nil {
		return netip.Addr{}, fmt.Errorf("encoding request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), buf)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Auth-Username", p.username)
	req.Header.Set("X-Auth-Password", p.password)

	resp, err := client.Do(req)
	if err != nil {
		return netip.Addr{}, err
	}
	defer resp.Body.Close()

	var result struct {
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return netip.Addr{}, fmt.Errorf("decoding response: %w", err)
	}

	if result.Message != "ok" {
		return netip.Addr{}, fmt.Errorf("servercow: %s", result.Error)
	}

	return ip, nil
}
