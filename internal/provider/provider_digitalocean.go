package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"

	"ddns-agent/internal/provider/constants"
)

type digitaloceanProvider struct {
	domain string
	owner  string

	token string
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
		domain: domain,
		owner:  owner,

		token: extra.Token,
	}, nil
}

func (p *digitaloceanProvider) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
}

func (p *digitaloceanProvider) getRecordID(ctx context.Context, client *http.Client, ip netip.Addr) (int64, bool, error) {
	typ := "A"
	if ip.Is6() {
		typ = "AAAA"
	}
	name := p.owner
	if name == "" {
		name = "@"
	}
	u := "https://api.digitalocean.com/v2/domains/" + p.domain + "/records?per_page=100&type=" + typ
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return 0, false, err
	}
	p.setHeaders(req)
	type record struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
		Type string `json:"type"`
		Data string `json:"data"`
	}
	records, err := listPaged[record](ctx, client, req, "domain_records")
	if err != nil {
		return 0, false, err
	}
	for _, r := range records {
		if r.Type == typ && r.Name == name {
			return r.ID, r.Data == ip.String(), nil
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
		if err := responseStatus(resp); err != nil {
			return netip.Addr{}, err
		}

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
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&respBody); err != nil {
		return netip.Addr{}, fmt.Errorf("decoding response: %w", err)
	}

	newIP, err := netip.ParseAddr(respBody.DomainRecord.Data)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("parsing returned IP: %w", err)
	}
	return newIP, nil
}
