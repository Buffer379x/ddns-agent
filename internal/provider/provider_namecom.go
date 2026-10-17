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
	domain string
	owner  string

	username string
	token    string
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
		domain: domain,
		owner:  owner,

		username: extra.Username,
		token:    extra.Token,
	}, nil
}

func (p *namecomProvider) getRecordID(ctx context.Context, client *http.Client, recordType string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("https://api.name.com/v4/domains/%s/records?perPage=100", p.domain), nil)
	if err != nil {
		return 0, err
	}
	req.SetBasicAuth(p.username, p.token)
	type record struct {
		ID   int    `json:"id"`
		Host string `json:"host"`
		Type string `json:"type"`
	}
	records, err := listPaged[record](ctx, client, req, "records")
	if err != nil {
		return 0, err
	}
	for _, r := range records {
		if r.Type == recordType && rootName(r.Host) == rootName(p.owner) {
			return r.ID, nil
		}
	}
	return 0, errRecordNotFound
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
	if err := responseStatus(resp); err != nil {
		return netip.Addr{}, err
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return netip.Addr{}, fmt.Errorf("name.com update: status %d", resp.StatusCode)
	}

	return ip, nil
}
