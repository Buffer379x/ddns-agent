package provider

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"ddns-agent/internal/provider/constants"
)

type route53Provider struct {
	domain    string
	owner     string
	ipVersion constants.IPVersion
	accessKey string
	secretKey string
	zoneID    string
	ttl       uint32
}

func newRoute53(data json.RawMessage, domain, owner string, ipVersion constants.IPVersion) (*route53Provider, error) {
	var s struct {
		AccessKey string  `json:"access_key"`
		SecretKey string  `json:"secret_key"`
		ZoneID    string  `json:"zone_id"`
		TTL       *uint32 `json:"ttl,omitempty"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("decoding route53 settings: %w", err)
	}
	if s.AccessKey == "" {
		return nil, fmt.Errorf("route53: access_key is required")
	}
	if s.SecretKey == "" {
		return nil, fmt.Errorf("route53: secret_key is required")
	}
	if s.ZoneID == "" {
		return nil, fmt.Errorf("route53: zone_id is required")
	}
	ttl := uint32(300)
	if s.TTL != nil {
		ttl = *s.TTL
	}
	return &route53Provider{
		domain:    domain,
		owner:     owner,
		ipVersion: ipVersion,
		accessKey: s.AccessKey,
		secretKey: s.SecretKey,
		zoneID:    s.ZoneID,
		ttl:       ttl,
	}, nil
}

func (p *route53Provider) String() string              { return string(constants.Route53) }
func (p *route53Provider) Domain() string              { return p.domain }
func (p *route53Provider) Owner() string               { return p.owner }
func (p *route53Provider) IPVersion() constants.IPVersion { return p.ipVersion }
func (p *route53Provider) Proxied() bool               { return false }
func (p *route53Provider) BuildDomainName() string {
	if p.owner == "@" || p.owner == "" {
		return p.domain
	}
	return p.owner + "." + p.domain
}

const (
	route53Host       = "route53.amazonaws.com"
	route53DateTimeFmt = "20060102T150405Z"
	route53DateFmt     = "20060102"
	route53Region      = "us-east-1"
	route53Service     = "route53"
)

// XML types for the Route53 ChangeResourceRecordSets API.
type r53ChangeRequest struct {
	XMLName     xml.Name       `xml:"ChangeResourceRecordSetsRequest"`
	XMLNS       string         `xml:"xmlns,attr"`
	ChangeBatch r53ChangeBatch `xml:"ChangeBatch"`
}

type r53ChangeBatch struct {
	Changes []r53Change `xml:"Changes>Change"`
}

type r53Change struct {
	Action            string         `xml:"Action"`
	ResourceRecordSet r53RecordSet   `xml:"ResourceRecordSet"`
}

type r53RecordSet struct {
	Name            string       `xml:"Name"`
	Type            string       `xml:"Type"`
	TTL             uint32       `xml:"TTL"`
	ResourceRecords []r53Record  `xml:"ResourceRecords>ResourceRecord"`
}

type r53Record struct {
	Value string `xml:"Value"`
}

type r53ErrorResponse struct {
	XMLName   xml.Name `xml:"ErrorResponse"`
	Error     struct {
		Type    string `xml:"Type"`
		Code    string `xml:"Code"`
		Message string `xml:"Message"`
	} `xml:"Error"`
	RequestID string `xml:"RequestId"`
}

func (p *route53Provider) Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error) {
	recordType := constants.A
	if ip.Is6() {
		recordType = constants.AAAA
	}

	body := r53ChangeRequest{
		XMLNS: "https://route53.amazonaws.com/doc/2013-04-01/",
		ChangeBatch: r53ChangeBatch{
			Changes: []r53Change{{
				Action: "UPSERT",
				ResourceRecordSet: r53RecordSet{
					Name: p.BuildDomainName(),
					Type: recordType,
					TTL:  p.ttl,
					ResourceRecords: []r53Record{{Value: ip.String()}},
				},
			}},
		},
	}

	var buf bytes.Buffer
	if err := xml.NewEncoder(&buf).Encode(body); err != nil {
		return netip.Addr{}, fmt.Errorf("encoding route53 xml: %w", err)
	}
	payload := buf.Bytes()

	urlStr := "https://" + route53Host + "/2013-04-01/hostedzone/" + p.zoneID + "/rrset"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, urlStr, bytes.NewReader(payload))
	if err != nil {
		return netip.Addr{}, fmt.Errorf("creating http request: %w", err)
	}

	now := time.Now().UTC()
	req.Header.Set("Content-Type", "application/xml")
	req.Header.Set("Host", route53Host)
	req.Header.Set("X-Amz-Date", now.Format(route53DateTimeFmt))
	req.Header.Set("Authorization", p.signV4(req.Method, req.URL.Path, payload, now))

	resp, err := client.Do(req)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("doing http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return ip, nil
	}

	var errResp r53ErrorResponse
	if xmlErr := xml.NewDecoder(resp.Body).Decode(&errResp); xmlErr != nil {
		return netip.Addr{}, fmt.Errorf("route53: HTTP %d", resp.StatusCode)
	}
	return netip.Addr{}, fmt.Errorf("route53: HTTP %d: %s/%s: %s",
		resp.StatusCode, errResp.Error.Type, errResp.Error.Code, errResp.Error.Message)
}

// signV4 produces an AWS Signature Version 4 Authorization header value.
func (p *route53Provider) signV4(method, urlPath string, payload []byte, t time.Time) string {
	dateStamp := t.Format(route53DateFmt)
	credentialScope := dateStamp + "/" + route53Region + "/" + route53Service + "/aws4_request"

	signedHeaders := "content-type;host;x-amz-date"
	canonicalHeaders := "content-type:application/xml\nhost:" + route53Host + "\nx-amz-date:" + t.Format(route53DateTimeFmt) + "\n"
	payloadHash := sha256Hex(payload)
	canonicalRequest := strings.Join([]string{
		method,
		urlPath,
		"",
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")

	stringToSign := "AWS4-HMAC-SHA256\n" +
		t.Format(route53DateTimeFmt) + "\n" +
		credentialScope + "\n" +
		sha256Hex([]byte(canonicalRequest))

	signingKey := hmacSHA256(
		hmacSHA256(
			hmacSHA256(
				hmacSHA256([]byte("AWS4"+p.secretKey), []byte(dateStamp)),
				[]byte(route53Region),
			),
			[]byte(route53Service),
		),
		[]byte("aws4_request"),
	)
	signature := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))
	return fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s,SignedHeaders=%s,Signature=%s",
		p.accessKey, credentialScope, signedHeaders, signature)
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func hmacSHA256(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}
