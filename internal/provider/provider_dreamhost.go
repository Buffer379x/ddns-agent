package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"

	"ddns-agent/internal/provider/constants"
)

type dreamhostProvider struct {
	domain    string
	owner     string
	ipVersion constants.IPVersion
	key       string
}

func newDreamhost(data json.RawMessage, domain, owner string, ipVersion constants.IPVersion) (Provider, error) {
	var extra struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(data, &extra); err != nil {
		return nil, err
	}
	if extra.Key == "" {
		return nil, fmt.Errorf("dreamhost: key is required")
	}
	return &dreamhostProvider{
		domain:    domain,
		owner:     owner,
		ipVersion: ipVersion,
		key:       extra.Key,
	}, nil
}

func (p *dreamhostProvider) String() string                    { return string(constants.Dreamhost) }
func (p *dreamhostProvider) Domain() string                    { return p.domain }
func (p *dreamhostProvider) Owner() string                     { return p.owner }
func (p *dreamhostProvider) IPVersion() constants.IPVersion    { return p.ipVersion }
func (p *dreamhostProvider) Proxied() bool                     { return false }

func (p *dreamhostProvider) BuildDomainName() string {
	if p.owner == "@" || p.owner == "" {
		return p.domain
	}
	return p.owner + "." + p.domain
}

type dreamhostAPIResponse struct {
	Result string `json:"result"`
	Data   string `json:"data"`
}

type dreamhostListResponse struct {
	Result string `json:"result"`
	Data   []struct {
		Editable string `json:"editable"`
		Type     string `json:"type"`
		Record   string `json:"record"`
		Value    string `json:"value"`
	} `json:"data"`
}

func (p *dreamhostProvider) apiCall(ctx context.Context, client *http.Client, cmd string, extra url.Values) (*dreamhostAPIResponse, error) {
	u := url.URL{
		Scheme: "https",
		Host:   "api.dreamhost.com",
	}
	q := url.Values{}
	q.Set("key", p.key)
	q.Set("cmd", cmd)
	q.Set("format", "json")
	for k, vs := range extra {
		for _, v := range vs {
			q.Set(k, v)
		}
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("dreamhost: status %d", resp.StatusCode)
	}

	var result dreamhostAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	if result.Result != "success" {
		return nil, fmt.Errorf("dreamhost %s: %s", cmd, result.Data)
	}
	return &result, nil
}

func (p *dreamhostProvider) listRecords(ctx context.Context, client *http.Client) (*dreamhostListResponse, error) {
	u := url.URL{
		Scheme: "https",
		Host:   "api.dreamhost.com",
	}
	q := url.Values{}
	q.Set("key", p.key)
	q.Set("cmd", "dns-list_records")
	q.Set("format", "json")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("dreamhost list: status %d", resp.StatusCode)
	}

	var result dreamhostListResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	if result.Result != "success" {
		return nil, fmt.Errorf("dreamhost list: %s", result.Result)
	}
	return &result, nil
}

func (p *dreamhostProvider) Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error) {
	recordType := constants.A
	if ip.Is6() {
		recordType = constants.AAAA
	}
	fqdn := p.BuildDomainName()

	records, err := p.listRecords(ctx, client)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("listing records: %w", err)
	}

	var oldIP netip.Addr
	for _, rec := range records.Data {
		if rec.Type == recordType && rec.Record == fqdn {
			parsed, parseErr := netip.ParseAddr(rec.Value)
			if parseErr == nil && parsed.Compare(ip) == 0 {
				return ip, nil
			}
			if parseErr == nil {
				oldIP = parsed
			}
			break
		}
	}

	// Add the new record first
	addParams := url.Values{}
	addParams.Set("record", fqdn)
	addParams.Set("type", recordType)
	addParams.Set("value", ip.String())
	if _, err := p.apiCall(ctx, client, "dns-add_record", addParams); err != nil {
		return netip.Addr{}, fmt.Errorf("adding record: %w", err)
	}

	// Remove the old record if it existed
	if oldIP.IsValid() {
		removeParams := url.Values{}
		removeParams.Set("record", fqdn)
		removeParams.Set("type", recordType)
		removeParams.Set("value", oldIP.String())
		if _, err := p.apiCall(ctx, client, "dns-remove_record", removeParams); err != nil {
			return netip.Addr{}, fmt.Errorf("removing old record: %w", err)
		}
	}

	return ip, nil
}
