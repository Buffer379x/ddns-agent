package provider

import (
	"fmt"
	"net/http"
	"net/netip"
	"strings"
)

func parseDynDNSResponse(s string, ip netip.Addr, statusCode int, providerName string) (netip.Addr, error) {
	switch {
	case s == "":
		return netip.Addr{}, fmt.Errorf("%s: empty response", providerName)
	case s == "911":
		return netip.Addr{}, fmt.Errorf("%s: server-side error (911)", providerName)
	case s == "abuse":
		return netip.Addr{}, fmt.Errorf("%s: account blocked for abuse", providerName)
	case s == "badagent":
		return netip.Addr{}, fmt.Errorf("%s: bad user agent", providerName)
	case s == "badauth":
		return netip.Addr{}, fmt.Errorf("%s: authentication failed", providerName)
	case s == "nohost":
		return netip.Addr{}, fmt.Errorf("%s: hostname does not exist", providerName)
	case s == "notfqdn":
		return netip.Addr{}, fmt.Errorf("%s: hostname is not a FQDN", providerName)
	case s == "numhost":
		return netip.Addr{}, fmt.Errorf("%s: too many hosts", providerName)
	case s == "!donator":
		return netip.Addr{}, fmt.Errorf("%s: feature not available", providerName)
	}

	if statusCode != http.StatusOK {
		return netip.Addr{}, fmt.Errorf("%s: status %d: %s", providerName, statusCode, s)
	}

	if !strings.HasPrefix(s, "good") && !strings.HasPrefix(s, "nochg") {
		return netip.Addr{}, fmt.Errorf("%s: unexpected response: %q", providerName, s)
	}

	parts := strings.Fields(s)
	if len(parts) >= 2 {
		newIP, err := netip.ParseAddr(parts[1])
		if err == nil {
			return newIP, nil
		}
	}

	return ip, nil
}
