package provider

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"ddns-agent/internal/provider/constants"
)

type aliyunProvider struct {
	domain       string
	owner        string
	ipVersion    constants.IPVersion
	accessKeyID  string
	accessSecret string
}

func newAliyun(data json.RawMessage, domain, owner string, ipVersion constants.IPVersion) (Provider, error) {
	var extra struct {
		AccessKeyID string `json:"access_key_id"`
		AccessSecret string `json:"access_secret"`
	}
	if err := json.Unmarshal(data, &extra); err != nil {
		return nil, err
	}
	if extra.AccessKeyID == "" {
		return nil, fmt.Errorf("aliyun: access_key_id is required")
	}
	if extra.AccessSecret == "" {
		return nil, fmt.Errorf("aliyun: access_secret is required")
	}
	return &aliyunProvider{
		domain:       domain,
		owner:        owner,
		ipVersion:    ipVersion,
		accessKeyID:  extra.AccessKeyID,
		accessSecret: extra.AccessSecret,
	}, nil
}

func (p *aliyunProvider) String() string                    { return string(constants.Aliyun) }
func (p *aliyunProvider) Domain() string                    { return p.domain }
func (p *aliyunProvider) Owner() string                     { return p.owner }
func (p *aliyunProvider) IPVersion() constants.IPVersion    { return p.ipVersion }
func (p *aliyunProvider) Proxied() bool                     { return false }

func (p *aliyunProvider) BuildDomainName() string {
	if p.owner == "@" || p.owner == "" {
		return p.domain
	}
	return p.owner + "." + p.domain
}

func (p *aliyunProvider) newURLValues() url.Values {
	randBytes := make([]byte, 8)
	_, _ = rand.Read(randBytes)
	nonce := int64(binary.BigEndian.Uint64(randBytes))

	values := make(url.Values)
	values.Set("AccessKeyId", p.accessKeyID)
	values.Set("Format", "JSON")
	values.Set("Version", "2015-01-09")
	values.Set("SignatureMethod", "HMAC-SHA1")
	values.Set("Timestamp", time.Now().UTC().Format("2006-01-02T15:04:05Z"))
	values.Set("SignatureVersion", "1.0")
	values.Set("SignatureNonce", strconv.FormatInt(nonce, 10))
	return values
}

func (p *aliyunProvider) sign(method string, values url.Values) {
	sortedParams := make(sort.StringSlice, 0, len(values))
	for key, vals := range values {
		s := url.QueryEscape(key) + "=" + url.QueryEscape(vals[0])
		sortedParams = append(sortedParams, s)
	}
	sortedParams.Sort()

	stringToSign := strings.ToUpper(method) + "&%2F&" +
		url.QueryEscape(strings.Join(sortedParams, "&"))

	key := []byte(p.accessSecret + "&")
	mac := hmac.New(sha1.New, key)
	_, _ = mac.Write([]byte(stringToSign))
	signature := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	values.Set("Signature", signature)
}

func (p *aliyunProvider) getRecordID(ctx context.Context, client *http.Client, recordType string) (string, error) {
	values := p.newURLValues()
	values.Set("Action", "DescribeDomainRecords")
	values.Set("DomainName", p.domain)
	values.Set("RRKeyWord", p.owner)
	values.Set("Type", recordType)

	p.sign(http.MethodGet, values)

	u := url.URL{
		Scheme:   "https",
		Host:     "dns.aliyuncs.com",
		RawQuery: values.Encode(),
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("aliyun describe records: status %d", resp.StatusCode)
	}

	var body struct {
		DomainRecords struct {
			Record []struct {
				RecordID string `json:"RecordId"`
			} `json:"Record"`
		} `json:"DomainRecords"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("decoding response: %w", err)
	}
	if len(body.DomainRecords.Record) == 0 {
		return "", fmt.Errorf("aliyun: no %s record found for %s", recordType, p.owner)
	}
	return body.DomainRecords.Record[0].RecordID, nil
}

func (p *aliyunProvider) Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error) {
	recordType := constants.A
	if ip.Is6() {
		recordType = constants.AAAA
	}

	recordID, err := p.getRecordID(ctx, client, recordType)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("getting record: %w", err)
	}

	values := p.newURLValues()
	values.Set("Action", "UpdateDomainRecord")
	values.Set("RecordId", recordID)
	values.Set("RR", p.owner)
	values.Set("Type", recordType)
	values.Set("Value", ip.String())

	p.sign(http.MethodGet, values)

	u := url.URL{
		Scheme:   "https",
		Host:     "alidns.aliyuncs.com",
		RawQuery: values.Encode(),
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("creating request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return netip.Addr{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return netip.Addr{}, fmt.Errorf("aliyun update: status %d", resp.StatusCode)
	}

	return ip, nil
}
