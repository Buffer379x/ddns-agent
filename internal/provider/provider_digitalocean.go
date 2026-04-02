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

type digitaloceanProvider struct {
	domain    string
	owner     string
	ipVersion constants.IPVersion
	token     string
}

func newDigitalOcean(data json.RawMessage, domain, owner string, ipVersion constants.IPVersion) (Provider, error) {
	var extra struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(data, &extra); err != nil {
		return nil, err
	}
	if extra.Token == "" {
		return nil, fmt.Errorf("digitalocean: token is required")
	}
	return &digitaloceanProvider{
		domain:    domain,
		owner:     owner,
		ipVersion: ipVersion,
		token:     extra.Token,
	}, nil
}

func (p *digitaloceanProvider) String() string                  { return string(constants.DigitalOcean) }
func (p *digitaloceanProvider) Domain() string                  { return p.domain }
func (p *digitaloceanProvider) Owner() string                   { return p.owner }
func (p *digitaloceanProvider) IPVersion() constants.IPVersion   { return p.ipVersion }
func (p *digitaloceanProvider) Proxied() bool                   { return false }

func (p *digitaloceanProvider) BuildDomainName() string {
	if p.owner == "@" || p.owner == "" {
		return p.domain
	}
	return p.owner + "." + p.domain
}

func (p *digitaloceanProvider) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
}

func (p *digitaloceanProvider) getRecordID(ctx context.Context, client *http.Client, ip netip.Addr) (int64, bool, error) {
	recordType := constants.A
	if ip.Is6() {
		recordType = constants.AAAA
	}

	name := p.owner
	if name == "" {
		name = "@"
	}

	u := url.URL{
		Scheme: "https",
		Host:   "api.digitalocean.com",
		Path:   fmt.Sprintf("/v2/domains/%s/records", p.domain),
	}
	q := url.Values{}
	q.Set("type", recordType)
	q.Set("name", name)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return 0, false, fmt.Errorf("creating request: %w", err)
	}
	p.setHeaders(req)

	resp, err := client.Do(req)
	if err != nil {
		return 0, false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, false, fmt.Errorf("digitalocean list records: status %d", resp.StatusCode)
	}

	var body struct {
		DomainRecords []struct {
			ID   int64  `json:"id"`
			Data string `json:"data"`
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"domain_records"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return 0, false, fmt.Errorf("decoding response: %w", err)
	}

	for _, rec := range body.DomainRecords {
		if rec.Type == recordType && rec.Name == name {
			if rec.Data == ip.String() {
				return rec.ID, true, nil
			}
			return rec.ID, false, nil
		}
	}

	return 0, false, nil
}

func (p *digitaloceanProvider) Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error) {
	recordID, upToDate, err := p.getRecordID(ctx, client, ip)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("getting record: %w", err)
	}
	if upToDate {
		return ip, nil
	}

	recordType := constants.A
	if ip.Is6() {
		recordType = constants.AAAA
	}

	name := p.owner
	if name == "" {
		name = "@"
	}

	if recordID == 0 {
		u := url.URL{
			Scheme: "https",
			Host:   "api.digitalocean.com",
			Path:   fmt.Sprintf("/v2/domains/%s/records", p.domain),
		}
		payload := struct {
			Type string `json:"type"`
			Name string `json:"name"`
			Data string `json:"data"`
			TTL  int    `json:"ttl"`
		}{
			Type: recordType,
			Name: name,
			Data: ip.String(),
			TTL:  300,
		}
		buf := new(bytes.Buffer)
		if err := json.NewEncoder(buf).Encode(payload); err != nil {
			return netip.Addr{}, fmt.Errorf("encoding request: %w", err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), buf)
		if err != nil {
			return netip.Addr{}, fmt.Errorf("creating request: %w", err)
		}
		p.setHeaders(req)

		resp, err := client.Do(req)
		if err != nil {
			return netip.Addr{}, err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusCreated {
			return netip.Addr{}, fmt.Errorf("digitalocean create record: status %d", resp.StatusCode)
		}
		return ip, nil
	}

	u := url.URL{
		Scheme: "https",
		Host:   "api.digitalocean.com",
		Path:   fmt.Sprintf("/v2/domains/%s/records/%d", p.domain, recordID),
	}

	payload := struct {
		Data string `json:"data"`
	}{
		Data: ip.String(),
	}

	buf := new(bytes.Buffer)
	if err := json.NewEncoder(buf).Encode(payload); err != nil {
		return netip.Addr{}, fmt.Errorf("encoding request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u.String(), buf)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("creating request: %w", err)
	}
	p.setHeaders(req)

	resp, err := client.Do(req)
	if err != nil {
		return netip.Addr{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return netip.Addr{}, fmt.Errorf("digitalocean update record: status %d", resp.StatusCode)
	}

	var respBody struct {
		DomainRecord struct {
			Data string `json:"data"`
		} `json:"domain_record"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&respBody); err != nil {
		return netip.Addr{}, fmt.Errorf("decoding response: %w", err)
	}

	newIP, err := netip.ParseAddr(respBody.DomainRecord.Data)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("parsing returned IP: %w", err)
	}
	return newIP, nil
}
