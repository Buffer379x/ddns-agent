package backup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"ddns-agent/internal/database"
)

type Service struct {
	db        *database.DB
	backupDir string
	retention atomic.Int32 // number of automatic DB backups to keep under backupDir
}

func New(db *database.DB, backupDir string, retention int) *Service {
	s := &Service{db: db, backupDir: backupDir}
	if retention < 1 {
		retention = 1
	}
	if retention > 365 {
		retention = 365
	}
	s.retention.Store(int32(retention))
	return s
}

func (s *Service) getRetention() int {
	return int(s.retention.Load())
}

// SetRetention updates how many rotated backup files are kept (hot-reload from settings).
func (s *Service) SetRetention(n int) {
	if n < 1 {
		n = 1
	}
	if n > 365 {
		n = 365
	}
	s.retention.Store(int32(n))
}

// StartAutoBackup runs a daily backup goroutine that exits when stop is closed.
func (s *Service) StartAutoBackup(stop <-chan struct{}) {
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		// Run once immediately on start.
		s.RunBackup()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				s.RunBackup()
			}
		}
	}()
}

func (s *Service) RunBackup() error {
	if err := os.MkdirAll(s.backupDir, 0755); err != nil {
		return fmt.Errorf("creating backup dir: %w", err)
	}

	filename := fmt.Sprintf("ddns-agent-%s.db", time.Now().Format("2006-01-02_150405"))
	dest := filepath.Join(s.backupDir, filename)

	if err := s.db.Vacuum(dest); err != nil {
		return fmt.Errorf("vacuum into: %w", err)
	}

	return s.cleanOld()
}

func (s *Service) cleanOld() error {
	entries, err := os.ReadDir(s.backupDir)
	if err != nil {
		return err
	}
	var backups []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "ddns-agent-") && strings.HasSuffix(e.Name(), ".db") {
			backups = append(backups, e.Name())
		}
	}
	sort.Strings(backups)
	for len(backups) > s.getRetention() {
		os.Remove(filepath.Join(s.backupDir, backups[0]))
		backups = backups[1:]
	}
	return nil
}

// --- Config Export / Import ---

type ExportData struct {
	Version    string              `json:"version"`
	ExportedAt string              `json:"exported_at"`
	Settings   map[string]string   `json:"settings"`
	Records    []database.Record   `json:"records"`
	Webhooks   []database.Webhook  `json:"webhooks"`
	Users      []database.User     `json:"users"`
}

func (s *Service) Export() ([]byte, error) {
	settings, err := s.db.AllSettings()
	if err != nil {
		return nil, fmt.Errorf("exporting settings: %w", err)
	}
	records, err := s.db.ListRecords()
	if err != nil {
		return nil, fmt.Errorf("exporting records: %w", err)
	}
	webhooks, err := s.db.ListWebhooks()
	if err != nil {
		return nil, fmt.Errorf("exporting webhooks: %w", err)
	}
	users, err := s.db.ListUsers()
	if err != nil {
		return nil, fmt.Errorf("exporting users: %w", err)
	}

	data := ExportData{
		Version:    "1.0",
		ExportedAt: time.Now().Format(time.RFC3339),
		Settings:   settings,
		Records:    records,
		Webhooks:   webhooks,
		Users:      users,
	}
	return json.MarshalIndent(data, "", "  ")
}

// Import applies exported JSON. recordsMode controls record handling:
//   - "replace" deletes all existing records first, then inserts;
//   - "merge"   skips records that already exist (same provider, domain, owner).
//
// Webhooks are always merged: existing entries (same name+type+url) are skipped.
func (s *Service) Import(data []byte, recordsMode string) error {
	var export ExportData
	if err := json.Unmarshal(data, &export); err != nil {
		return fmt.Errorf("parsing import data: %w", err)
	}

	for key, value := range export.Settings {
		if err := s.db.SetSetting(key, value); err != nil {
			return fmt.Errorf("importing setting %s: %w", key, err)
		}
	}

	switch recordsMode {
	case "replace":
		if err := s.db.DeleteAllRecords(); err != nil {
			return fmt.Errorf("clearing records: %w", err)
		}
		for _, r := range export.Records {
			r.ID = 0
			if _, err := s.db.CreateRecord(&r); err != nil {
				return fmt.Errorf("importing record %s.%s: %w", r.Owner, r.Domain, err)
			}
		}
	case "merge":
		for _, r := range export.Records {
			r.ID = 0
			exists, err := s.db.RecordExists(r.Provider, r.Domain, r.Owner)
			if err != nil {
				return fmt.Errorf("checking record %s.%s: %w", r.Owner, r.Domain, err)
			}
			if exists {
				continue
			}
			if _, err := s.db.CreateRecord(&r); err != nil {
				return fmt.Errorf("importing record %s.%s: %w", r.Owner, r.Domain, err)
			}
		}
	default:
		return fmt.Errorf("invalid records mode %q (use merge or replace)", recordsMode)
	}

	// Webhooks are always merged — skip duplicates by name+type+url.
	for _, w := range export.Webhooks {
		w.ID = 0
		exists, err := s.db.WebhookExists(w.Name, w.Type, w.URL)
		if err != nil {
			return fmt.Errorf("checking webhook %s: %w", w.Name, err)
		}
		if exists {
			continue
		}
		if _, err := s.db.CreateWebhook(&w); err != nil {
			return fmt.Errorf("importing webhook %s: %w", w.Name, err)
		}
	}

	return nil
}
