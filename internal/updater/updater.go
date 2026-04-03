package updater

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"ddns-agent/internal/crypto"
	"ddns-agent/internal/database"
	"ddns-agent/internal/ipcheck"
	"ddns-agent/internal/logger"
	"ddns-agent/internal/provider"
	"ddns-agent/internal/provider/constants"
)

// ErrRecordDisabled is returned when a refresh is requested for a disabled record.
var ErrRecordDisabled = errors.New("record is disabled")

// WebhookNotifier is the minimal interface the updater needs to fire notifications.
type WebhookNotifier interface {
	Notify(event, message string)
}

type Service struct {
	db        *database.DB
	ipFetcher *ipcheck.Fetcher
	encryptor *crypto.Encryptor
	logger    *logger.Logger
	webhooks  WebhookNotifier

	providerClient atomic.Value // stores *http.Client for DNS provider API calls
	periodNs       int64        // update loop interval in nanoseconds; read/written atomically
	cooldownNs     int64        // cooldown duration in nanoseconds; read/written atomically

	mu    sync.Mutex   // serialises updateAll and ForceUpdateRecord
	force chan struct{} // buffered(1); sending triggers an immediate updateAll in Start
}

func New(db *database.DB, ipFetcher *ipcheck.Fetcher, enc *crypto.Encryptor,
	log *logger.Logger, webhooks WebhookNotifier,
	period, cooldown, httpTimeout time.Duration) *Service {
	s := &Service{
		db:        db,
		ipFetcher: ipFetcher,
		encryptor: enc,
		logger:    log,
		webhooks:  webhooks,
		force:     make(chan struct{}, 1),
	}
	s.providerClient.Store(&http.Client{Timeout: httpTimeout})
	s.periodNs = period.Nanoseconds()
	s.cooldownNs = cooldown.Nanoseconds()
	return s
}

func (s *Service) providerHTTP() *http.Client {
	return s.providerClient.Load().(*http.Client)
}

// SetHTTPTimeout hot-reloads timeouts for IP discovery and DNS provider API calls.
func (s *Service) SetHTTPTimeout(d time.Duration) {
	if d < time.Second {
		d = time.Second
	}
	if d > 15*time.Minute {
		d = 15 * time.Minute
	}
	s.providerClient.Store(&http.Client{Timeout: d})
	if s.ipFetcher != nil {
		s.ipFetcher.SetHTTPTimeout(d)
	}
	s.logger.Info("updater", "HTTP client timeout set to %s", d)
}

func (s *Service) period() time.Duration {
	return time.Duration(atomic.LoadInt64(&s.periodNs))
}

// SetPeriod hot-reloads the interval between automatic IP checks.
func (s *Service) SetPeriod(d time.Duration) {
	if d < time.Second {
		d = time.Second
	}
	atomic.StoreInt64(&s.periodNs, d.Nanoseconds())
	s.logger.Info("updater", "update interval set to %s", d)
}

// SetCooldown hot-reloads the minimum time between successful updates for the same record.
func (s *Service) SetCooldown(d time.Duration) {
	if d < 0 {
		d = 0
	}
	atomic.StoreInt64(&s.cooldownNs, d.Nanoseconds())
	s.logger.Info("updater", "cooldown period set to %s", d)
}

func (s *Service) cooldown() time.Duration {
	return time.Duration(atomic.LoadInt64(&s.cooldownNs))
}

func (s *Service) Start(ctx context.Context) {
	p := s.period()
	s.logger.Info("updater", "starting update loop with interval %s", p)
	s.updateAll(ctx)

	timer := time.NewTimer(p)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("updater", "shutting down")
			return
		case <-timer.C:
			s.updateAll(ctx)
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(s.period())
		case <-s.force:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			s.updateAll(ctx)
			timer.Reset(s.period())
		}
	}
}

// TriggerUpdate signals the update loop to run immediately.
// Returns true if the signal was queued, false if an update is already pending.
func (s *Service) TriggerUpdate() bool {
	select {
	case s.force <- struct{}{}:
		return true
	default:
		return false
	}
}

// ForceUpdateRecord immediately updates a single record, bypassing the normal loop.
func (s *Service) ForceUpdateRecord(ctx context.Context, recordID int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, err := s.db.GetRecord(recordID)
	if err != nil {
		return fmt.Errorf("record not found: %w", err)
	}
	if !rec.Enabled {
		return ErrRecordDisabled
	}

	ip, err := s.getIPForVersion(ctx, rec.IPVersion)
	if err != nil {
		return fmt.Errorf("fetching IP: %w", err)
	}

	return s.updateRecord(ctx, rec, ip)
}

func (s *Service) updateAll(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()

	records, err := s.db.ListEnabledRecords()
	if err != nil {
		s.logger.Error("updater", "listing records: %v", err)
		return
	}
	if len(records) == 0 {
		return
	}

	needV4, needV6 := false, false
	for _, r := range records {
		switch r.IPVersion {
		case "ipv4":
			needV4 = true
		case "ipv6":
			needV6 = true
		case "dual", "both": // "both" is a legacy option value
			needV4 = true
			needV6 = true
		}
	}

	var ipv4, ipv6 netip.Addr

	if needV4 {
		ipv4, err = s.ipFetcher.IPv4(ctx)
		if err != nil {
			s.logger.Error("updater", "fetching IPv4: %v", err)
		} else {
			s.logger.Info("updater", "current public IPv4: %s", ipv4)
		}
	}
	if needV6 {
		ipv6, err = s.ipFetcher.IPv6(ctx)
		if err != nil {
			s.logger.Warn("updater", "fetching IPv6: %v", err)
		} else {
			s.logger.Info("updater", "current public IPv6: %s", ipv6)
		}
	}

	var updateErrors int
	updated := 0
	skippedSameIP := 0
	for i := range records {
		rec := &records[i]
		ip := s.pickIP(rec.IPVersion, ipv4, ipv6)
		if !ip.IsValid() {
			continue
		}

		if rec.CurrentIP != nil && *rec.CurrentIP == ip.String() && rec.Status == "success" {
			skippedSameIP++
			continue
		}

		if !s.shouldUpdate(rec) {
			continue
		}

		if err := s.updateRecord(ctx, rec, ip); err != nil {
			updateErrors++
		} else {
			updated++
		}
	}

	if updated > 0 {
		s.logger.Success("updater", "IP updated for %d record(s)", updated)
	}
	if skippedSameIP > 0 {
		s.logger.Info("updater", "IP unchanged for %d record(s), no update needed", skippedSameIP)
	}
	if updateErrors > 0 {
		s.logger.Error("updater", "update failed for %d record(s)", updateErrors)
	}
}

func (s *Service) shouldUpdate(rec *database.Record) bool {
	if rec.LastUpdate != nil && time.Since(*rec.LastUpdate) < s.cooldown() && rec.Status == "success" {
		return false
	}
	if rec.LastBan != nil && time.Since(*rec.LastBan) < time.Hour {
		return false
	}
	return true
}

func (s *Service) updateRecord(ctx context.Context, rec *database.Record, ip netip.Addr) error {
	fqdn := rec.Owner + "." + rec.Domain
	if rec.Owner == "@" || rec.Owner == "" {
		fqdn = rec.Domain
	}

	if err := s.db.UpdateRecordStatus(rec.ID, "updating", "updating...", ""); err != nil {
		s.logger.Warn("updater", "%s: failed to set status 'updating': %v", fqdn, err)
	}
	s.logger.Info("updater", "updating %s (%s) to %s", fqdn, rec.Provider, ip)

	// Attempt to decrypt the stored provider config; fall back to plaintext for
	// legacy unencrypted rows where decryption will fail harmlessly.
	configJSON := rec.ProviderConfig
	if s.encryptor != nil {
		if dec, err := s.encryptor.Decrypt(configJSON); err == nil {
			configJSON = dec
		}
	}

	p, err := provider.New(
		constants.Provider(rec.Provider),
		json.RawMessage(configJSON),
		rec.Domain, rec.Owner,
		constants.IPVersion(rec.IPVersion),
	)
	if err != nil {
		msg := fmt.Sprintf("creating provider: %v", err)
		if dbErr := s.db.UpdateRecordStatus(rec.ID, "error", msg, ""); dbErr != nil {
			s.logger.Warn("updater", "%s: failed to persist error status: %v", fqdn, dbErr)
		}
		s.logger.Error("updater", "%s: %s", fqdn, msg)
		return fmt.Errorf("%s: %s", fqdn, msg)
	}

	newIP, err := p.Update(ctx, s.providerHTTP(), ip)
	if err != nil {
		msg := fmt.Sprintf("update failed: %v", err)
		if dbErr := s.db.UpdateRecordStatus(rec.ID, "error", msg, ""); dbErr != nil {
			s.logger.Warn("updater", "%s: failed to persist error status: %v", fqdn, dbErr)
		}
		s.logger.Error("updater", "%s: %s", fqdn, msg)
		if s.webhooks != nil {
			s.webhooks.Notify("error", fmt.Sprintf("%s update failed: %v", fqdn, err))
		}
		return fmt.Errorf("%s: %s", fqdn, msg)
	}

	ipStr := newIP.String()
	oldIP := ""
	if rec.CurrentIP != nil {
		oldIP = *rec.CurrentIP
	}

	if dbErr := s.db.UpdateRecordStatus(rec.ID, "success", "updated to "+ipStr, ipStr); dbErr != nil {
		s.logger.Warn("updater", "%s: failed to persist success status: %v", fqdn, dbErr)
	}
	s.logger.Success("updater", "%s updated to %s", fqdn, ipStr)

	if oldIP != ipStr {
		if dbErr := s.db.InsertIPHistory(rec.ID, ipStr); dbErr != nil {
			s.logger.Warn("updater", "%s: failed to insert IP history: %v", fqdn, dbErr)
		}
		if s.webhooks != nil {
			s.webhooks.Notify("ip_change", fmt.Sprintf("%s changed from %s to %s", fqdn, oldIP, ipStr))
		}
	}

	return nil
}

func (s *Service) getIPForVersion(ctx context.Context, version string) (netip.Addr, error) {
	switch version {
	case "ipv6":
		return s.ipFetcher.IPv6(ctx)
	default:
		return s.ipFetcher.IPv4(ctx)
	}
}

func (s *Service) pickIP(version string, v4, v6 netip.Addr) netip.Addr {
	switch version {
	case "ipv6":
		return v6
	case "dual", "both":
		if v4.IsValid() {
			return v4
		}
		return v6
	default:
		return v4
	}
}
