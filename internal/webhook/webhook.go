package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"ddns-agent/internal/database"
)

type Service struct {
	db     *database.DB
	client *http.Client
}

func New(db *database.DB) *Service {
	return &Service{
		db:     db,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// Notify sends a notification for the given event to all enabled, subscribed webhooks.
// Delivery is fire-and-forget; individual failures do not affect other webhooks.
func (s *Service) Notify(event, message string) {
	hooks, err := s.db.EnabledWebhooksForEvent(event)
	if err != nil {
		return
	}
	for _, h := range hooks {
		go s.send(h, event, message)
	}
}

func (s *Service) send(hook database.Webhook, event, message string) {
	var err error
	switch hook.Type {
	case "discord":
		err = s.sendDiscord(hook.URL, event, message)
	case "telegram":
		err = s.sendTelegram(hook.URL, event, message)
	case "gotify":
		err = s.sendGotify(hook.URL, event, message)
	default:
		err = s.sendGeneric(hook.URL, event, message)
	}
	if err != nil {
		// Delivery errors are intentionally not logged to avoid recursive log→webhook loops.
		_ = err
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
		"chat_id":    chatID,
		"text":       fmt.Sprintf("*DDNS Agent - %s*\n%s", event, message),
		"parse_mode": "Markdown",
	}
	return s.postJSON(apiURL, payload)
}

func (s *Service) sendGotify(serverURL, event, message string) error {
	payload := map[string]any{
		"title":    fmt.Sprintf("DDNS Agent: %s", event),
		"message":  message,
		"priority": priorityForEvent(event),
	}
	url := strings.TrimRight(serverURL, "/") + "/message"
	return s.postJSON(url, payload)
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
	// Use context.Background so the goroutine is not tied to any request context.
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook returned %d", resp.StatusCode)
	}
	return nil
}

// SendTest synchronously delivers a test notification and returns any delivery error.
func (s *Service) SendTest(hook database.Webhook) error {
	const msg = "This is a test notification from DDNS Agent"
	switch hook.Type {
	case "discord":
		return s.sendDiscord(hook.URL, "test", msg)
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
