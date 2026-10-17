package database

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type DB struct {
	conn *sql.DB
	path string
}

func New(dbPath string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, fmt.Errorf("creating data directory: %w", err)
	}

	// SQLite inherits the main file's permissions for newly created WAL/SHM files.
	// Secure the main file before opening SQLite, including existing 1.0.x volumes.
	file, err := os.OpenFile(dbPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("preparing database file: %w", err)
	}
	err = file.Chmod(0600)
	closeErr := file.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err = os.Chmod(dbPath+suffix, 0600); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("securing database companion: %w", err)
		}
	}

	conn, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	// Single writer; WAL mode handles concurrent readers without a pool.
	conn.SetMaxOpenConns(1)
	conn.SetMaxIdleConns(1)

	db := &DB{conn: conn, path: dbPath}
	if err := db.migrate(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("running migrations: %w", err)
	}
	if err := os.Chmod(dbPath, 0600); err != nil {
		conn.Close()
		return nil, err
	}
	return db, nil
}

func (db *DB) Close() error { return db.conn.Close() }
func (db *DB) Ping() error  { return db.conn.Ping() }

// Path returns the filesystem path of the database file.
func (db *DB) Path() string { return db.path }

// Vacuum creates a compacted copy of the database at dest using SQLite's
// VACUUM INTO statement with a parameterized destination.
func (db *DB) Vacuum(dest string) error {
	_, err := db.conn.Exec("VACUUM INTO ?", dest)
	if err != nil {
		return err
	}
	return os.Chmod(dest, 0600)
}

func (db *DB) migrate() error {
	if _, err := db.conn.Exec(schema); err != nil {
		return err
	}
	return db.upgrade()
}

// CheckIntegrity runs SQLite's built-in integrity checks and returns a list
// of problem descriptions. An empty slice means the database is healthy.
func (db *DB) CheckIntegrity() ([]string, error) {
	var issues []string

	// Structural integrity check.
	rows, err := db.conn.Query("PRAGMA integrity_check")
	if err != nil {
		return nil, fmt.Errorf("integrity_check: %w", err)
	}
	for rows.Next() {
		var msg string
		if err := rows.Scan(&msg); err != nil {
			rows.Close()
			return nil, err
		}
		if msg != "ok" {
			issues = append(issues, msg)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	// Foreign-key consistency check.
	fkRows, err := db.conn.Query("PRAGMA foreign_key_check")
	if err != nil {
		return nil, fmt.Errorf("foreign_key_check: %w", err)
	}
	defer fkRows.Close()
	cols, _ := fkRows.Columns()
	for fkRows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := fkRows.Scan(ptrs...); err != nil {
			return nil, err
		}
		issues = append(issues, fmt.Sprintf("foreign_key_violation: table=%v rowid=%v parent=%v fkid=%v", vals[0], vals[1], vals[2], vals[3]))
	}
	if err := fkRows.Err(); err != nil {
		return nil, err
	}

	return issues, nil
}

// Repair re-runs the schema migration (all CREATE … IF NOT EXISTS / CREATE INDEX IF NOT EXISTS
// statements) so that any missing tables or indexes are recreated. Existing data
// is untouched. Call CheckIntegrity first to decide whether a repair is needed.
func (db *DB) Repair() error {
	return db.migrate()
}

const schema = `
CREATE TABLE IF NOT EXISTS users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    username TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    role TEXT NOT NULL DEFAULT 'admin',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS records (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    provider TEXT NOT NULL,
    domain TEXT NOT NULL,
    owner TEXT NOT NULL DEFAULT '@',
    ip_version TEXT NOT NULL DEFAULT 'ipv4',
    current_ip TEXT,
    status TEXT NOT NULL DEFAULT 'pending',
    message TEXT,
    last_update DATETIME,
    last_ban DATETIME,
    provider_config TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS ip_history (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    record_id INTEGER NOT NULL REFERENCES records(id) ON DELETE CASCADE,
    ip TEXT NOT NULL,
    changed_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    level TEXT NOT NULL,
    source TEXT,
    message TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS webhooks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    type TEXT NOT NULL,
    url TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    events TEXT NOT NULL DEFAULT 'ip_change,error',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_logs_created_at ON logs(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_logs_level ON logs(level);
CREATE INDEX IF NOT EXISTS idx_ip_history_record ON ip_history(record_id);
CREATE INDEX IF NOT EXISTS idx_records_status ON records(status);
`

// --- User Queries ---

type User struct {
	ID                 int       `json:"id"`
	Username           string    `json:"username"`
	PasswordHash       string    `json:"-"`
	MustChangePassword bool      `json:"must_change_password"`
	Role               string    `json:"role"`
	CreatedAt          time.Time `json:"created_at"`
}

func (db *DB) UserCount() (int, error) {
	var count int
	err := db.conn.QueryRow("SELECT COUNT(*) FROM users").Scan(&count)
	return count, err
}

func (db *DB) CreateUser(username, passwordHash, role string) (*User, error) {
	res, err := db.conn.Exec("INSERT INTO users (username, password_hash, role) VALUES (?, ?, ?)",
		username, passwordHash, role)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &User{ID: int(id), Username: username, Role: role, CreatedAt: time.Now()}, nil
}

func (db *DB) GetUserByUsername(username string) (*User, error) {
	u := &User{}
	err := db.conn.QueryRow("SELECT id, username, password_hash, role, created_at, must_change_password FROM users WHERE username = ?", username).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.CreatedAt, &u.MustChangePassword)
	if err != nil {
		return nil, err
	}
	return u, nil
}

func (db *DB) GetUserByID(id int) (*User, error) {
	u := &User{}
	err := db.conn.QueryRow("SELECT id, username, password_hash, role, created_at, must_change_password FROM users WHERE id = ?", id).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.CreatedAt, &u.MustChangePassword)
	if err != nil {
		return nil, err
	}
	return u, nil
}

func (db *DB) ListUsers() ([]User, error) {
	rows, err := db.conn.Query("SELECT id, username, role, created_at, must_change_password FROM users ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.Role, &u.CreatedAt, &u.MustChangePassword); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (db *DB) UpdateUser(id int, username, passwordHash, role string) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = guardAdmin(tx, id, role, false); err != nil {
		return err
	}
	if passwordHash != "" {
		_, err = tx.Exec("UPDATE users SET username=?,password_hash=?,role=?,must_change_password=0 WHERE id=?", username, passwordHash, role, id)
	} else {
		_, err = tx.Exec("UPDATE users SET username=?,role=? WHERE id=?", username, role, id)
	}
	if err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM sessions WHERE user_id=?", id); err != nil {
		return err
	}
	return tx.Commit()
}

func (db *DB) DeleteUser(id int) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = guardAdmin(tx, id, "", true); err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM users WHERE id=?", id); err != nil {
		return err
	}
	return tx.Commit()
}

// --- Record Queries ---

type Record struct {
	ID             int        `json:"id"`
	Provider       string     `json:"provider"`
	Domain         string     `json:"domain"`
	Owner          string     `json:"owner"`
	IPVersion      string     `json:"ip_version"`
	CurrentIP      *string    `json:"current_ip"`
	Status         string     `json:"status"`
	Message        *string    `json:"message"`
	LastUpdate     *time.Time `json:"last_update"`
	LastBan        *time.Time `json:"last_ban"`
	ProviderConfig string     `json:"provider_config"`
	Enabled        bool       `json:"enabled"`
	CreatedAt      time.Time  `json:"created_at"`
	CurrentIPv4    *string    `json:"current_ipv4"`
	CurrentIPv6    *string    `json:"current_ipv6"`
	StatusIPv4     string     `json:"status_ipv4"`
	StatusIPv6     string     `json:"status_ipv6"`
	MessageIPv4    string     `json:"message_ipv4"`
	MessageIPv6    string     `json:"message_ipv6"`
	LastSuccess    *time.Time `json:"last_success"`
	HasCredentials bool       `json:"has_credentials"`
}

func (db *DB) CreateRecord(r *Record) (int64, error) {
	res, err := db.conn.Exec(
		`INSERT INTO records (provider, domain, owner, ip_version, provider_config, enabled)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		r.Provider, r.Domain, r.Owner, r.IPVersion, r.ProviderConfig, r.Enabled)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (db *DB) GetRecord(id int) (*Record, error) {
	return scanRecord(db.conn.QueryRow("SELECT "+recordColumns+" FROM records WHERE id=?", id))
}

func (db *DB) ListRecords() ([]Record, error) { return db.records("") }

func (db *DB) ListEnabledRecords() ([]Record, error) { return db.records(" WHERE enabled=1") }

func (db *DB) UpdateRecord(r *Record) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var changed bool
	err = tx.QueryRow("SELECT provider<>? OR domain<>? OR owner<>? OR ip_version<>? OR provider_config<>? FROM records WHERE id=?", r.Provider, r.Domain, r.Owner, r.IPVersion, r.ProviderConfig, r.ID).Scan(&changed)
	if err != nil {
		return err
	}
	if changed {
		if _, err = tx.Exec("UPDATE records SET status='pending',status_ipv4='pending',status_ipv6='pending',message='',message_ipv4='',message_ipv6='',last_ban=NULL WHERE id=?", r.ID); err != nil {
			return err
		}
	}
	_, err = tx.Exec("UPDATE records SET provider=?,domain=?,owner=?,ip_version=?,provider_config=?,enabled=? WHERE id=?", r.Provider, r.Domain, r.Owner, r.IPVersion, r.ProviderConfig, r.Enabled, r.ID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (db *DB) UpdateRecordStatus(id int, status, message, currentIP string) error {
	_, err := db.conn.Exec("UPDATE records SET status=?,message=?,current_ip=CASE WHEN ?='' THEN current_ip ELSE ? END,last_update=? WHERE id=?", status, message, currentIP, currentIP, time.Now(), id)
	return err
}

func (db *DB) UpdateRecordBan(id int, banTime *time.Time) error {
	_, err := db.conn.Exec("UPDATE records SET last_ban=? WHERE id=?", banTime, id)
	return err
}

func (db *DB) DeleteRecord(id int) error {
	_, err := db.conn.Exec("DELETE FROM records WHERE id=?", id)
	return err
}

// DeleteAllRecords removes every hostname record (ip_history rows are removed via ON DELETE CASCADE).
func (db *DB) DeleteAllRecords() error {
	_, err := db.conn.Exec("DELETE FROM records")
	return err
}

// RecordExists reports whether a record with the same provider, domain and owner already exists.
func (db *DB) RecordExists(provider, domain, owner string) (bool, error) {
	var n int
	err := db.conn.QueryRow(
		`SELECT COUNT(*) FROM records WHERE provider = ? AND domain = ? AND owner = ?`,
		provider, domain, owner).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// RecordCounts returns aggregate counts in a single round-trip using
// SQLite's conditional aggregation (FILTER clause, supported since 3.23).
func (db *DB) RecordCounts() (total, activeEnabled, successful, errors int, err error) {
	err = db.conn.QueryRow(`
		SELECT
		  COUNT(*),
		  COUNT(*) FILTER (WHERE enabled = 1),
		  COUNT(*) FILTER (WHERE enabled = 1 AND status = 'success'),
		  COUNT(*) FILTER (WHERE enabled = 1 AND status = 'error')
		FROM records`).Scan(&total, &activeEnabled, &successful, &errors)
	return
}

// --- IP History ---

func (db *DB) InsertIPHistory(recordID int, ip string) error {
	_, err := db.conn.Exec("INSERT INTO ip_history (record_id, ip) VALUES (?, ?)", recordID, ip)
	return err
}

func (db *DB) GetIPHistory(recordID, limit int) ([]struct {
	IP        string    `json:"ip"`
	ChangedAt time.Time `json:"changed_at"`
}, error) {
	rows, err := db.conn.Query(
		"SELECT ip, changed_at FROM ip_history WHERE record_id=? ORDER BY changed_at DESC LIMIT ?",
		recordID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var history []struct {
		IP        string    `json:"ip"`
		ChangedAt time.Time `json:"changed_at"`
	}
	for rows.Next() {
		var h struct {
			IP        string    `json:"ip"`
			ChangedAt time.Time `json:"changed_at"`
		}
		if err := rows.Scan(&h.IP, &h.ChangedAt); err != nil {
			return nil, err
		}
		history = append(history, h)
	}
	return history, rows.Err()
}

// --- Logs ---

func (db *DB) InsertLog(level, source, message string) error {
	_, err := db.conn.Exec("INSERT INTO logs (level, source, message) VALUES (?, ?, ?)",
		level, source, message)
	return err
}

func (db *DB) ListLogs(level string, limit, offset int, search ...string) ([]LogEntry, int, error) {
	if limit < 1 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	if offset < 0 {
		offset = 0
	}
	var total int
	countQ := "SELECT COUNT(*) FROM logs"
	listQ := "SELECT id, level, source, message, created_at FROM logs"
	args := []any{}

	if level != "" {
		countQ += " WHERE level = ?"
		listQ += " WHERE level = ?"
		args = append(args, level)
	}

	if len(search) > 0 && strings.TrimSpace(search[0]) != "" {
		sep := " WHERE "
		if level != "" {
			sep = " AND "
		}
		clause := sep + "(instr(lower(message),lower(?))>0 OR instr(lower(COALESCE(source,'')),lower(?))>0)"
		countQ += clause
		listQ += clause
		args = append(args, search[0], search[0])
	}
	if err := db.conn.QueryRow(countQ, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	listQ += " ORDER BY created_at DESC,id DESC LIMIT ? OFFSET ?"
	listArgs := append(args, limit, offset)

	rows, err := db.conn.Query(listQ, listArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var logs []LogEntry
	for rows.Next() {
		var l LogEntry
		if err := rows.Scan(&l.ID, &l.Level, &l.Source, &l.Message, &l.CreatedAt); err != nil {
			return nil, 0, err
		}
		logs = append(logs, l)
	}
	return logs, total, rows.Err()
}

type LogEntry struct {
	ID        int       `json:"id"`
	Level     string    `json:"level"`
	Source    *string   `json:"source"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}

func (db *DB) DeleteLogs() error {
	_, err := db.conn.Exec("DELETE FROM logs")
	return err
}

// --- Settings ---

func (db *DB) GetSetting(key string) (string, error) {
	var value string
	err := db.conn.QueryRow("SELECT value FROM settings WHERE key=?", key).Scan(&value)
	return value, err
}

func (db *DB) SetSetting(key, value string) error {
	_, err := db.conn.Exec(
		"INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value",
		key, value)
	return err
}

func (db *DB) AllSettings() (map[string]string, error) {
	rows, err := db.conn.Query("SELECT key, value FROM settings")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		m[k] = v
	}
	return m, rows.Err()
}

// --- Webhooks ---

type Webhook struct {
	ID         int       `json:"id"`
	Name       string    `json:"name"`
	Type       string    `json:"type"`
	URL        string    `json:"url"`
	Enabled    bool      `json:"enabled"`
	Events     string    `json:"events"`
	CreatedAt  time.Time `json:"created_at"`
	DisplayURL string    `json:"display_url,omitempty"`
	HasSecret  bool      `json:"has_secret,omitempty"`
}

func (db *DB) CreateWebhook(w *Webhook) (int64, error) {
	res, err := db.conn.Exec(
		"INSERT INTO webhooks (name, type, url, enabled, events) VALUES (?, ?, ?, ?, ?)",
		w.Name, w.Type, w.URL, w.Enabled, w.Events)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (db *DB) ListWebhooks() ([]Webhook, error) {
	rows, err := db.conn.Query("SELECT id, name, type, url, enabled, events, created_at FROM webhooks ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var hooks []Webhook
	for rows.Next() {
		var w Webhook
		if err := rows.Scan(&w.ID, &w.Name, &w.Type, &w.URL, &w.Enabled, &w.Events, &w.CreatedAt); err != nil {
			return nil, err
		}
		hooks = append(hooks, w)
	}
	return hooks, rows.Err()
}

func (db *DB) GetWebhook(id int) (*Webhook, error) {
	w := &Webhook{}
	err := db.conn.QueryRow("SELECT id, name, type, url, enabled, events, created_at FROM webhooks WHERE id=?", id).
		Scan(&w.ID, &w.Name, &w.Type, &w.URL, &w.Enabled, &w.Events, &w.CreatedAt)
	return w, err
}

func (db *DB) UpdateWebhook(w *Webhook) error {
	_, err := db.conn.Exec("UPDATE webhooks SET name=?, type=?, url=?, enabled=?, events=? WHERE id=?",
		w.Name, w.Type, w.URL, w.Enabled, w.Events, w.ID)
	return err
}

func (db *DB) DeleteWebhook(id int) error {
	_, err := db.conn.Exec("DELETE FROM webhooks WHERE id=?", id)
	return err
}

func (db *DB) EnabledWebhooksForEvent(event string) ([]Webhook, error) {
	rows, err := db.conn.Query(
		"SELECT id, name, type, url, enabled, events, created_at FROM webhooks WHERE enabled=1 AND instr(','||events||',',?)>0",
		","+event+",")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var hooks []Webhook
	for rows.Next() {
		var w Webhook
		if err := rows.Scan(&w.ID, &w.Name, &w.Type, &w.URL, &w.Enabled, &w.Events, &w.CreatedAt); err != nil {
			return nil, err
		}
		hooks = append(hooks, w)
	}
	return hooks, rows.Err()
}

// WebhookExists reports whether a webhook with the same name, type and URL already exists.
func (db *DB) WebhookExists(name, webhookType, url string) (bool, error) {
	var n int
	err := db.conn.QueryRow(
		`SELECT COUNT(*) FROM webhooks WHERE name = ? AND type = ? AND url = ?`,
		name, webhookType, url).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
