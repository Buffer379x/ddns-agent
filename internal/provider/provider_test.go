package provider

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"ddns-agent/internal/provider/constants"
)

type mockTransport func(*http.Request) (*http.Response, error)

func (f mockTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}
}
func TestEveryProviderRejectsHTTPFailures(t *testing.T) {
	for _, info := range AllProviderInfos() {
		t.Run(string(info.Name), func(t *testing.T) {
			cfg := map[string]any{}
			for _, f := range info.Fields {
				switch f.Type {
				case "checkbox":
					cfg[f.Name] = false
				case "number":
					cfg[f.Name] = 300
				default:
					cfg[f.Name] = "test"
				}
			}
			cfg["url"] = "https://example.net/update?ip={ip}"
			if info.Name == constants.GCP {
				key, err := rsa.GenerateKey(rand.Reader, 2048)
				if err != nil {
					t.Fatal(err)
				}
				der, _ := x509.MarshalPKCS8PrivateKey(key)
				cfg["credentials"] = map[string]any{"client_email": "x@example.net", "private_key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), "token_uri": "https://oauth2.googleapis.com/token"}
			}
			raw, _ := json.Marshal(cfg)
			p, err := New(info.Name, raw, "example.net", "home", constants.IPv4)
			if err != nil {
				t.Fatal(err)
			}
			client := &http.Client{Timeout: time.Second, Transport: mockTransport(func(r *http.Request) (*http.Response, error) { return response(500, "good 203.0.113.1"), nil })}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if _, err = p.Update(ctx, client, netip.MustParseAddr("203.0.113.1")); err == nil {
				t.Fatal("HTTP 500 accepted as success")
			}
		})
	}
}
func TestDynDNSOnlyAcceptsExactSuccessWords(t *testing.T) {
	ip := netip.MustParseAddr("203.0.113.1")
	for _, body := range []string{"not good", "goodbye", "nochg-error", "<html>good</html>"} {
		if _, err := parseDynDNSResponse(body, ip, 200, "test"); err == nil {
			t.Fatalf("accepted %q", body)
		}
	}
	for _, body := range []string{"good 203.0.113.1", "nochg 203.0.113.1", "good"} {
		if _, err := parseDynDNSResponse(body, ip, 200, "test"); err != nil {
			t.Fatal(err)
		}
	}
}
func TestLinodeNeverCreatesAfterListFailure(t *testing.T) {
	p, _ := newLinode(json.RawMessage(`{"token":"private"}`), "example.net", "home", constants.IPv4)
	client := &http.Client{Transport: mockTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" {
			t.Fatal("write after list failure")
		}
		if r.URL.Path == "/v4/domains" {
			return response(200, `{"data":[{"id":42,"domain":"example.net"}],"pages":1}`), nil
		}
		return response(429, `{"errors":[{"reason":"rate limit"}]}`), nil
	})}
	if _, err := p.Update(context.Background(), client, netip.MustParseAddr("203.0.113.1")); err == nil {
		t.Fatal("rate limit ignored")
	}
}
func TestLinodeFindsRecordOnLaterPages(t *testing.T) {
	p, _ := newLinode(json.RawMessage(`{"token":"private"}`), "example.net", "@", constants.IPv4)
	var written bool
	client := &http.Client{Transport: mockTransport(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/v4/domains":
			if r.URL.Query().Get("page") == "2" {
				return response(200, `{"data":[{"id":42,"domain":"example.net"}],"pages":2}`), nil
			}
			return response(200, `{"data":[],"pages":2}`), nil
		case "/v4/domains/42/records":
			if r.URL.Query().Get("page") == "2" {
				return response(200, `{"data":[{"id":99,"name":"","type":"A"}],"pages":2}`), nil
			}
			return response(200, `{"data":[],"pages":2}`), nil
		case "/v4/domains/42/records/99":
			if r.Method != "PUT" {
				t.Fatal(r.Method)
			}
			written = true
			return response(200, `{}`), nil
		}
		t.Fatal(r.URL)
		return nil, nil
	})}
	if _, err := p.Update(context.Background(), client, netip.MustParseAddr("203.0.113.1")); err != nil {
		t.Fatal(err)
	}
	if !written {
		t.Fatal("did not update existing record")
	}
}
func TestPaginationCannotForwardCredentialsToAnotherHost(t *testing.T) {
	req, _ := http.NewRequest("GET", "https://api.example.net/records", nil)
	calls := 0
	client := &http.Client{Transport: mockTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return response(200, `{"records":[],"links":{"pages":{"next":"https://evil.example/records"}}}`), nil
	})}
	if _, err := listPaged[map[string]any](context.Background(), client, req, "records"); err == nil || calls != 1 {
		t.Fatal("unsafe pagination followed")
	}
}
func TestHetznerCloudRRSetAndActionContract(t *testing.T) {
	p, _ := newHetzner(json.RawMessage(`{"token":"private","zone_identifier":"legacyopaque"}`), "example.net", "home", constants.IPv6)
	calls := 0
	client := &http.Client{Transport: mockTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host != "api.hetzner.cloud" || r.Header.Get("Authorization") != "Bearer private" {
			t.Fatal("wrong API or auth")
		}
		switch calls {
		case 1:
			if r.URL.Path != "/v1/zones/example.net/rrsets/home/AAAA" {
				t.Fatal(r.URL.Path)
			}
			return response(200, `{"rrset":{"records":[{"value":"2001:db8::old"}]}}`), nil
		case 2:
			if r.Method != "POST" || !strings.HasSuffix(r.URL.Path, "/actions/set_records") {
				t.Fatal(r.URL)
			}
			return response(201, `{"action":{"id":42,"status":"running"}}`), nil
		case 3:
			if r.URL.Path != "/v1/zones/actions/42" {
				t.Fatal(r.URL.Path)
			}
			return response(200, `{"action":{"id":42,"status":"success"}}`), nil
		}
		return nil, fmt.Errorf("unexpected call")
	})}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := p.Update(ctx, client, netip.MustParseAddr("2001:db8::1")); err != nil {
		t.Fatal(err)
	}
}
func TestIONOSUpdatesOnlyExactFamilyAndKeepsTTL(t *testing.T) {
	p, _ := newIonos(json.RawMessage(`{"api_key":"private"}`), "example.net", "home", constants.IPv6)
	var written bool
	client := &http.Client{Transport: mockTransport(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("X-API-Key") != "private" {
			t.Fatal("missing auth")
		}
		switch r.URL.Path {
		case "/dns/v1/zones":
			return response(200, `[{"id":"zone","name":"example.net"}]`), nil
		case "/dns/v1/zones/zone":
			if r.URL.Query().Get("recordName") != "home.example.net" || r.URL.Query().Get("recordType") != "AAAA" {
				t.Fatal(r.URL)
			}
			return response(200, `{"records":[{"id":"v4","name":"home.example.net","type":"A","content":"203.0.113.1"},{"id":"v6","name":"home.example.net","type":"AAAA","content":"2001:db8::2","ttl":600}]}`), nil
		case "/dns/v1/zones/zone/records/v6":
			var payload map[string]any
			json.NewDecoder(r.Body).Decode(&payload)
			if r.Method != "PUT" || payload["content"] != "2001:db8::1" || payload["ttl"] != float64(600) {
				t.Fatal(payload)
			}
			written = true
			return response(200, `{}`), nil
		}
		t.Fatal("unexpected write", r.URL)
		return nil, nil
	})}
	if _, err := p.Update(context.Background(), client, netip.MustParseAddr("2001:db8::1")); err != nil {
		t.Fatal(err)
	}
	if !written {
		t.Fatal("record not updated")
	}
}
func TestAliyunMatchesExactRecordAndRejectsAPIError(t *testing.T) {
	p, _ := newAliyun(json.RawMessage(`{"access_key_id":"test","access_secret":"secret"}`), "example.net", "home", constants.IPv4)
	client := &http.Client{Transport: mockTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("Action") == "DescribeDomainRecords" {
			return response(200, `{"DomainRecords":{"Record":[{"RecordId":"wrong","RR":"homepage","Type":"A"},{"RecordId":"right","RR":"home","Type":"A"}]}}`), nil
		}
		if r.URL.Query().Get("RecordId") != "right" {
			t.Fatal("updated wrong record")
		}
		return response(200, `{"Code":"InvalidAccessKeyId.NotFound","Message":"denied"}`), nil
	})}
	if _, err := p.Update(context.Background(), client, netip.MustParseAddr("203.0.113.1")); err == nil {
		t.Fatal("API error accepted")
	}
}
func TestNjallaUsesDynamicHostnameKey(t *testing.T) {
	p, _ := newNjalla(json.RawMessage(`{"key":"private"}`), "example.net", "home", constants.IPv4)
	client := &http.Client{Transport: mockTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" || r.URL.Path != "/update/" || r.URL.Query().Get("h") != "home.example.net" || r.URL.Query().Get("a") != "203.0.113.1" {
			t.Fatal(r.URL)
		}
		return response(200, ""), nil
	})}
	if _, err := p.Update(context.Background(), client, netip.MustParseAddr("203.0.113.1")); err != nil {
		t.Fatal(err)
	}
}

func TestHetznerNeverReplacesAMultiAddressRRSet(t *testing.T) {
	p, _ := newHetzner(json.RawMessage(`{"token":"private"}`), "example.net", "home", constants.IPv4)
	calls := 0
	client := &http.Client{Transport: mockTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "GET" {
			t.Fatal("modified an ambiguous RRSet")
		}
		return response(200, `{"rrset":{"records":[{"value":"203.0.113.1"},{"value":"203.0.113.2"}]}}`), nil
	})}
	if _, err := p.Update(context.Background(), client, netip.MustParseAddr("203.0.113.3")); err == nil || calls != 1 {
		t.Fatal("ambiguous RRSet was accepted")
	}
}

func TestVultrFollowsOpaqueCursorWithoutCreatingDuplicate(t *testing.T) {
	p, _ := newVultr(json.RawMessage(`{"token":"private"}`), "example.net", "home", constants.IPv4)
	calls := 0
	written := false
	cursor := "bmV4dF9fMTMxOTgxNQ=="
	client := &http.Client{Transport: mockTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path == "/v2/domains/example.net/records" {
			if r.Method != "GET" {
				t.Fatal("created a duplicate record")
			}
			if calls == 1 {
				return response(200, `{"records":[],"meta":{"total":101,"links":{"next":"bmV4dF9fMTMxOTgxNQ==","prev":""}}}`), nil
			}
			if r.URL.Query().Get("cursor") != cursor {
				t.Fatal("cursor was not forwarded as a query parameter")
			}
			return response(200, `{"records":[{"id":"existing","name":"home","type":"A"}],"meta":{"links":{"next":""}}}`), nil
		}
		if r.URL.Path == "/v2/domains/example.net/records/existing" && r.Method == "PATCH" {
			written = true
			return response(204, ""), nil
		}
		t.Fatal("unexpected request", r.Method, r.URL)
		return nil, nil
	})}
	if _, err := p.Update(context.Background(), client, netip.MustParseAddr("203.0.113.5")); err != nil {
		t.Fatal(err)
	}
	if !written || calls != 3 {
		t.Fatalf("record was not updated: writes=%v calls=%d", written, calls)
	}
}

func TestVultrRepeatedCursorStopsWithoutWriting(t *testing.T) {
	p, _ := newVultr(json.RawMessage(`{"token":"private"}`), "example.net", "home", constants.IPv4)
	calls := 0
	cursor := "https://other.example/records?token=opaque"
	client := &http.Client{Transport: mockTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "GET" || r.URL.Host != "api.vultr.com" || r.URL.Path != "/v2/domains/example.net/records" {
			t.Fatal("cursor changed the destination or caused a write")
		}
		if calls == 2 && r.URL.Query().Get("cursor") != cursor {
			t.Fatal("cursor was not treated as an opaque token")
		}
		return response(200, `{"records":[],"meta":{"links":{"next":"https://other.example/records?token=opaque"}}}`), nil
	})}
	if _, err := p.Update(context.Background(), client, netip.MustParseAddr("203.0.113.5")); err == nil || calls != 2 {
		t.Fatalf("repeated cursor was not stopped: calls=%d error=%v", calls, err)
	}
}
