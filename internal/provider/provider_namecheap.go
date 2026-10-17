package provider

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
)

type namecheapProvider struct {
	domain   string
	owner    string
	password string
}

func newNamecheap(data json.RawMessage, domain, owner string) (Provider, error) {
	var extra struct {
		Password string `json:"password"`
	}
	if err := json.Unmarshal(data, &extra); err != nil {
		return nil, err
	}
	if extra.Password == "" {
		return nil, fmt.Errorf("namecheap: password is required")
	}
	return &namecheapProvider{
		domain:   domain,
		owner:    owner,
		password: extra.Password,
	}, nil
}

func (p *namecheapProvider) Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error) {
	if ip.Is6() {
		return netip.Addr{}, fmt.Errorf("namecheap: IPv6 is not supported")
	}

	host := p.owner
	if host == "" {
		host = "@"
	}

	u := url.URL{
		Scheme: "https",
		Host:   "dynamicdns.park-your-domain.com",
		Path:   "/update",
	}
	q := url.Values{}
	q.Set("host", host)
	q.Set("domain", p.domain)
	q.Set("password", p.password)
	q.Set("ip", ip.String())
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
	if err := responseStatus(resp); err != nil {
		return netip.Addr{}, err
	}

	b, err := readResponse(resp.Body)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("reading response: %w", err)
	}

	var xmlResp struct {
		XMLName  xml.Name `xml:"interface-response"`
		ErrCount int      `xml:"ErrCount"`
		Err1     string   `xml:"errors>Err1"`
		IP       string   `xml:"IP"`
	}
	if err := xml.Unmarshal(b, &xmlResp); err != nil {
		return netip.Addr{}, fmt.Errorf("parsing XML response: %w", err)
	}

	if xmlResp.ErrCount > 0 {
		return netip.Addr{}, fmt.Errorf("namecheap: %s", xmlResp.Err1)
	}

	newIP, err := netip.ParseAddr(xmlResp.IP)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("parsing returned IP %q: %w", xmlResp.IP, err)
	}
	return newIP, nil
}
