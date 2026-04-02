package provider

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"time"

	"ddns-agent/internal/provider/constants"
	"github.com/golang-jwt/jwt/v5"
)

type gcpProvider struct {
	domain      string
	owner       string
	ipVersion   constants.IPVersion
	project     string
	zone        string
	credentials json.RawMessage
}

func newGCP(data json.RawMessage, domain, owner string, ipVersion constants.IPVersion) (Provider, error) {
	var extra struct {
		Project     string          `json:"project"`
		Zone        string          `json:"zone"`
		Credentials json.RawMessage `json:"credentials"`
	}
	if err := json.Unmarshal(data, &extra); err != nil {
		return nil, err
	}
	if extra.Project == "" {
		return nil, fmt.Errorf("gcp: project is required")
	}
	if extra.Zone == "" {
		return nil, fmt.Errorf("gcp: zone is required")
	}
	if len(extra.Credentials) == 0 {
		return nil, fmt.Errorf("gcp: credentials JSON is required")
	}
	return &gcpProvider{
		domain:      domain,
		owner:       owner,
		ipVersion:   ipVersion,
		project:     extra.Project,
		zone:        extra.Zone,
		credentials: extra.Credentials,
	}, nil
}

func (p *gcpProvider) String() string                    { return string(constants.GCP) }
func (p *gcpProvider) Domain() string                    { return p.domain }
func (p *gcpProvider) Owner() string                     { return p.owner }
func (p *gcpProvider) IPVersion() constants.IPVersion     { return p.ipVersion }
func (p *gcpProvider) Proxied() bool                     { return false }

func (p *gcpProvider) BuildDomainName() string {
	if p.owner == "@" || p.owner == "" {
		return p.domain
	}
	return p.owner + "." + p.domain
}

func (p *gcpProvider) getAccessToken(ctx context.Context, client *http.Client) (string, error) {
	var sa struct {
		ClientEmail string `json:"client_email"`
		PrivateKey  string `json:"private_key"`
		TokenURI    string `json:"token_uri"`
	}
	if err := json.Unmarshal(p.credentials, &sa); err != nil {
		return "", fmt.Errorf("parsing service account: %w", err)
	}
	if sa.TokenURI == "" {
		sa.TokenURI = "https://oauth2.googleapis.com/token"
	}

	block, _ := pem.Decode([]byte(sa.PrivateKey))
	if block == nil {
		return "", fmt.Errorf("gcp: failed to decode PEM private key")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("parsing private key: %w", err)
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return "", fmt.Errorf("gcp: private key is not RSA")
	}

	now := time.Now()
	claims := jwt.MapClaims{
		"iss":   sa.ClientEmail,
		"scope": "https://www.googleapis.com/auth/ndev.clouddns.readwrite",
		"aud":   sa.TokenURI,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := token.SignedString(rsaKey)
	if err != nil {
		return "", fmt.Errorf("signing JWT: %w", err)
	}

	form := url.Values{}
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:jwt-bearer")
	form.Set("assertion", signed)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sa.TokenURI,
		bytes.NewBufferString(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("creating token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("requesting access token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("gcp token exchange: status %d: %s", resp.StatusCode, string(b))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", fmt.Errorf("decoding token response: %w", err)
	}
	return tokenResp.AccessToken, nil
}

type gcpResourceRecordSet struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	TTL     int      `json:"ttl"`
	Rrdatas []string `json:"rrdatas,omitempty"`
}

func (p *gcpProvider) Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error) {
	accessToken, err := p.getAccessToken(ctx, client)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("getting access token: %w", err)
	}

	recordType := constants.A
	if ip.Is6() {
		recordType = constants.AAAA
	}
	fqdn := p.BuildDomainName() + "."

	existingRRSet, err := p.gcpGetRRSet(ctx, client, accessToken, fqdn, recordType)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("getting existing record: %w", err)
	}

	newRRSet := gcpResourceRecordSet{
		Name:    fqdn,
		Type:    recordType,
		TTL:     300,
		Rrdatas: []string{ip.String()},
	}

	changeBody := struct {
		Additions []gcpResourceRecordSet `json:"additions"`
		Deletions []gcpResourceRecordSet `json:"deletions,omitempty"`
	}{
		Additions: []gcpResourceRecordSet{newRRSet},
	}
	if existingRRSet != nil {
		changeBody.Deletions = []gcpResourceRecordSet{*existingRRSet}
	}

	body, err := json.Marshal(changeBody)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("encoding change request: %w", err)
	}

	u := fmt.Sprintf("https://dns.googleapis.com/dns/v1/projects/%s/managedZones/%s/changes",
		p.project, p.zone)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return netip.Addr{}, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return netip.Addr{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return netip.Addr{}, fmt.Errorf("gcp change: status %d: %s", resp.StatusCode, string(b))
	}

	return ip, nil
}

func (p *gcpProvider) gcpGetRRSet(ctx context.Context, client *http.Client, accessToken, fqdn, recordType string) (*gcpResourceRecordSet, error) {
	u := fmt.Sprintf("https://dns.googleapis.com/dns/v1/projects/%s/managedZones/%s/rrsets/%s/%s",
		p.project, p.zone, fqdn, recordType)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("gcp get rrset: status %d: %s", resp.StatusCode, string(b))
	}

	var rrSet gcpResourceRecordSet
	if err := json.NewDecoder(resp.Body).Decode(&rrSet); err != nil {
		return nil, fmt.Errorf("decoding rrset: %w", err)
	}
	return &rrSet, nil
}
