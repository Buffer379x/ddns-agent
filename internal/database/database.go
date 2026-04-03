package database

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type DB struct {
	conn *sql.DB
}

func New(dbPath string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, fmt.Errorf("creating data directory: %w", err)
	}

	conn, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_foreign_keys=on")
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	// Single writer; WAL mode handles concurrent readers without a pool.
	conn.SetMaxOpenConns(1)
	conn.SetMaxIdleConns(1)

	db := &DB{conn: conn}
	if err := db.migrate(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("running migrations: %w", err)
	}
	return db, nil
}

func (db *DB) Close() error { return db.conn.Close() }
func (db *DB) Ping() error  { return db.conn.Ping() }

// Vacuum creates a compacted copy of the database at dest using SQLite's
// VACUUM INTO statement. dest must not contain a single-quote character.
func (db *DB) Vacuum(dest string) error {
	for _, c := range dest {
		if c == '\'' {
			return fmt.Errorf("vacuum: destination path must not contain single quotes")
		}
	}
	_, err := db.conn.Exec("VACUUM INTO '" + dest + "'")
	return err
}

func (db *DB) migrate() error {
	_, err := db.conn.Exec(schema)
	return err
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
	ID           int       `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	Role         string    `json:"role"`
	CreatedAt    time.Time `json:"created_at"`
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
	err := db.conn.QueryRow("SELECT id, username, password_hash, role, created_at FROM users WHERE username = ?", username).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return u, nil
}

func (db *DB) GetUserByID(id int) (*User, error) {
	u := &User{}
	err := db.conn.QueryRow("SELECT id, username, password_hash, role, created_at FROM users WHERE id = ?", id).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return u, nil
}

func (db *DB) ListUsers() ([]User, error) {
	rows, err := db.conn.Query("SELECT id, username, role, created_at FROM users ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.Role, &u.CreatedAt); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (db *DB) UpdateUser(id int, username, passwordHash, role string) error {
	if passwordHash != "" {
		_, err := db.conn.Exec("UPDATE users SET username=?, password_hash=?, role=? WHERE id=?",
			username, passwordHash, role, id)
		return err
	}
	_, err := db.conn.Exec("UPDATE users SET username=?, role=? WHERE id=?", username, role, id)
	return err
}

func (db *DB) DeleteUser(id int) error {
	_, err := db.conn.Exec("DELETE FROM users WHERE id=?", id)
	return err
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
	r := &Record{}
	err := db.conn.QueryRow(
		`SELECT id, provider, domain, owner, ip_version, current_ip, status, message,
		        last_update, last_ban, provider_config, enabled, created_at
		 FROM records WHERE id = ?`, id).
		Scan(&r.ID, &r.Provider, &r.Domain, &r.Owner, &r.IPVersion, &r.CurrentIP,
			&r.Status, &r.Message, &r.LastUpdate, &r.LastBan, &r.ProviderConfig, &r.Enabled, &r.CreatedAt)
	if err != nil {
		return nil, err
	}
	return r, nil
}

func (db *DB) ListRecords() ([]Record, error) {
	rows, err := db.conn.Query(
		`SELECT id, provider, domain, owner, ip_version, current_ip, status, message,
		        last_update, last_ban, provider_config, enabled, created_at
		 FROM records ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records []Record
	for rows.Next() {
		var r Record
		if err := rows.Scan(&r.ID, &r.Provider, &r.Domain, &r.Owner, &r.IPVersion, &r.CurrentIP,
			&r.Status, &r.Message, &r.LastUpdate, &r.LastBan, &r.ProviderConfig, &r.Enabled, &r.CreatedAt); err != nil {
			return nil, err
		}
		records = append(records, r)
	}
	return records, rows.Err()
}

func (db *DB) ListEnabledRecords() ([]Record, error) {
	rows, err := db.conn.Query(
		`SELECT id, provider, domain, owner, ip_version, current_ip, status, message,
		        last_update, last_ban, provider_config, enabled, created_at
		 FROM records WHERE enabled = 1 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records []Record
	for rows.Next() {
		var r Record
		if err := rows.Scan(&r.ID, &r.Provider, &r.Domain, &r.Owner, &r.IPVersion, &r.CurrentIP,
			&r.Status, &r.Message, &r.LastUpdate, &r.LastBan, &r.ProviderConfig, &r.Enabled, &r.CreatedAt); err != nil {
			return nil, err
		}
		records = append(records, r)
	}
	return records, rows.Err()
}

func (db *DB) UpdateRecord(r *Record) error {
	_, err := db.conn.Exec(
		`UPDATE records SET provider=?, domain=?, owner=?, ip_version=?, provider_config=?, enabled=? WHERE id=?`,
		r.Provider, r.Domain, r.Owner, r.IPVersion, r.ProviderConfig, r.Enabled, r.ID)
	return err
}

func (db *DB) UpdateRecordStatus(id int, status, message, currentIP string) error {
	now := time.Now()
	_, err := db.conn.Exec(
		`UPDATE records SET status=?, message=?, current_ip=?, last_update=? WHERE id=?`,
		status, message, currentIP, now, id)
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

func (db *DB) ListLogs(level string, limit, offset int) ([]LogEntry, int, error) {
	var total int
	countQ := "SELECT COUNT(*) FROM logs"
	listQ := "SELECT id, level, source, message, created_at FROM logs"
	args := []any{}

	if level != "" {
		countQ += " WHERE level = ?"
		listQ += " WHERE level = ?"
		args = append(args, level)
	}

	if err := db.conn.QueryRow(countQ, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	listQ += " ORDER BY created_at DESC LIMIT ? OFFSET ?"
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
	ID        int       `json:"id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	URL       string    `json:"url"`
	Enabled   bool      `json:"enabled"`
	Events    string    `json:"events"`
	CreatedAt time.Time `json:"created_at"`
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
		"SELECT id, name, type, url, enabled, events, created_at FROM webhooks WHERE enabled=1 AND events LIKE ?",
		"%"+event+"%")
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
