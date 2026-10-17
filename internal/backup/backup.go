package backup

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ddns-agent/internal/crypto"
	"ddns-agent/internal/database"
	"ddns-agent/internal/validation"

	"golang.org/x/crypto/scrypt"
)

type Status struct {
	LastSuccess *time.Time `json:"last_success"`
	Error       string     `json:"error"`
}
type Service struct {
	db        *database.DB
	backupDir string
	retention atomic.Int32
	enc       *crypto.Encryptor
	mu        sync.Mutex
	operation sync.Mutex
	statusMu  sync.RWMutex
	status    Status
	wg        sync.WaitGroup
	report    func(string)
}

func New(db *database.DB, dir string, retention int, enc *crypto.Encryptor) *Service {
	s := &Service{db: db, backupDir: dir, enc: enc}
	s.SetRetention(retention)
	return s
}
func (s *Service) SetReporter(report func(string)) { s.report = report }
func (s *Service) SetRetention(n int) {
	if n < 1 {
		n = 1
	}
	if n > 365 {
		n = 365
	}
	s.retention.Store(int32(n))
}
func (s *Service) Status() Status { s.statusMu.RLock(); defer s.statusMu.RUnlock(); return s.status }
func (s *Service) StartAutoBackup(stop <-chan struct{}) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		run := func() {
			if err := s.RunBackup(); err != nil && s.report != nil {
				s.report(err.Error())
			}
			days := 30
			if v, err := s.db.GetSetting("data_retention_days"); err == nil {
				fmt.Sscan(v, &days)
			}
			if err := s.db.Prune(days); err != nil && s.report != nil {
				s.report("data retention: " + err.Error())
			}
		}
		run()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				run()
			}
		}
	}()
}
func (s *Service) Wait() { s.wg.Wait() }
func (s *Service) RunBackup() (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() {
		s.statusMu.Lock()
		defer s.statusMu.Unlock()
		if err != nil {
			s.status.Error = err.Error()
		} else {
			now := time.Now().UTC()
			s.status = Status{LastSuccess: &now}
		}
	}()
	if err = os.MkdirAll(s.backupDir, 0700); err != nil {
		return err
	}
	dest := filepath.Join(s.backupDir, "ddns-agent-"+time.Now().UTC().Format("2006-01-02_150405.000000000")+".db")
	if err = s.db.Vacuum(dest); err != nil {
		return err
	}
	if err = os.WriteFile(dest+".key", []byte(s.enc.RecoveryKey()), 0600); err != nil {
		_ = os.Remove(dest)
		return fmt.Errorf("saving recovery key: %w", err)
	}
	return s.cleanOld()
}
func (s *Service) cleanOld() error {
	entries, err := os.ReadDir(s.backupDir)
	if err != nil {
		return err
	}
	names := []string{}
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "ddns-agent-") && strings.HasSuffix(e.Name(), ".db") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for len(names) > int(s.retention.Load()) {
		for _, suffix := range []string{"", ".key", "-wal", "-shm"} {
			err = os.Remove(filepath.Join(s.backupDir, names[0]+suffix))
			if err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		names = names[1:]
	}
	return nil
}
func (s *Service) RunRawCopyBackup(src string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if src != s.db.Path() {
		return "", errors.New("invalid database path")
	}
	if err := os.MkdirAll(s.backupDir, 0700); err != nil {
		return "", err
	}
	dest := filepath.Join(s.backupDir, "ddns-agent-"+time.Now().UTC().Format("2006-01-02_150405.000000000")+"-raw.db")
	err := s.db.QuiescedSnapshot(func() error {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			if _, err := os.Stat(src + suffix); os.IsNotExist(err) && suffix != "" {
				continue
			}
			if err := copyFile(src+suffix, dest+suffix); err != nil {
				return err
			}
		}
		return os.WriteFile(dest+".key", []byte(s.enc.RecoveryKey()), 0600)
	})
	if err != nil {
		for _, suffix := range []string{"", "-wal", "-shm", ".key"} {
			_ = os.Remove(dest + suffix)
		}
		return "", fmt.Errorf("cannot safely snapshot live database; stop the app for offline recovery: %w", err)
	}
	return dest, s.cleanOld()
}
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	closeErr := out.Close()
	if err != nil {
		return err
	}
	return closeErr
}

type ExportData struct {
	Version    string             `json:"version"`
	ExportedAt string             `json:"exported_at"`
	KeyID      string             `json:"key_id,omitempty"`
	Settings   map[string]string  `json:"settings"`
	Records    []database.Record  `json:"records"`
	Webhooks   []database.Webhook `json:"webhooks"`
}
type Envelope struct {
	Version    string `json:"version"`
	KDF        string `json:"kdf"`
	Salt       string `json:"salt"`
	Ciphertext string `json:"ciphertext"`
}

func (s *Service) exportData(plain bool) (*ExportData, error) {
	settings, records, hooks, err := s.db.ConfigSnapshot()
	if err != nil {
		return nil, err
	}
	keyID := s.enc.Fingerprint()
	if plain {
		keyID = ""
		for i := range records {
			v, err := s.enc.OpenConfig(records[i].ProviderConfig)
			if err != nil {
				return nil, fmt.Errorf("cannot decrypt record %d", records[i].ID)
			}
			records[i].ProviderConfig = v
		}
		for i := range hooks {
			v, err := s.OpenWebhook(hooks[i].URL)
			if err != nil {
				return nil, err
			}
			hooks[i].URL = v
		}
	}
	return &ExportData{Version: "1.1", ExportedAt: time.Now().UTC().Format(time.RFC3339), KeyID: keyID, Settings: settings, Records: records, Webhooks: hooks}, nil
}
func (s *Service) Export() ([]byte, error) {
	data, err := s.exportData(false)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(data, "", "  ")
}
func derive(password string, salt []byte) (*crypto.Encryptor, error) {
	key, err := scrypt.Key([]byte(password), salt, 32768, 8, 1, 32)
	if err != nil {
		return nil, err
	}
	return crypto.NewEncryptor(hex.EncodeToString(key))
}
func (s *Service) ExportPortable(password string) ([]byte, error) {
	s.operation.Lock()
	defer s.operation.Unlock()
	if len(password) < 12 || len(password) > 256 {
		return nil, errors.New("export password must contain 12–256 bytes")
	}
	data, err := s.exportData(true)
	if err != nil {
		return nil, err
	}
	plain, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	salt, _ := hex.DecodeString(crypto.GenerateKey()[:32])
	enc, err := derive(password, salt)
	if err != nil {
		return nil, err
	}
	cipher, err := enc.Encrypt(string(plain))
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(Envelope{Version: "1.1-encrypted", KDF: "scrypt-32768-8-1", Salt: base64.StdEncoding.EncodeToString(salt), Ciphertext: cipher}, "", "  ")
}
func (s *Service) OpenWebhook(value string) (string, error) {
	if strings.HasPrefix(value, "v1:") {
		return s.enc.Decrypt(value)
	}
	return value, nil
}
func (s *Service) Prepare(data []byte, password string) (*ExportData, error) {
	s.operation.Lock()
	defer s.operation.Unlock()
	if len(data) > 10<<20 {
		return nil, errors.New("import exceeds 10 MiB")
	}
	var envelope Envelope
	if json.Unmarshal(data, &envelope) != nil {
		return nil, errors.New("invalid import JSON")
	}
	if envelope.Version == "1.1-encrypted" {
		if envelope.KDF != "scrypt-32768-8-1" || len(password) > 256 {
			return nil, errors.New("unsupported encrypted export")
		}
		salt, err := base64.StdEncoding.DecodeString(envelope.Salt)
		if err != nil || len(salt) != 16 {
			return nil, errors.New("invalid export salt")
		}
		enc, err := derive(password, salt)
		if err != nil {
			return nil, err
		}
		plain, err := enc.Decrypt(envelope.Ciphertext)
		if err != nil {
			return nil, errors.New("wrong export password or damaged file")
		}
		data = []byte(plain)
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return nil, errors.New("configuration must be an object")
	}
	if string(fields["records"]) == "null" || fields["records"] == nil {
		return nil, errors.New("configuration must contain a records array")
	}
	var export ExportData
	if err := json.Unmarshal(data, &export); err != nil {
		return nil, errors.New("invalid configuration")
	}
	if export.Version != "1.0" && export.Version != "1.1" {
		return nil, errors.New("unsupported configuration version")
	}
	if len(export.Records) > 10000 || len(export.Webhooks) > 100 {
		return nil, errors.New("too many records or webhooks")
	}
	if export.KeyID != "" && export.KeyID != s.enc.Fingerprint() {
		return nil, errors.New("export belongs to another instance; use a password-protected portable export")
	}
	if legacy, ok := export.Settings["update_interval"]; ok {
		if _, exists := export.Settings["refresh_interval"]; !exists {
			d, err := time.ParseDuration(legacy)
			if err != nil {
				return nil, errors.New("invalid legacy update interval")
			}
			export.Settings["refresh_interval"] = fmt.Sprint(int(d / time.Second))
		}
		delete(export.Settings, "update_interval")
	}
	if err := validation.Settings(export.Settings); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for i := range export.Records {
		r := &export.Records[i]
		plain, err := s.enc.OpenConfig(r.ProviderConfig)
		if err != nil {
			return nil, fmt.Errorf("cannot decrypt record %d; restore the original key or use a portable export", i+1)
		}
		r.ProviderConfig = plain
		if err := validation.Record(r); err != nil {
			return nil, fmt.Errorf("record %d: %w", i+1, err)
		}
		identity := r.Provider + "/" + r.Domain + "/" + r.Owner + "/" + r.IPVersion
		if seen[identity] {
			return nil, fmt.Errorf("duplicate record %s.%s", r.Owner, r.Domain)
		}
		seen[identity] = true
		r.ProviderConfig, err = s.enc.Encrypt(r.ProviderConfig)
		if err != nil {
			return nil, err
		}
	}
	existing, err := s.db.ListWebhooks()
	if err != nil {
		return nil, err
	}
	identities := map[string]bool{}
	for _, h := range existing {
		plain, err := s.OpenWebhook(h.URL)
		if err != nil {
			return nil, err
		}
		identities[h.Name+"/"+h.Type+"/"+plain] = true
	}
	hooks := []database.Webhook{}
	for _, h := range export.Webhooks {
		h.URL, err = s.OpenWebhook(h.URL)
		if err != nil {
			return nil, errors.New("cannot decrypt webhook")
		}
		if err := validation.Webhook(&h); err != nil {
			return nil, err
		}
		identity := h.Name + "/" + h.Type + "/" + h.URL
		if identities[identity] {
			continue
		}
		identities[identity] = true
		h.URL, err = s.enc.Encrypt(h.URL)
		if err != nil {
			return nil, err
		}
		hooks = append(hooks, h)
	}
	export.Webhooks = hooks
	return &export, nil
}
func (s *Service) Import(data []byte, mode string) error { return s.ImportWithPassword(data, mode, "") }
func (s *Service) ImportWithPassword(data []byte, mode, password string) error {
	if mode != "merge" && mode != "replace" {
		return errors.New("records must be merge or replace")
	}
	prepared, err := s.Prepare(data, password)
	if err != nil {
		return err
	}
	if err = s.RunBackup(); err != nil {
		return fmt.Errorf("backup before import failed: %w", err)
	}
	return s.db.ApplyConfig(prepared.Settings, prepared.Records, prepared.Webhooks, mode)
}
