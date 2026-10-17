package netutil

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const MaxResponse = 2 << 20

var ErrResponseTooLarge = errors.New("remote response exceeds 2 MiB")

type limitedBody struct {
	io.ReadCloser
	remaining int64
}

func (b *limitedBody) Read(p []byte) (int, error) {
	if b.remaining == 0 {
		var extra [1]byte
		n, err := b.ReadCloser.Read(extra[:])
		if n > 0 {
			return 0, ErrResponseTooLarge
		}
		return 0, err
	}
	if int64(len(p)) > b.remaining {
		p = p[:b.remaining]
	}
	n, err := b.ReadCloser.Read(p)
	b.remaining -= int64(n)
	return n, err
}

type Transport struct{ Base http.RoundTripper }

func (t Transport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme != "http" && r.URL.Scheme != "https" {
		return nil, errors.New("only HTTP and HTTPS are supported")
	}
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	res, err := base.RoundTrip(r)
	if err != nil {
		return nil, errors.New(Redact(err.Error()))
	}
	res.Body = &limitedBody{ReadCloser: res.Body, remaining: MaxResponse}
	return res, nil
}
func NewClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: Transport{Base: http.DefaultTransport.(*http.Transport).Clone()}, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if len(via) > 0 && (req.URL.Scheme != via[0].URL.Scheme || req.URL.Host != via[0].URL.Host) {
			return errors.New("cross-origin redirects are not allowed")
		}
		return nil
	}}
}

var urlPattern = regexp.MustCompile(`https?://[^\s<>"']+`)

func Redact(message string, secrets ...string) string {
	message = urlPattern.ReplaceAllStringFunc(message, func(raw string) string {
		u, err := url.Parse(raw)
		if err != nil {
			return "[redacted URL]"
		}
		if (u.Path == "" || u.Path == "/") && u.RawQuery == "" && u.User == nil {
			return u.Scheme + "://" + u.Host + u.Path
		}
		return u.Scheme + "://" + u.Host + "/[redacted]"
	})
	for _, secret := range secrets {
		if len(secret) >= 3 {
			message = strings.ReplaceAll(message, secret, "[redacted]")
		}
	}
	message = strings.Map(func(r rune) rune {
		if r < 32 && r != '\t' {
			return ' '
		}
		return r
	}, message)
	if len(message) > 2048 {
		message = message[:2048] + "…"
	}
	return message
}
func SafeError(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Sprintf("request failed: %s", Redact(ue.Err.Error()))
	}
	return Redact(err.Error())
}

func (t Transport) CloseIdleConnections() {
	if closer, ok := t.Base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}
