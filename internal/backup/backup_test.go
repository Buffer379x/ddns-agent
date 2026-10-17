package backup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"ddns-agent/internal/crypto"
	"ddns-agent/internal/database"
)

func fixture(t *testing.T) (*Service, *database.DB) {
	t.Helper()
	dir := t.TempDir()
	db, err := database.New(filepath.Join(dir, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	enc, _ := crypto.NewEncryptor(crypto.GenerateKey())
	return New(db, filepath.Join(dir, "backups"), 2, enc), db
}
func TestImportRejectsEmptyAndValidatesBeforeChangingData(t *testing.T) {
	s, db := fixture(t)
	db.SetSetting("refresh_interval", "300")
	for _, data := range []string{`{}`, `{"version":"1.0","records":null}`, `{"version":"1.0","settings":{"refresh_interval":"0"},"records":[]}`, `{"version":"1.0","records":[{"provider":"duckdns","domain":"example.net","ip_version":"ipv4","provider_config":"invalid"}]}`} {
		if err := s.Import([]byte(data), "replace"); err == nil {
			t.Fatalf("accepted %s", data)
		}
	}
	v, _ := db.GetSetting("refresh_interval")
	if v != "300" {
		t.Fatal("changed settings after failed validation")
	}
}
func TestPortableRoundTripAcrossKeysAndRecoveryPair(t *testing.T) {
	s, db := fixture(t)
	config, _ := s.enc.Encrypt(`{"token":"private-value"}`)
	db.CreateRecord(&database.Record{Provider: "duckdns", Domain: "example.net", Owner: "home", IPVersion: "dual", ProviderConfig: config, Enabled: true})
	db.CreateUser("admin", "hash", "admin")
	data, err := s.ExportPortable("correct export password")
	if err != nil {
		t.Fatal(err)
	}
	var env map[string]any
	json.Unmarshal(data, &env)
	if env["ciphertext"] == nil {
		t.Fatal("portable export not encrypted")
	}
	other, target := fixture(t)
	if _, err = other.Prepare(data, "wrong password"); err == nil {
		t.Fatal("wrong password accepted")
	}
	if err = other.ImportWithPassword(data, "merge", "correct export password"); err != nil {
		t.Fatal(err)
	}
	records, _ := target.ListRecords()
	if len(records) != 1 {
		t.Fatal(len(records))
	}
	plain, err := other.enc.OpenConfig(records[0].ProviderConfig)
	if err != nil || plain != `{"token":"private-value"}` {
		t.Fatalf("credential lost: %s %v", plain, err)
	}
	count, _ := target.UserCount()
	if count != 0 {
		t.Fatal("users unexpectedly imported")
	}
	local, _ := s.Export()
	if _, err = other.Prepare(local, ""); err == nil {
		t.Fatal("accepted another instance key")
	}
	if err = s.RunBackup(); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(s.backupDir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".db" {
			key, err := os.ReadFile(filepath.Join(s.backupDir, e.Name()+".key"))
			if err != nil || string(key) != s.enc.RecoveryKey() {
				t.Fatal("missing backup recovery key")
			}
			restored, err := database.New(filepath.Join(s.backupDir, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			issues, err := restored.CheckIntegrity()
			restored.Close()
			if err != nil || len(issues) > 0 {
				t.Fatalf("unrestorable backup: %v %v", issues, err)
			}
		}
	}
}
