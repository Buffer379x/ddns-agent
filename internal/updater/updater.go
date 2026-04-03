package updater

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"ddns-agent/internal/crypto"
	"ddns-agent/internal/database"
	"ddns-agent/internal/ipcheck"
	"ddns-agent/internal/logger"
	"ddns-agent/internal/provider"
	"ddns-agent/internal/provider/constants"
)

// ErrRecordDisabled is returned when a refresh is requested for a record with enabled=false.
var ErrRecordDisabled = errors.New("record is disabled")

type WebhookNotifier interface {
	Notify(event, message string)
}

type Service struct {
	db        *database.DB
	ipFetcher *ipcheck.Fetcher
	encryptor *crypto.Encryptor
	logger    *logger.Logger
	webhooks  WebhookNotifier
	client    *http.Client
	period    time.Duration
	cooldown  time.Duration

	mu        sync.Mutex
	force     chan struct{}
	forceResult chan []error
}

func New(db *database.DB, ipFetcher *ipcheck.Fetcher, enc *crypto.Encryptor,
	log *logger.Logger, webhooks WebhookNotifier,
	period, cooldown, httpTimeout time.Duration) *Service {
	return &Service{
		db:          db,
		ipFetcher:   ipFetcher,
		encryptor:   enc,
		logger:      log,
		webhooks:    webhooks,
		client:      &http.Client{Timeout: httpTimeout},
		period:      period,
		cooldown:    cooldown,
		force:       make(chan struct{}, 1),
		forceResult: make(chan []error, 1),
	}
}

func (s *Service) Start(ctx context.Context) {
	s.logger.Info("updater", "starting update loop with interval %s", s.period)
	s.updateAll(ctx)

	ticker := time.NewTicker(s.period)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("updater", "shutting down")
			return
		case <-ticker.C:
			s.updateAll(ctx)
		case <-s.force:
			errs := s.updateAll(ctx)
			s.forceResult <- errs
		}
	}
}

func (s *Service) ForceUpdate() []error {
	select {
	case s.force <- struct{}{}:
		return <-s.forceResult
	default:
		return []error{fmt.Errorf("update already in progress")}
	}
}

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

func (s *Service) updateAll(ctx context.Context) []error {
	s.mu.Lock()
	defer s.mu.Unlock()

	records, err := s.db.ListEnabledRecords()
	if err != nil {
		s.logger.Error("updater", "listing records: %v", err)
		return []error{err}
	}
	if len(records) == 0 {
		return nil
	}

	needV4, needV6 := false, false
	for _, r := range records {
		switch r.IPVersion {
		case "ipv4":
			needV4 = true
		case "ipv6":
			needV6 = true
		case "dual", "both": // "both" was used by an older UI option value
			needV4 = true
			needV6 = true
		}
	}

	var ipv4, ipv6 netip.Addr
	var fetchErrors []error

	if needV4 {
		ipv4, err = s.ipFetcher.IPv4(ctx)
		if err != nil {
			s.logger.Error("updater", "fetching IPv4: %v", err)
			fetchErrors = append(fetchErrors, err)
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

	var updateErrors []error
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
			updateErrors = append(updateErrors, err)
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
	if len(updateErrors) > 0 {
		s.logger.Error("updater", "update failed for %d record(s)", len(updateErrors))
	}

	return append(fetchErrors, updateErrors...)
}

func (s *Service) shouldUpdate(rec *database.Record) bool {
	if rec.LastUpdate != nil && time.Since(*rec.LastUpdate) < s.cooldown && rec.Status == "success" {
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

	s.db.UpdateRecordStatus(rec.ID, "updating", "updating...", "")
	s.logger.Info("updater", "updating %s (%s) to %s", fqdn, rec.Provider, ip)

	configJSON := rec.ProviderConfig
	if s.encryptor != nil {
		decrypted, err := s.encryptor.Decrypt(configJSON)
		if err != nil {
			decrypted = configJSON
		} else {
			configJSON = decrypted
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
		s.db.UpdateRecordStatus(rec.ID, "error", msg, "")
		s.logger.Error("updater", "%s: %s", fqdn, msg)
		return fmt.Errorf("%s: %s", fqdn, msg)
	}

	newIP, err := p.Update(ctx, s.client, ip)
	if err != nil {
		msg := fmt.Sprintf("update failed: %v", err)
		s.db.UpdateRecordStatus(rec.ID, "error", msg, "")
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

	s.db.UpdateRecordStatus(rec.ID, "success", "updated to "+ipStr, ipStr)
	s.logger.Success("updater", "%s updated to %s", fqdn, ipStr)

	if oldIP != ipStr {
		s.db.InsertIPHistory(rec.ID, ipStr)
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
