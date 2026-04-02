package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"

	"ddns-agent/internal/provider/constants"
)

type njallaProvider struct {
	domain    string
	owner     string
	ipVersion constants.IPVersion
	key       string
}

func newNjalla(data json.RawMessage, domain, owner string, ipVersion constants.IPVersion) (*njallaProvider, error) {
	var s struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("decoding njalla settings: %w", err)
	}
	if s.Key == "" {
		return nil, fmt.Errorf("njalla: key is required")
	}
	return &njallaProvider{
		domain:    domain,
		owner:     owner,
		ipVersion: ipVersion,
		key:       s.Key,
	}, nil
}

func (p *njallaProvider) String() string                  { return string(constants.Njalla) }
func (p *njallaProvider) Domain() string                  { return p.domain }
func (p *njallaProvider) Owner() string                   { return p.owner }
func (p *njallaProvider) IPVersion() constants.IPVersion   { return p.ipVersion }
func (p *njallaProvider) Proxied() bool                   { return false }
func (p *njallaProvider) BuildDomainName() string {
	if p.owner == "@" || p.owner == "" {
		return p.domain
	}
	return p.owner + "." + p.domain
}

func (p *njallaProvider) Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error) {
	recordType := constants.A
	if ip.Is6() {
		recordType = constants.AAAA
	}

	name := p.owner
	if name == "" {
		name = "@"
	}

	reqBody := map[string]interface{}{
		"method": "set_record",
		"params": map[string]interface{}{
			"domain":  p.domain,
			"name":    name,
			"type":    recordType,
			"content": ip.String(),
			"token":   p.key,
		},
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("encoding njalla request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://njal.la/api/1/", bytes.NewReader(payload))
	if err != nil {
		return netip.Addr{}, fmt.Errorf("creating http request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("doing http request: %w", err)
	}
	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("reading response: %w", err)
	}

	var result struct {
		Result struct {
			Content string `json:"content"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(b, &result); err != nil {
		return netip.Addr{}, fmt.Errorf("decoding njalla response: %w", err)
	}

	if result.Error != nil {
		return netip.Addr{}, fmt.Errorf("njalla error %d: %s", result.Error.Code, result.Error.Message)
	}
	return ip, nil
}
