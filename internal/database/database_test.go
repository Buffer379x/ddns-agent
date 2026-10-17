package database

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestLegacyUpgradeIsRepeatableAndPreservesData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	conn, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(schema); err != nil {
		t.Fatal(err)
	}
	_, err = conn.Exec(`INSERT INTO users(username,password_hash)VALUES('admin','hash');INSERT INTO records(provider,domain,owner,ip_version,current_ip,status,provider_config)VALUES('duckdns','example.net','home','both','203.0.113.1','success','{}')`)
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	for i := 0; i < 2; i++ {
		db, err := New(path)
		if err != nil {
			t.Fatal(err)
		}
		r, err := db.GetRecord(1)
		if err != nil {
			t.Fatal(err)
		}
		if r.IPVersion != "dual" || r.CurrentIPv4 == nil || *r.CurrentIPv4 != "203.0.113.1" || r.CurrentIPv6 != nil {
			t.Fatalf("bad migration: %+v", r)
		}
		if issues, err := db.CheckIntegrity(); err != nil || len(issues) > 0 {
			t.Fatalf("integrity: %v %v", issues, err)
		}
		db.Close()
	}
}
func TestDualStackPartialFailureAndConfigChanges(t *testing.T) {
	db, err := New(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := Record{Provider: "duckdns", Domain: "example.net", Owner: "@", IPVersion: "dual", ProviderConfig: "cipher", Enabled: true}
	id, _ := db.CreateRecord(&r)
	r.ID = int(id)
	if err = db.UpdateFamily(r.ID, "ipv4", "success", "OK", "203.0.113.1"); err != nil {
		t.Fatal(err)
	}
	db.UpdateFamily(r.ID, "ipv6", "error", "no route", "")
	got, _ := db.GetRecord(r.ID)
	if got.Status != "error" || got.StatusIPv4 != "success" || got.CurrentIPv4 == nil || got.LastSuccess == nil {
		t.Fatalf("partial failure: %+v", got)
	}
	db.UpdateFamily(r.ID, "ipv6", "success", "OK", "2001:db8::1")
	got, _ = db.GetRecord(r.ID)
	if got.Status != "success" {
		t.Fatal(got.Status)
	}
	r.Enabled = false
	db.UpdateRecord(&r)
	got, _ = db.GetRecord(r.ID)
	if got.Status != "success" {
		t.Fatal("toggle reset successful state")
	}
	r.Domain = "changed.example"
	db.UpdateRecord(&r)
	got, _ = db.GetRecord(r.ID)
	if got.StatusIPv4 != "pending" || got.StatusIPv6 != "pending" {
		t.Fatal("domain change did not reset families")
	}
}
func TestLastAdministratorProtectedAndApplyAtomic(t *testing.T) {
	db, _ := New(filepath.Join(t.TempDir(), "app.db"))
	defer db.Close()
	u, _ := db.CreateUser("admin", "hash", "admin")
	if db.DeleteUser(u.ID) == nil || db.UpdateUser(u.ID, "admin", "", "viewer") == nil {
		t.Fatal("removed last admin")
	}
	db.SetSetting("refresh_interval", "300")
	db.conn.Exec(`CREATE TRIGGER fail_record BEFORE INSERT ON records BEGIN SELECT RAISE(FAIL,'disk simulation'); END`)
	err := db.ApplyConfig(map[string]string{"refresh_interval": "600"}, []Record{{Provider: "duckdns", Domain: "example.net", Owner: "@", IPVersion: "ipv4", ProviderConfig: "cipher", Enabled: true}}, nil, "replace")
	if err == nil {
		t.Fatal("expected injected failure")
	}
	value, _ := db.GetSetting("refresh_interval")
	if value != "300" {
		t.Fatal("partial settings import committed")
	}
}

func TestRetentionRemovesOldHistoryWithoutTouchingConfiguration(t *testing.T) {
	db, _ := New(filepath.Join(t.TempDir(), "app.db"))
	defer db.Close()
	id, _ := db.CreateRecord(&Record{Provider: "duckdns", Domain: "example.net", Owner: "@", IPVersion: "ipv4", ProviderConfig: "secret", Enabled: true})
	db.conn.Exec(`INSERT INTO logs(level,message,created_at)VALUES('ERROR','old',datetime('now','-40 days')),('INFO','recent',datetime('now'));INSERT INTO ip_history(record_id,ip,changed_at)VALUES(?,'203.0.113.1',datetime('now','-40 days'))`, id)
	if err := db.Prune(30); err != nil {
		t.Fatal(err)
	}
	logs, total, err := db.ListLogs("", 100, 0)
	if err != nil || total != 1 || logs[0].Message != "recent" {
		t.Fatal("log retention failed", logs, err)
	}
	if _, err = db.GetRecord(int(id)); err != nil {
		t.Fatal("configuration removed by retention")
	}
	var count int
	db.conn.QueryRow("SELECT count(*) FROM ip_history").Scan(&count)
	if count != 0 {
		t.Fatal("old history survived")
	}
}

func TestDatabaseAndWALArePrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private.db")
	if err := os.WriteFile(path, nil, 0644); err != nil {
		t.Fatal(err)
	}
	db, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, suffix := range []string{"", "-wal", "-shm"} {
		info, err := os.Stat(path + suffix)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("%s permissions: %o", suffix, info.Mode().Perm())
		}
	}
}
