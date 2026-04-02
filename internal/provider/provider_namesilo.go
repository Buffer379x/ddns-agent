package provider

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"

	"ddns-agent/internal/provider/constants"
)

type namesiloProvider struct {
	domain    string
	owner     string
	ipVersion constants.IPVersion
	key       string
}

func newNameSilo(data json.RawMessage, domain, owner string) (Provider, error) {
	var extra struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(data, &extra); err != nil {
		return nil, err
	}
	if extra.Key == "" {
		return nil, fmt.Errorf("namesilo: key is required")
	}
	return &namesiloProvider{
		domain:    domain,
		owner:     owner,
		ipVersion: constants.IPv4,
		key:       extra.Key,
	}, nil
}

func (p *namesiloProvider) String() string                    { return string(constants.NameSilo) }
func (p *namesiloProvider) Domain() string                    { return p.domain }
func (p *namesiloProvider) Owner() string                     { return p.owner }
func (p *namesiloProvider) IPVersion() constants.IPVersion    { return p.ipVersion }
func (p *namesiloProvider) Proxied() bool                     { return false }

func (p *namesiloProvider) BuildDomainName() string {
	if p.owner == "@" || p.owner == "" {
		return p.domain
	}
	return p.owner + "." + p.domain
}

type namesiloXMLReply struct {
	XMLName xml.Name `xml:"namesilo"`
	Reply   struct {
		Code   int    `xml:"code"`
		Detail string `xml:"detail"`
	} `xml:"reply"`
}

type namesiloListReply struct {
	XMLName xml.Name `xml:"namesilo"`
	Reply   struct {
		Code           int    `xml:"code"`
		Detail         string `xml:"detail"`
		ResourceRecord []struct {
			RecordID string `xml:"record_id"`
			Type     string `xml:"type"`
			Host     string `xml:"host"`
			Value    string `xml:"value"`
		} `xml:"resource_record"`
	} `xml:"reply"`
}

func (p *namesiloProvider) getRecordID(ctx context.Context, client *http.Client, recordType string) (string, error) {
	u := url.URL{
		Scheme: "https",
		Host:   "www.namesilo.com",
		Path:   "/api/dnsListRecords",
	}
	q := url.Values{}
	q.Set("version", "1")
	q.Set("type", "xml")
	q.Set("key", p.key)
	q.Set("domain", p.domain)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	var reply namesiloListReply
	if err := xml.Unmarshal(b, &reply); err != nil {
		return "", fmt.Errorf("decoding xml: %w", err)
	}
	if reply.Reply.Code != 300 {
		return "", fmt.Errorf("namesilo list: code %d: %s", reply.Reply.Code, reply.Reply.Detail)
	}

	fqdn := p.BuildDomainName()
	for _, rec := range reply.Reply.ResourceRecord {
		if rec.Host == fqdn && rec.Type == recordType {
			return rec.RecordID, nil
		}
	}
	return "", fmt.Errorf("namesilo: no %s record found for %s", recordType, fqdn)
}

func (p *namesiloProvider) Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error) {
	recordType := constants.A
	if ip.Is6() {
		recordType = constants.AAAA
	}

	rrid, err := p.getRecordID(ctx, client, recordType)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("getting record: %w", err)
	}

	u := url.URL{
		Scheme: "https",
		Host:   "www.namesilo.com",
		Path:   "/api/dnsUpdateRecord",
	}
	q := url.Values{}
	q.Set("version", "1")
	q.Set("type", "xml")
	q.Set("key", p.key)
	q.Set("domain", p.domain)
	q.Set("rrid", rrid)
	rrhost := p.owner
	if rrhost == "@" {
		rrhost = ""
	}
	q.Set("rrhost", rrhost)
	q.Set("rrvalue", ip.String())
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("creating request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return netip.Addr{}, err
	}
	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("reading response: %w", err)
	}

	var reply namesiloXMLReply
	if err := xml.Unmarshal(b, &reply); err != nil {
		return netip.Addr{}, fmt.Errorf("decoding xml: %w", err)
	}
	if reply.Reply.Code != 300 {
		return netip.Addr{}, fmt.Errorf("namesilo update: code %d: %s", reply.Reply.Code, reply.Reply.Detail)
	}

	return ip, nil
}
