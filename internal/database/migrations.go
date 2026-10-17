package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (db *DB) upgrade() error {
	columns := map[string]map[string]string{
		"users":   {"must_change_password": "INTEGER NOT NULL DEFAULT 0"},
		"records": {"current_ipv4": "TEXT", "current_ipv6": "TEXT", "status_ipv4": "TEXT NOT NULL DEFAULT 'pending'", "status_ipv6": "TEXT NOT NULL DEFAULT 'pending'", "message_ipv4": "TEXT NOT NULL DEFAULT ''", "message_ipv6": "TEXT NOT NULL DEFAULT ''", "last_success": "DATETIME"},
	}
	for table, fields := range columns {
		rows, err := db.conn.Query("PRAGMA table_info(" + table + ")")
		if err != nil {
			return err
		}
		present := map[string]bool{}
		for rows.Next() {
			var id, notnull, pk int
			var name, typ string
			var def any
			if err = rows.Scan(&id, &name, &typ, &notnull, &def, &pk); err != nil {
				rows.Close()
				return err
			}
			present[name] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for name, typ := range fields {
			if !present[name] {
				if _, err = db.conn.Exec("ALTER TABLE " + table + " ADD COLUMN " + name + " " + typ); err != nil {
					return err
				}
			}
		}
	}
	_, err := db.conn.Exec(`
 CREATE TABLE IF NOT EXISTS sessions(id TEXT PRIMARY KEY,user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,expires_at INTEGER NOT NULL);
 CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);
 CREATE INDEX IF NOT EXISTS idx_sessions_expiry ON sessions(expires_at);
 UPDATE records SET ip_version='dual' WHERE ip_version='both';
 UPDATE records SET current_ipv4=current_ip,status_ipv4=status WHERE current_ipv4 IS NULL AND current_ip<>'' AND instr(current_ip,':')=0;
 UPDATE records SET current_ipv6=current_ip,status_ipv6=status WHERE current_ipv6 IS NULL AND instr(current_ip,':')>0;
 UPDATE records SET last_success=last_update WHERE last_success IS NULL AND status='success';
 UPDATE records SET status='pending' WHERE ip_version='dual' AND status='success' AND (status_ipv4<>'success' OR status_ipv6<>'success');
 `)
	return err
}

func guardAdmin(tx *sql.Tx, id int, nextRole string, deleting bool) error {
	var role string
	if err := tx.QueryRow("SELECT role FROM users WHERE id=?", id).Scan(&role); err != nil {
		return err
	}
	if role != "admin" || (!deleting && nextRole == "admin") {
		return nil
	}
	var count int
	if err := tx.QueryRow("SELECT COUNT(*) FROM users WHERE role='admin'").Scan(&count); err != nil {
		return err
	}
	if count < 2 {
		return errors.New("cannot remove the last administrator")
	}
	return nil
}

func (db *DB) RequirePasswordChange(id int) error {
	_, err := db.conn.Exec("UPDATE users SET must_change_password=1 WHERE id=?", id)
	return err
}
func (db *DB) SaveSession(id string, userID int, expires time.Time) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("DELETE FROM sessions WHERE expires_at<=?", time.Now().Unix()); err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM sessions WHERE user_id=? AND id IN (SELECT id FROM sessions WHERE user_id=? ORDER BY expires_at DESC LIMIT -1 OFFSET 19)", userID, userID); err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO sessions(id,user_id,expires_at) VALUES(?,?,?)", id, userID, expires.Unix()); err != nil {
		return err
	}
	return tx.Commit()
}
func (db *DB) SessionUser(session string) (*User, error) {
	var id int
	if err := db.conn.QueryRow("SELECT user_id FROM sessions WHERE id=? AND expires_at>?", session, time.Now().Unix()).Scan(&id); err != nil {
		return nil, err
	}
	return db.GetUserByID(id)
}
func (db *DB) RevokeSession(id string) error {
	_, err := db.conn.Exec("DELETE FROM sessions WHERE id=?", id)
	return err
}

const recordColumns = "id,provider,domain,owner,ip_version,current_ip,status,message,last_update,last_ban,provider_config,enabled,created_at,current_ipv4,current_ipv6,status_ipv4,status_ipv6,message_ipv4,message_ipv6,last_success"

type scanner interface{ Scan(...any) error }

func scanRecord(row scanner) (*Record, error) {
	r := &Record{}
	err := row.Scan(&r.ID, &r.Provider, &r.Domain, &r.Owner, &r.IPVersion, &r.CurrentIP, &r.Status, &r.Message, &r.LastUpdate, &r.LastBan, &r.ProviderConfig, &r.Enabled, &r.CreatedAt, &r.CurrentIPv4, &r.CurrentIPv6, &r.StatusIPv4, &r.StatusIPv6, &r.MessageIPv4, &r.MessageIPv6, &r.LastSuccess)
	return r, err
}
func (db *DB) records(where string) ([]Record, error) {
	rows, err := db.conn.Query("SELECT " + recordColumns + " FROM records" + where + " ORDER BY domain,owner,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := []Record{}
	for rows.Next() {
		r, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, *r)
	}
	return records, rows.Err()
}

// UpdateFamily preserves the last known address on failure and aggregates dual-stack state.
func (db *DB) UpdateFamily(id int, family, status, message, ip string) error {
	if family != "ipv4" && family != "ipv6" {
		return errors.New("invalid address family")
	}
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	suffix := "4"
	if family == "ipv6" {
		suffix = "6"
	}
	q := "UPDATE records SET status_ipv" + suffix + "=?,message_ipv" + suffix + "=?,last_update=?"
	args := []any{status, message, now}
	if status == "success" {
		q += ",current_ipv" + suffix + "=?,current_ip=?,last_success=?"
		args = append(args, ip, ip, now)
	}
	q += " WHERE id=?"
	args = append(args, id)
	if _, err = tx.Exec(q, args...); err != nil {
		return err
	}
	rec, err := scanRecord(tx.QueryRow("SELECT "+recordColumns+" FROM records WHERE id=?", id))
	if err != nil {
		return err
	}
	states := []string{rec.StatusIPv4}
	messages := []string{rec.MessageIPv4}
	if rec.IPVersion == "ipv6" {
		states = []string{rec.StatusIPv6}
		messages = []string{rec.MessageIPv6}
	} else if rec.IPVersion == "dual" {
		states = append(states, rec.StatusIPv6)
		messages = append(messages, rec.MessageIPv6)
	}
	aggregate := "success"
	for _, candidate := range []string{"pending", "updating", "error"} {
		for _, s := range states {
			if s == candidate {
				aggregate = candidate
			}
		}
	}
	if _, err = tx.Exec("UPDATE records SET status=?,message=? WHERE id=?", aggregate, strings.Join(messages, " · "), id); err != nil {
		return err
	}
	return tx.Commit()
}

func (db *DB) SetSettings(settings map[string]string) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for k, v := range settings {
		if _, err = tx.Exec("INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", k, v); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ApplyConfig is all-or-nothing, including replace, settings and webhook merges.
func (db *DB) ApplyConfig(settings map[string]string, records []Record, hooks []Webhook, mode string) error {
	if mode != "merge" && mode != "replace" {
		return errors.New("invalid import mode")
	}
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for k, v := range settings {
		if _, err = tx.Exec("INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", k, v); err != nil {
			return err
		}
	}
	if mode == "replace" {
		if _, err = tx.Exec("DELETE FROM records"); err != nil {
			return err
		}
	}
	for _, r := range records {
		if mode == "merge" {
			var n int
			if err = tx.QueryRow("SELECT COUNT(*) FROM records WHERE provider=? AND domain=? AND owner=? AND ip_version=?", r.Provider, r.Domain, r.Owner, r.IPVersion).Scan(&n); err != nil {
				return err
			}
			if n > 0 {
				continue
			}
		}
		if _, err = tx.Exec("INSERT INTO records(provider,domain,owner,ip_version,provider_config,enabled) VALUES(?,?,?,?,?,?)", r.Provider, r.Domain, r.Owner, r.IPVersion, r.ProviderConfig, r.Enabled); err != nil {
			return fmt.Errorf("record %s.%s: %w", r.Owner, r.Domain, err)
		}
	}
	for _, h := range hooks {
		if _, err = tx.Exec("INSERT INTO webhooks(name,type,url,enabled,events) VALUES(?,?,?,?,?)", h.Name, h.Type, h.URL, h.Enabled, h.Events); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (db *DB) Prune(days int) error {
	if days < 1 || days > 3650 {
		return errors.New("invalid retention")
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -days)
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, table := range []string{"logs", "ip_history"} {
		column := "created_at"
		if table == "ip_history" {
			column = "changed_at"
		}
		if _, err = tx.Exec("DELETE FROM "+table+" WHERE "+column+"<?", cutoff); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// QuiescedSnapshot holds the only connection and SQLite's writer lock throughout a raw snapshot.
func (db *DB) QuiescedSnapshot(copy func() error) error {
	conn, err := db.conn.Conn(context.Background())
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(context.Background(), "BEGIN EXCLUSIVE"); err != nil {
		return err
	}
	defer conn.ExecContext(context.Background(), "ROLLBACK")
	return copy()
}
