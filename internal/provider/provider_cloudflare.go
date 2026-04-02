package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"strings"

	"ddns-agent/internal/provider/constants"
)

type cloudflareProvider struct {
	domain         string
	owner          string
	ipVersion      constants.IPVersion
	zoneIdentifier string
	token          string
	email          string
	key            string
	proxied        bool
	ttl            uint32
}

func newCloudflare(data json.RawMessage, domain, owner string, ipVersion constants.IPVersion) (Provider, error) {
	var extra struct {
		ZoneIdentifier string `json:"zone_identifier"`
		Token          string `json:"token"`
		Email          string `json:"email"`
		Key            string `json:"key"`
		Proxied        bool   `json:"proxied"`
		TTL            uint32 `json:"ttl"`
	}
	if err := json.Unmarshal(data, &extra); err != nil {
		return nil, err
	}
	if extra.ZoneIdentifier == "" {
		return nil, fmt.Errorf("cloudflare: zone_identifier is required")
	}
	if extra.Token == "" && (extra.Email == "" || extra.Key == "") {
		return nil, fmt.Errorf("cloudflare: either token or email+key must be provided")
	}
	ttl := extra.TTL
	if ttl == 0 {
		ttl = 1 // 1 = automatic
	}
	return &cloudflareProvider{
		domain:         domain,
		owner:          owner,
		ipVersion:      ipVersion,
		zoneIdentifier: extra.ZoneIdentifier,
		token:          extra.Token,
		email:          extra.Email,
		key:            extra.Key,
		proxied:        extra.Proxied,
		ttl:            ttl,
	}, nil
}

func (p *cloudflareProvider) String() string        { return string(constants.Cloudflare) }
func (p *cloudflareProvider) Domain() string         { return p.domain }
func (p *cloudflareProvider) Owner() string          { return p.owner }
func (p *cloudflareProvider) IPVersion() constants.IPVersion { return p.ipVersion }
func (p *cloudflareProvider) Proxied() bool          { return p.proxied }

func (p *cloudflareProvider) BuildDomainName() string {
	if p.owner == "@" || p.owner == "" {
		return p.domain
	}
	return p.owner + "." + p.domain
}

func (p *cloudflareProvider) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if p.token != "" {
		req.Header.Set("Authorization", "Bearer "+p.token)
	} else {
		req.Header.Set("X-Auth-Email", p.email)
		req.Header.Set("X-Auth-Key", p.key)
	}
}

func (p *cloudflareProvider) getRecordID(ctx context.Context, client *http.Client, ip netip.Addr) (string, bool, error) {
	recordType := constants.A
	if ip.Is6() {
		recordType = constants.AAAA
	}

	u := url.URL{
		Scheme: "https",
		Host:   "api.cloudflare.com",
		Path:   fmt.Sprintf("/client/v4/zones/%s/dns_records", p.zoneIdentifier),
	}
	q := url.Values{}
	q.Set("type", recordType)
	q.Set("name", p.BuildDomainName())
	q.Set("page", "1")
	q.Set("per_page", "1")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", false, fmt.Errorf("creating request: %w", err)
	}
	p.setHeaders(req)

	resp, err := client.Do(req)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", false, fmt.Errorf("cloudflare list records: status %d", resp.StatusCode)
	}

	var body struct {
		Success bool `json:"success"`
		Errors  []struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
		Result []struct {
			ID      string `json:"id"`
			Content string `json:"content"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", false, fmt.Errorf("decoding response: %w", err)
	}
	if !body.Success {
		msgs := make([]string, len(body.Errors))
		for i, e := range body.Errors {
			msgs[i] = e.Message
		}
		return "", false, fmt.Errorf("cloudflare: %s", strings.Join(msgs, "; "))
	}
	if len(body.Result) == 0 {
		return "", false, nil // no existing record
	}
	rec := body.Result[0]
	if rec.Content == ip.String() {
		return rec.ID, true, nil
	}
	return rec.ID, false, nil
}

func (p *cloudflareProvider) createRecord(ctx context.Context, client *http.Client, ip netip.Addr) error {
	recordType := constants.A
	if ip.Is6() {
		recordType = constants.AAAA
	}

	u := url.URL{
		Scheme: "https",
		Host:   "api.cloudflare.com",
		Path:   fmt.Sprintf("/client/v4/zones/%s/dns_records", p.zoneIdentifier),
	}

	payload := struct {
		Type    string `json:"type"`
		Name    string `json:"name"`
		Content string `json:"content"`
		Proxied bool   `json:"proxied"`
		TTL     uint32 `json:"ttl"`
	}{
		Type:    recordType,
		Name:    p.BuildDomainName(),
		Content: ip.String(),
		Proxied: p.proxied,
		TTL:     p.ttl,
	}

	buf := new(bytes.Buffer)
	if err := json.NewEncoder(buf).Encode(payload); err != nil {
		return fmt.Errorf("encoding request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), buf)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	p.setHeaders(req)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var body struct {
		Success bool `json:"success"`
		Errors  []struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}
	if !body.Success {
		msgs := make([]string, len(body.Errors))
		for i, e := range body.Errors {
			msgs[i] = e.Message
		}
		return fmt.Errorf("cloudflare create: %s", strings.Join(msgs, "; "))
	}
	return nil
}

func (p *cloudflareProvider) Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error) {
	recordType := constants.A
	if ip.Is6() {
		recordType = constants.AAAA
	}

	recordID, upToDate, err := p.getRecordID(ctx, client, ip)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("getting record: %w", err)
	}
	if upToDate {
		return ip, nil
	}

	if recordID == "" {
		if err := p.createRecord(ctx, client, ip); err != nil {
			return netip.Addr{}, fmt.Errorf("creating record: %w", err)
		}
		return ip, nil
	}

	u := url.URL{
		Scheme: "https",
		Host:   "api.cloudflare.com",
		Path:   fmt.Sprintf("/client/v4/zones/%s/dns_records/%s", p.zoneIdentifier, recordID),
	}

	payload := struct {
		Type    string `json:"type"`
		Name    string `json:"name"`
		Content string `json:"content"`
		Proxied bool   `json:"proxied"`
		TTL     uint32 `json:"ttl"`
	}{
		Type:    recordType,
		Name:    p.BuildDomainName(),
		Content: ip.String(),
		Proxied: p.proxied,
		TTL:     p.ttl,
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

	var body struct {
		Success bool `json:"success"`
		Errors  []struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
		Result struct {
			Content string `json:"content"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return netip.Addr{}, fmt.Errorf("decoding response: %w", err)
	}
	if !body.Success {
		msgs := make([]string, len(body.Errors))
		for i, e := range body.Errors {
			msgs[i] = e.Message
		}
		return netip.Addr{}, fmt.Errorf("cloudflare update: %s", strings.Join(msgs, "; "))
	}

	newIP, err := netip.ParseAddr(body.Result.Content)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("parsing returned IP: %w", err)
	}
	if newIP.Compare(ip) != 0 {
		return netip.Addr{}, fmt.Errorf("cloudflare: sent %s but received %s", ip, newIP)
	}
	return newIP, nil
}
