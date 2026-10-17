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

type desecProvider struct {
	domain string
	owner  string

	token string
}

func newDeSEC(data json.RawMessage, domain, owner string, ipVersion constants.IPVersion) (Provider, error) {
	var extra struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(data, &extra); err != nil {
		return nil, err
	}
	if extra.Token == "" {
		return nil, fmt.Errorf("desec: token is required")
	}
	return &desecProvider{
		domain: domain,
		owner:  owner,

		token: extra.Token,
	}, nil
}

func (p *desecProvider) Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error) {
	recordType := constants.A
	if ip.Is6() {
		recordType = constants.AAAA
	}

	subname := p.owner
	if subname == "@" {
		subname = ""
	}

	u := url.URL{
		Scheme: "https",
		Host:   "desec.io",
		Path:   fmt.Sprintf("/api/v1/domains/%s/rrsets/%s/%s/", p.domain, subname, recordType),
	}

	payload := struct {
		Records []string `json:"records"`
		TTL     int      `json:"ttl"`
	}{
		Records: []string{ip.String()},
		TTL:     300,
	}

	buf := new(bytes.Buffer)
	if err := json.NewEncoder(buf).Encode(payload); err != nil {
		return netip.Addr{}, fmt.Errorf("encoding request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, u.String(), buf)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Token "+p.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return netip.Addr{}, err
	}
	defer resp.Body.Close()
	if err := responseStatus(resp); err != nil {
		return netip.Addr{}, err
	}

	switch resp.StatusCode {
	case http.StatusOK:
		var body struct {
			Records []string `json:"records"`
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&body); err != nil {
			return netip.Addr{}, fmt.Errorf("decoding response: %w", err)
		}
		if len(body.Records) > 0 {
			newIP, err := netip.ParseAddr(body.Records[0])
			if err == nil {
				return newIP, nil
			}
		}
		return ip, nil
	case http.StatusCreated, http.StatusNoContent:
		return ip, nil
	default:
		return netip.Addr{}, fmt.Errorf("desec: status %d", resp.StatusCode)
	}
}
