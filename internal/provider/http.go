package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

var errRecordNotFound = errors.New("record not found")

func readResponse(body io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(body, (2<<20)+1))
	if len(b) > 2<<20 {
		return nil, errors.New("provider response exceeds 2 MiB")
	}
	return b, err
}
func responseStatus(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	b, _ := readResponse(resp.Body)
	return fmt.Errorf("HTTP %d: %.512s", resp.StatusCode, strings.TrimSpace(string(b)))
}
func decodeResponse(resp *http.Response, out any) error {
	if err := responseStatus(resp); err != nil {
		return err
	}
	body, err := readResponse(resp.Body)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(body, out)
}
func domainName(domain, owner string) string {
	if owner == "" || owner == "@" {
		return domain
	}
	return owner + "." + domain
}

func rootName(name string) string {
	if name == "@" {
		return ""
	}
	return name
}
func responseWord(body, word string) bool {
	parts := strings.Fields(body)
	return len(parts) > 0 && parts[0] == word
}
