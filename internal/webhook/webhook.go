package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"ddns-agent/internal/crypto"
	"ddns-agent/internal/database"
	"ddns-agent/internal/netutil"
)

type delivery struct {
	hook           database.Webhook
	event, message string
}

type Service struct {
	db     *database.DB
	client *http.Client
	enc    *crypto.Encryptor
	queue  chan delivery
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	mu     sync.Mutex
	closed bool
	report func(string)
}

func New(db *database.DB, enc *crypto.Encryptor, report func(string)) *Service {
	if report == nil {
		report = func(string) {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{db: db, enc: enc, client: netutil.NewClient(10 * time.Second), queue: make(chan delivery, 128), ctx: ctx, cancel: cancel, report: report}
	for i := 0; i < 8; i++ {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			for {
				select {
				case <-s.ctx.Done():
					return
				case task, ok := <-s.queue:
					if !ok {
						return
					}
					s.send(task.hook, task.event, task.message)
				}
			}
		}()
	}
	return s
}

// Notify sends a notification for the given event to all enabled, subscribed webhooks.
// Delivery uses a bounded queue and eight workers; failures do not affect other webhooks.
func (s *Service) Notify(event, message string) {
	hooks, err := s.db.EnabledWebhooksForEvent(event)
	if err != nil {
		s.report("cannot load webhooks")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	for _, hook := range hooks {
		select {
		case s.queue <- delivery{hook, event, message}:
		default:
			s.report("webhook queue full; delivery skipped")
		}
	}
}

func (s *Service) send(hook database.Webhook, event, message string) {
	if strings.HasPrefix(hook.URL, "v1:") {
		plain, err := s.enc.Decrypt(hook.URL)
		if err != nil {
			s.report("cannot decrypt webhook endpoint")
			return
		}
		hook.URL = plain
	}
	var err error
	switch hook.Type {
	case "discord":
		err = s.sendDiscord(hook.URL, event, message)
	case "slack":
		err = s.postJSON(hook.URL, map[string]string{"text": "DDNS Agent · " + event + "\n" + message})
	case "telegram":
		err = s.sendTelegram(hook.URL, event, message)
	case "gotify":
		err = s.sendGotify(hook.URL, event, message)
	default:
		err = s.sendGeneric(hook.URL, event, message)
	}
	if err != nil {
		s.report("webhook delivery failed: " + netutil.SafeError(err))
	}
}

func (s *Service) sendDiscord(webhookURL, event, message string) error {
	payload := map[string]any{
		"embeds": []map[string]any{
			{
				"title":       fmt.Sprintf("DDNS Agent: %s", event),
				"description": message,
				"color":       colorForEvent(event),
				"timestamp":   time.Now().Format(time.RFC3339),
			},
		},
	}
	return s.postJSON(webhookURL, payload)
}

func (s *Service) sendTelegram(urlWithToken, event, message string) error {
	parts := strings.SplitN(urlWithToken, "|", 2)
	if len(parts) != 2 {
		return fmt.Errorf("telegram URL format: bot_token|chat_id")
	}
	botToken, chatID := parts[0], parts[1]
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", botToken)
	payload := map[string]any{
		"chat_id": chatID,
		"text":    fmt.Sprintf("*DDNS Agent - %s*\n%s", event, message),
	}
	return s.postJSON(apiURL, payload)
}

func (s *Service) sendGotify(serverURL, event, message string) error {
	payload := map[string]any{
		"title":    fmt.Sprintf("DDNS Agent: %s", event),
		"message":  message,
		"priority": priorityForEvent(event),
	}
	endpoint, err := url.Parse(serverURL)
	if err != nil {
		return err
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/")
	if !strings.HasSuffix(endpoint.Path, "/message") {
		endpoint.Path += "/message"
	}
	return s.postJSON(endpoint.String(), payload)
}

func (s *Service) sendGeneric(webhookURL, event, message string) error {
	payload := map[string]any{
		"event":     event,
		"message":   message,
		"timestamp": time.Now().Format(time.RFC3339),
		"source":    "ddns-agent",
	}
	return s.postJSON(webhookURL, payload)
}

func (s *Service) postJSON(url string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	// Delivery outlives an API request, but remains cancellable during shutdown.
	req, err := http.NewRequestWithContext(s.ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("webhook request failed: %s", netutil.SafeError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned %d", resp.StatusCode)
	}
	return nil
}

// SendTest synchronously delivers a test notification and returns any delivery error.
func (s *Service) SendTest(hook database.Webhook) (err error) {
	defer func() {
		if err != nil {
			err = fmt.Errorf("%s", netutil.Redact(netutil.SafeError(err), hook.URL))
		}
	}()

	if strings.HasPrefix(hook.URL, "v1:") {
		plain, err := s.enc.Decrypt(hook.URL)
		if err != nil {
			return fmt.Errorf("cannot decrypt webhook")
		}
		hook.URL = plain
	}
	const msg = "This is a test notification from DDNS Agent"
	switch hook.Type {
	case "discord":
		return s.sendDiscord(hook.URL, "test", msg)
	case "slack":
		return s.postJSON(hook.URL, map[string]string{"text": "DDNS Agent · test\n" + msg})
	case "telegram":
		return s.sendTelegram(hook.URL, "test", msg)
	case "gotify":
		return s.sendGotify(hook.URL, "test", msg)
	default:
		return s.sendGeneric(hook.URL, "test", msg)
	}
}

func colorForEvent(event string) int {
	switch event {
	case "ip_change":
		return 0x22C55E // green
	case "error":
		return 0xEF4444 // red
	case "warning":
		return 0xF59E0B // amber
	default:
		return 0x3B82F6 // blue
	}
}

func priorityForEvent(event string) int {
	switch event {
	case "error":
		return 8
	case "warning":
		return 5
	default:
		return 3
	}
}

func (s *Service) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	close(s.queue)
	s.mu.Unlock()
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		s.cancel()
		<-done
	}
	s.cancel()
	s.client.CloseIdleConnections()
}
