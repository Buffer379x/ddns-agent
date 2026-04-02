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

type namecomProvider struct {
	domain    string
	owner     string
	ipVersion constants.IPVersion
	username  string
	token     string
}

func newNameCom(data json.RawMessage, domain, owner string, ipVersion constants.IPVersion) (Provider, error) {
	var extra struct {
		Username string `json:"username"`
		Token    string `json:"token"`
	}
	if err := json.Unmarshal(data, &extra); err != nil {
		return nil, err
	}
	if extra.Username == "" {
		return nil, fmt.Errorf("name.com: username is required")
	}
	if extra.Token == "" {
		return nil, fmt.Errorf("name.com: token is required")
	}
	return &namecomProvider{
		domain:    domain,
		owner:     owner,
		ipVersion: ipVersion,
		username:  extra.Username,
		token:     extra.Token,
	}, nil
}

func (p *namecomProvider) String() string                    { return string(constants.NameCom) }
func (p *namecomProvider) Domain() string                    { return p.domain }
func (p *namecomProvider) Owner() string                     { return p.owner }
func (p *namecomProvider) IPVersion() constants.IPVersion    { return p.ipVersion }
func (p *namecomProvider) Proxied() bool                     { return false }

func (p *namecomProvider) BuildDomainName() string {
	if p.owner == "@" || p.owner == "" {
		return p.domain
	}
	return p.owner + "." + p.domain
}

func (p *namecomProvider) getRecordID(ctx context.Context, client *http.Client, recordType string) (int, error) {
	u := url.URL{
		Scheme: "https",
		Host:   "api.name.com",
		Path:   fmt.Sprintf("/v4/domains/%s/records", p.domain),
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return 0, fmt.Errorf("creating request: %w", err)
	}
	req.SetBasicAuth(p.username, p.token)

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("name.com list records: status %d", resp.StatusCode)
	}

	var body struct {
		Records []struct {
			ID   int    `json:"id"`
			Host string `json:"host"`
			Type string `json:"type"`
		} `json:"records"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return 0, fmt.Errorf("decoding response: %w", err)
	}

	for _, rec := range body.Records {
		host := rec.Host
		if host == "" {
			host = "@"
		}
		if host == p.owner && rec.Type == recordType {
			return rec.ID, nil
		}
	}
	return 0, fmt.Errorf("name.com: no %s record found for %s", recordType, p.owner)
}

func (p *namecomProvider) Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error) {
	recordType := constants.A
	if ip.Is6() {
		recordType = constants.AAAA
	}

	recordID, err := p.getRecordID(ctx, client, recordType)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("getting record: %w", err)
	}

	u := url.URL{
		Scheme: "https",
		Host:   "api.name.com",
		Path:   fmt.Sprintf("/v4/domains/%s/records/%d", p.domain, recordID),
	}

	host := p.owner
	if host == "@" {
		host = ""
	}
	payload := struct {
		Host   string `json:"host"`
		Type   string `json:"type"`
		Answer string `json:"answer"`
	}{
		Host:   host,
		Type:   recordType,
		Answer: ip.String(),
	}

	buf := new(bytes.Buffer)
	if err := json.NewEncoder(buf).Encode(payload); err != nil {
		return netip.Addr{}, fmt.Errorf("encoding request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u.String(), buf)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("creating request: %w", err)
	}
	req.SetBasicAuth(p.username, p.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return netip.Addr{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return netip.Addr{}, fmt.Errorf("name.com update: status %d", resp.StatusCode)
	}

	return ip, nil
}
