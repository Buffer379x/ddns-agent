package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"

	"ddns-agent/internal/provider/constants"
)

type netcupProvider struct {
	domain         string
	owner          string
	ipVersion      constants.IPVersion
	customerNumber string
	apiKey         string
	apiPassword    string
}

func newNetcup(data json.RawMessage, domain, owner string, ipVersion constants.IPVersion) (Provider, error) {
	var extra struct {
		CustomerNumber string `json:"customer_number"`
		APIKey         string `json:"api_key"`
		APIPassword    string `json:"api_password"`
	}
	if err := json.Unmarshal(data, &extra); err != nil {
		return nil, err
	}
	if extra.CustomerNumber == "" {
		return nil, fmt.Errorf("netcup: customer_number is required")
	}
	if extra.APIKey == "" {
		return nil, fmt.Errorf("netcup: api_key is required")
	}
	if extra.APIPassword == "" {
		return nil, fmt.Errorf("netcup: api_password is required")
	}
	return &netcupProvider{
		domain:         domain,
		owner:          owner,
		ipVersion:      ipVersion,
		customerNumber: extra.CustomerNumber,
		apiKey:         extra.APIKey,
		apiPassword:    extra.APIPassword,
	}, nil
}

func (p *netcupProvider) String() string                    { return string(constants.Netcup) }
func (p *netcupProvider) Domain() string                    { return p.domain }
func (p *netcupProvider) Owner() string                     { return p.owner }
func (p *netcupProvider) IPVersion() constants.IPVersion     { return p.ipVersion }
func (p *netcupProvider) Proxied() bool                     { return false }

func (p *netcupProvider) BuildDomainName() string {
	if p.owner == "@" || p.owner == "" {
		return p.domain
	}
	return p.owner + "." + p.domain
}

const netcupEndpoint = "https://ccp.netcup.net/run/webservice/servers/endpoint.php?JSON"

type netcupDNSRecord struct {
	ID          string `json:"id"`
	Hostname    string `json:"hostname"`
	Type        string `json:"type"`
	Destination string `json:"destination"`
	Priority    string `json:"priority"`
	State       string `json:"state"`
}

type netcupDNSRecordSet struct {
	DNSRecords []netcupDNSRecord `json:"dnsrecords"`
}

func (p *netcupProvider) netcupRequest(ctx context.Context, client *http.Client, reqBody, respData any) error {
	buf := new(bytes.Buffer)
	if err := json.NewEncoder(buf).Encode(reqBody); err != nil {
		return fmt.Errorf("encoding request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, netcupEndpoint, buf)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var common struct {
		Status       string          `json:"status"`
		StatusCode   int             `json:"statuscode"`
		ShortMessage string          `json:"shortmessage"`
		ResponseData json.RawMessage `json:"responsedata"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&common); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}
	if common.Status == "error" {
		return fmt.Errorf("netcup: %s (code %d)", common.ShortMessage, common.StatusCode)
	}
	if respData != nil {
		if err := json.Unmarshal(common.ResponseData, respData); err != nil {
			return fmt.Errorf("decoding response data: %w", err)
		}
	}
	return nil
}

func (p *netcupProvider) netcupLogin(ctx context.Context, client *http.Client) (string, error) {
	reqBody := struct {
		Action string `json:"action"`
		Param  struct {
			APIKey         string `json:"apikey"`
			APIPassword    string `json:"apipassword"`
			CustomerNumber string `json:"customernumber"`
		} `json:"param"`
	}{Action: "login"}
	reqBody.Param.APIKey = p.apiKey
	reqBody.Param.APIPassword = p.apiPassword
	reqBody.Param.CustomerNumber = p.customerNumber

	var respData struct {
		Session string `json:"apisessionid"`
	}
	if err := p.netcupRequest(ctx, client, reqBody, &respData); err != nil {
		return "", fmt.Errorf("login: %w", err)
	}
	if respData.Session == "" {
		return "", fmt.Errorf("netcup: empty session returned")
	}
	return respData.Session, nil
}

func (p *netcupProvider) netcupInfoDNSRecords(ctx context.Context, client *http.Client, session string) (netcupDNSRecordSet, error) {
	reqBody := struct {
		Action string `json:"action"`
		Param  struct {
			APIKey         string `json:"apikey"`
			APISessionID   string `json:"apisessionid"`
			CustomerNumber string `json:"customernumber"`
			DomainName     string `json:"domainname"`
		} `json:"param"`
	}{Action: "infoDnsRecords"}
	reqBody.Param.APIKey = p.apiKey
	reqBody.Param.APISessionID = session
	reqBody.Param.CustomerNumber = p.customerNumber
	reqBody.Param.DomainName = p.domain

	var recordSet netcupDNSRecordSet
	err := p.netcupRequest(ctx, client, reqBody, &recordSet)
	return recordSet, err
}

func (p *netcupProvider) netcupUpdateDNSRecords(ctx context.Context, client *http.Client, session string, recordSet netcupDNSRecordSet) (netcupDNSRecordSet, error) {
	reqBody := struct {
		Action string `json:"action"`
		Param  struct {
			APIKey         string             `json:"apikey"`
			APISessionID   string             `json:"apisessionid"`
			CustomerNumber string             `json:"customernumber"`
			DomainName     string             `json:"domainname"`
			DNSRecordSet   netcupDNSRecordSet `json:"dnsrecordset"`
		} `json:"param"`
	}{Action: "updateDnsRecords"}
	reqBody.Param.APIKey = p.apiKey
	reqBody.Param.APISessionID = session
	reqBody.Param.CustomerNumber = p.customerNumber
	reqBody.Param.DomainName = p.domain
	reqBody.Param.DNSRecordSet = recordSet

	var response netcupDNSRecordSet
	err := p.netcupRequest(ctx, client, reqBody, &response)
	return response, err
}

func (p *netcupProvider) Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error) {
	session, err := p.netcupLogin(ctx, client)
	if err != nil {
		return netip.Addr{}, err
	}

	recordType := constants.A
	if ip.Is6() {
		recordType = constants.AAAA
	}

	records, err := p.netcupInfoDNSRecords(ctx, client, session)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("getting DNS records: %w", err)
	}

	var target netcupDNSRecord
	found := false
	for _, r := range records.DNSRecords {
		if r.Hostname == p.owner && r.Type == recordType {
			target = r
			target.Destination = ip.String()
			found = true
			break
		}
	}
	if !found {
		target = netcupDNSRecord{
			Hostname:    p.owner,
			Type:        recordType,
			Destination: ip.String(),
		}
	}

	updateResp, err := p.netcupUpdateDNSRecords(ctx, client, session, netcupDNSRecordSet{
		DNSRecords: []netcupDNSRecord{target},
	})
	if err != nil {
		return netip.Addr{}, fmt.Errorf("updating record: %w", err)
	}

	for _, r := range updateResp.DNSRecords {
		if r.Hostname == p.owner && r.Type == recordType {
			newIP, err := netip.ParseAddr(r.Destination)
			if err != nil {
				return netip.Addr{}, fmt.Errorf("parsing returned IP: %w", err)
			}
			if newIP.Compare(ip) != 0 {
				return netip.Addr{}, fmt.Errorf("netcup: sent %s but received %s", ip, newIP)
			}
			return newIP, nil
		}
	}

	return ip, nil
}
