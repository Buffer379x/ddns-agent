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

type dnspodProvider struct {
	domain    string
	owner     string
	ipVersion constants.IPVersion
	token     string
}

func newDNSPod(data json.RawMessage, domain, owner string, ipVersion constants.IPVersion) (*dnspodProvider, error) {
	var s struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("decoding dnspod settings: %w", err)
	}
	if s.Token == "" {
		return nil, fmt.Errorf("dnspod: token is required")
	}
	return &dnspodProvider{
		domain:    domain,
		owner:     owner,
		ipVersion: ipVersion,
		token:     s.Token,
	}, nil
}

func (p *dnspodProvider) String() string                  { return string(constants.DNSPod) }
func (p *dnspodProvider) Domain() string                  { return p.domain }
func (p *dnspodProvider) Owner() string                   { return p.owner }
func (p *dnspodProvider) IPVersion() constants.IPVersion   { return p.ipVersion }
func (p *dnspodProvider) Proxied() bool                   { return false }
func (p *dnspodProvider) BuildDomainName() string {
	if p.owner == "@" || p.owner == "" {
		return p.domain
	}
	return p.owner + "." + p.domain
}

func (p *dnspodProvider) Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error) {
	recordType := constants.A
	if ip.Is6() {
		recordType = constants.AAAA
	}

	recordID, err := p.getRecordID(ctx, client, recordType)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("getting dnspod record: %w", err)
	}

	if err := p.modifyRecord(ctx, client, recordID, recordType, ip); err != nil {
		return netip.Addr{}, fmt.Errorf("modifying dnspod record: %w", err)
	}
	return ip, nil
}

func (p *dnspodProvider) dnspodPost(ctx context.Context, client *http.Client, apiURL string, form url.Values) (json.RawMessage, error) {
	form.Set("login_token", p.token)
	form.Set("format", "json")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("creating http request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "ddns-agent/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("doing http request: %w", err)
	}
	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	var result struct {
		Status struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"status"`
	}
	if err := json.Unmarshal(b, &result); err != nil {
		return nil, fmt.Errorf("decoding dnspod response: %w", err)
	}
	if result.Status.Code != "1" {
		return nil, fmt.Errorf("dnspod error: %s: %s", result.Status.Code, result.Status.Message)
	}
	return b, nil
}

func (p *dnspodProvider) getRecordID(ctx context.Context, client *http.Client, recordType string) (string, error) {
	form := url.Values{}
	form.Set("domain", p.domain)
	form.Set("sub_domain", p.owner)
	form.Set("record_type", recordType)

	b, err := p.dnspodPost(ctx, client, "https://dnsapi.cn/Record.List", form)
	if err != nil {
		return "", err
	}

	var result struct {
		Records []struct {
			ID string `json:"id"`
		} `json:"records"`
	}
	if err := json.Unmarshal(b, &result); err != nil {
		return "", fmt.Errorf("decoding dnspod records: %w", err)
	}
	if len(result.Records) == 0 {
		return "", fmt.Errorf("dnspod: no %s record found for %s", recordType, p.owner)
	}
	return result.Records[0].ID, nil
}

func (p *dnspodProvider) modifyRecord(ctx context.Context, client *http.Client, recordID, recordType string, ip netip.Addr) error {
	form := url.Values{}
	form.Set("domain", p.domain)
	form.Set("record_id", recordID)
	form.Set("sub_domain", p.owner)
	form.Set("record_type", recordType)
	form.Set("record_line", "默认")
	form.Set("value", ip.String())

	_, err := p.dnspodPost(ctx, client, "https://dnsapi.cn/Record.Modify", form)
	return err
}
