package updater

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ddns-agent/internal/crypto"
	"ddns-agent/internal/database"
	"ddns-agent/internal/logger"
	"ddns-agent/internal/netutil"
	"ddns-agent/internal/provider"
	"ddns-agent/internal/provider/constants"
	"ddns-agent/internal/validation"
)

var ErrRecordDisabled = errors.New("record is disabled")

type WebhookNotifier interface{ Notify(string, string) }
type IPFetcher interface {
	IPv4(context.Context) (netip.Addr, error)
	IPv6(context.Context) (netip.Addr, error)
	SetHTTPTimeout(time.Duration)
}
type Service struct {
	db             *database.DB
	ipFetcher      IPFetcher
	encryptor      *crypto.Encryptor
	logger         *logger.Logger
	webhooks       WebhookNotifier
	providerClient atomic.Pointer[http.Client]
	periodNs       atomic.Int64
	cooldownNs     atomic.Int64
	gate           sync.RWMutex
	manual         chan int
	force          chan struct{}
	reset          chan struct{}
	locksMu        sync.Mutex
	providerLocks  map[string]chan struct{}
	changed        func()
}

func New(db *database.DB, ipFetcher IPFetcher, enc *crypto.Encryptor, log *logger.Logger, hooks WebhookNotifier, period, cooldown, timeout time.Duration) *Service {
	s := &Service{db: db, ipFetcher: ipFetcher, encryptor: enc, logger: log, webhooks: hooks, manual: make(chan int, 32), force: make(chan struct{}, 1), reset: make(chan struct{}, 1), providerLocks: map[string]chan struct{}{}}
	s.providerClient.Store(netutil.NewClient(timeout))
	s.periodNs.Store(int64(period))
	s.cooldownNs.Store(int64(cooldown))
	return s
}
func (s *Service) SetChanged(fn func()) { s.changed = fn }
func (s *Service) notifyChanged() {
	if s.changed != nil {
		s.changed()
	}
}
func (s *Service) Exclusive(fn func() error) error {
	s.gate.Lock()
	defer s.gate.Unlock()
	err := fn()
	if err == nil {
		s.notifyChanged()
	}
	return err
}
func (s *Service) SetHTTPTimeout(d time.Duration) {
	if d < time.Second {
		d = time.Second
	}
	if d > 15*time.Minute {
		d = 15 * time.Minute
	}
	old := s.providerClient.Swap(netutil.NewClient(d))
	if old != nil {
		old.CloseIdleConnections()
	}
	s.ipFetcher.SetHTTPTimeout(d)
}
func (s *Service) SetPeriod(d time.Duration) {
	if d < time.Minute {
		d = time.Minute
	}
	s.periodNs.Store(int64(d))
	select {
	case s.reset <- struct{}{}:
	default:
	}
}
func (s *Service) SetCooldown(d time.Duration) {
	if d < 0 {
		d = 0
	}
	s.cooldownNs.Store(int64(d))
}
func (s *Service) TriggerUpdate() bool {
	select {
	case s.force <- struct{}{}:
		return true
	default:
		return false
	}
}
func (s *Service) Start(ctx context.Context) {
	s.updateAll(ctx)
	timer := time.NewTimer(time.Duration(s.periodNs.Load()))
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-s.manual:
			_ = s.ForceUpdateRecord(ctx, id)
		case <-s.reset:
		case <-s.force:
			s.updateAll(ctx)
		case <-timer.C:
			s.updateAll(ctx)
		}
		timer.Reset(time.Duration(s.periodNs.Load()))
	}
}
func (s *Service) providerLock(ctx context.Context, name string) (func(), error) {
	s.locksMu.Lock()
	ch := s.providerLocks[name]
	if ch == nil {
		ch = make(chan struct{}, 1)
		s.providerLocks[name] = ch
	}
	s.locksMu.Unlock()
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func families(version string) []string {
	switch version {
	case "ipv6":
		return []string{"ipv6"}
	case "dual", "both":
		return []string{"ipv4", "ipv6"}
	default:
		return []string{"ipv4"}
	}
}
func (s *Service) ForceUpdateRecord(ctx context.Context, id int) error {
	s.gate.RLock()
	defer s.gate.RUnlock()
	rec, err := s.db.GetRecord(id)
	if err != nil {
		return err
	}
	if !rec.Enabled {
		return ErrRecordDisabled
	}
	release, err := s.providerLock(ctx, rec.Provider)
	if err != nil {
		return err
	}
	defer release()
	var errs []error
	for _, family := range families(rec.IPVersion) {
		ip, err := s.fetch(ctx, family)
		if err != nil {
			s.fail(rec, family, "IP detection failed: "+netutil.SafeError(err))
			errs = append(errs, err)
			continue
		}
		if err = s.updateRecord(ctx, rec, family, ip); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
func (s *Service) fetch(ctx context.Context, family string) (netip.Addr, error) {
	if family == "ipv6" {
		return s.ipFetcher.IPv6(ctx)
	}
	return s.ipFetcher.IPv4(ctx)
}
func (s *Service) updateAll(ctx context.Context) {
	s.gate.RLock()
	defer s.gate.RUnlock()
	records, err := s.db.ListEnabledRecords()
	if err != nil {
		s.logger.Error("updater", "listing records: %v", err)
		return
	}
	if len(records) == 0 {
		return
	}
	need := map[string]bool{}
	for _, rec := range records {
		for _, family := range families(rec.IPVersion) {
			need[family] = true
		}
	}
	ips := map[string]netip.Addr{}
	ipErrors := map[string]error{}
	for _, family := range []string{"ipv4", "ipv6"} {
		if need[family] {
			ips[family], ipErrors[family] = s.fetch(ctx, family)
		}
	}
	var wg sync.WaitGroup
	slots := make(chan struct{}, 4)
	for _, rec := range records {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return
		}
		wg.Add(1)
		go func(rec database.Record) {
			defer wg.Done()
			defer func() { <-slots }()
			release, err := s.providerLock(ctx, rec.Provider)
			if err != nil {
				return
			}
			defer release()
			current, err := s.db.GetRecord(rec.ID)
			if err != nil || !current.Enabled {
				return
			}
			if current.LastBan != nil && time.Since(*current.LastBan) < time.Hour {
				return
			}
			for _, family := range families(current.IPVersion) {
				if ctx.Err() != nil {
					return
				}
				if err := ipErrors[family]; err != nil {
					s.fail(current, family, "IP detection failed: "+netutil.SafeError(err))
					continue
				}
				ip := ips[family]
				old, status := current.CurrentIPv4, current.StatusIPv4
				if family == "ipv6" {
					old, status = current.CurrentIPv6, current.StatusIPv6
				}
				if old != nil && *old == ip.String() && status == "success" {
					continue
				}
				if current.LastSuccess != nil && status == "success" && time.Since(*current.LastSuccess) < time.Duration(s.cooldownNs.Load()) {
					continue
				}
				_ = s.updateRecord(ctx, current, family, ip)
			}
		}(rec)
	}
	wg.Wait()
}
func hostname(rec *database.Record) string {
	if rec.Owner == "@" || rec.Owner == "" {
		return rec.Domain
	}
	return rec.Owner + "." + rec.Domain
}
func (s *Service) fail(rec *database.Record, family, message string) {
	if err := s.db.UpdateFamily(rec.ID, family, "error", message, ""); err != nil {
		s.logger.Error("updater", "persisting error for record %d: %v", rec.ID, err)
	}
	s.logger.Error("updater", "%s (%s): %s", hostname(rec), family, message)
	s.notifyChanged()
	if s.webhooks != nil {
		s.webhooks.Notify("error", hostname(rec)+" ("+family+"): "+message)
	}
}
func (s *Service) updateRecord(ctx context.Context, rec *database.Record, family string, ip netip.Addr) error {
	config, err := s.encryptor.OpenConfig(rec.ProviderConfig)
	if err != nil {
		s.fail(rec, family, "provider credentials cannot be decrypted; restore the original key")
		return errors.New("credentials unavailable")
	}
	p, err := provider.New(constants.Provider(rec.Provider), json.RawMessage(config), rec.Domain, rec.Owner, constants.IPVersion(family))
	if err != nil {
		msg := netutil.Redact(err.Error(), validation.SecretValues(config)...)
		s.fail(rec, family, msg)
		return errors.New(msg)
	}
	if err = s.db.UpdateFamily(rec.ID, family, "updating", "Updating", ""); err != nil {
		return err
	}
	s.notifyChanged()
	// Each provider operation has a total budget in addition to per-request timeouts.
	updateCtx, cancel := context.WithTimeout(ctx, s.providerClient.Load().Timeout*8)
	defer cancel()
	newIP, err := p.Update(updateCtx, s.providerClient.Load(), ip)
	if err == nil && newIP != ip {
		err = errors.New("provider returned an unexpected address")
	}
	if err != nil {
		message := netutil.Redact(netutil.SafeError(err), validation.SecretValues(config)...)
		lower := strings.ToLower(message)
		for _, marker := range []string{"abuse", "authentication", "badauth", "rate limit", "status 429", "http 429", "status 401", "http 401", "status 403", "http 403"} {
			if strings.Contains(lower, marker) {
				now := time.Now()
				_ = s.db.UpdateRecordBan(rec.ID, &now)
				break
			}
		}
		s.fail(rec, family, message)
		return errors.New(message)
	}
	old := rec.CurrentIPv4
	if family == "ipv6" {
		old = rec.CurrentIPv6
	}
	if err = s.db.UpdateFamily(rec.ID, family, "success", "Synchronized", ip.String()); err != nil {
		return err
	}
	_ = s.db.UpdateRecordBan(rec.ID, nil)
	if old == nil || *old != ip.String() {
		if err = s.db.InsertIPHistory(rec.ID, ip.String()); err != nil {
			s.logger.Warn("updater", "saving IP history: %v", err)
		}
		if s.webhooks != nil {
			s.webhooks.Notify("ip_change", fmt.Sprintf("%s (%s) changed to %s", hostname(rec), family, ip))
		}
	}
	s.logger.Success("updater", "%s (%s) synchronized to %s", hostname(rec), family, ip)
	s.notifyChanged()
	return nil
}

func (s *Service) RequestUpdate(id int) error {
	rec, err := s.db.GetRecord(id)
	if err != nil {
		return err
	}
	if !rec.Enabled {
		return ErrRecordDisabled
	}
	select {
	case s.manual <- id:
		return nil
	default:
		return errors.New("refresh queue is full")
	}
}
