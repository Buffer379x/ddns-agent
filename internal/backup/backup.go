package backup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"ddns-agent/internal/database"
)

type Service struct {
	db        *database.DB
	backupDir string
	retention int
}

func New(db *database.DB, backupDir string, retention int) *Service {
	return &Service{db: db, backupDir: backupDir, retention: retention}
}

// StartAutoBackup runs a daily backup in a goroutine
func (s *Service) StartAutoBackup(stop <-chan struct{}) {
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		// run once on start
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

	_, err := s.db.Conn().Exec(fmt.Sprintf("VACUUM INTO '%s'", dest))
	if err != nil {
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
	for len(backups) > s.retention {
		os.Remove(filepath.Join(s.backupDir, backups[0]))
		backups = backups[1:]
	}
	return nil
}

// --- Config Export/Import ---

type ExportData struct {
	Version  string                `json:"version"`
	ExportedAt string             `json:"exported_at"`
	Settings map[string]string     `json:"settings"`
	Records  []database.Record     `json:"records"`
	Webhooks []database.Webhook    `json:"webhooks"`
	Users    []database.User       `json:"users"`
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

func (s *Service) Import(data []byte) error {
	var export ExportData
	if err := json.Unmarshal(data, &export); err != nil {
		return fmt.Errorf("parsing import data: %w", err)
	}

	for key, value := range export.Settings {
		if err := s.db.SetSetting(key, value); err != nil {
			return fmt.Errorf("importing setting %s: %w", key, err)
		}
	}

	for _, r := range export.Records {
		if _, err := s.db.CreateRecord(&r); err != nil {
			return fmt.Errorf("importing record %s.%s: %w", r.Owner, r.Domain, err)
		}
	}

	for _, w := range export.Webhooks {
		if _, err := s.db.CreateWebhook(&w); err != nil {
			return fmt.Errorf("importing webhook %s: %w", w.Name, err)
		}
	}

	return nil
}
