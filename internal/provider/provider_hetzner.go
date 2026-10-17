package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"ddns-agent/internal/provider/constants"
)

type hetznerProvider struct {
	domain, owner, token, zoneIdentifier string
}

func newHetzner(raw json.RawMessage, domain, owner string, version constants.IPVersion) (Provider, error) {
	var cfg struct {
		Token string `json:"token"`
		Zone  string `json:"zone_identifier"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}
	if cfg.Token == "" {
		return nil, errors.New("hetzner Console API token is required")
	}
	return &hetznerProvider{domain: domain, owner: owner, token: cfg.Token, zoneIdentifier: cfg.Zone}, nil
}
func (p *hetznerProvider) request(ctx context.Context, client *http.Client, method, path string, payload any) (*http.Response, error) {
	var body bytes.Buffer
	if payload != nil {
		if err := json.NewEncoder(&body).Encode(payload); err != nil {
			return nil, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, "https://api.hetzner.cloud/v1"+path, &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Content-Type", "application/json")
	return client.Do(req)
}
func (p *hetznerProvider) Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error) {
	zone := p.domain
	// Legacy DNS Console IDs were opaque strings. Console accepts a numeric ID or the zone name.
	if _, err := strconv.ParseUint(p.zoneIdentifier, 10, 64); err == nil && p.zoneIdentifier != "" {
		zone = p.zoneIdentifier
	} else if strings.Contains(p.zoneIdentifier, ".") {
		zone = p.zoneIdentifier
	}
	name := p.owner
	if name == "" {
		name = "@"
	}
	typ := "A"
	if ip.Is6() {
		typ = "AAAA"
	}
	base := "/zones/" + url.PathEscape(zone) + "/rrsets"
	path := base + "/" + url.PathEscape(name) + "/" + typ
	res, err := p.request(ctx, client, "GET", path, nil)
	if err != nil {
		return netip.Addr{}, err
	}
	missing := res.StatusCode == 404
	var current struct {
		RRSet struct {
			Records []struct {
				Value string `json:"value"`
			} `json:"records"`
		} `json:"rrset"`
	}
	if !missing {
		err = decodeResponse(res, &current)
	}
	res.Body.Close()
	if err != nil {
		return netip.Addr{}, err
	}

	if len(current.RRSet.Records) > 1 {
		return netip.Addr{}, errors.New("hetzner: multiple addresses in this RRSet; use a dedicated DDNS hostname to avoid overwriting other records")
	}
	if len(current.RRSet.Records) == 1 && current.RRSet.Records[0].Value == ip.String() {
		return ip, nil
	}
	payload := map[string]any{"records": []map[string]string{{"value": ip.String()}}}
	if missing {
		payload["name"] = name
		payload["type"] = typ
		payload["ttl"] = 300
		path = base
	} else {
		path += "/actions/set_records"
	}
	res, err = p.request(ctx, client, "POST", path, payload)
	if err != nil {
		return netip.Addr{}, err
	}
	var result struct {
		Action struct {
			ID     int64  `json:"id"`
			Status string `json:"status"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		} `json:"action"`
	}
	err = decodeResponse(res, &result)
	res.Body.Close()
	if err != nil {
		return netip.Addr{}, err
	}
	for result.Action.Status != "success" {
		if result.Action.Error != nil {
			return netip.Addr{}, fmt.Errorf("hetzner: %s", result.Action.Error.Message)
		}
		if result.Action.ID == 0 || result.Action.Status == "error" {
			return netip.Addr{}, errors.New("hetzner: invalid or failed action")
		}
		select {
		case <-ctx.Done():
			return netip.Addr{}, ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
		res, err = p.request(ctx, client, "GET", fmt.Sprintf("/zones/actions/%d", result.Action.ID), nil)
		if err != nil {
			return netip.Addr{}, err
		}
		err = decodeResponse(res, &result)
		res.Body.Close()
		if err != nil {
			return netip.Addr{}, err
		}
	}
	return ip, nil
}
