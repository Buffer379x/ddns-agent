package database

// ConfigSnapshot reads all exported configuration from a single SQLite snapshot.
// Password hashes and sessions deliberately remain outside portable exports.
func (db *DB) ConfigSnapshot() (map[string]string, []Record, []Webhook, error) {
	tx, err := db.conn.Begin()
	if err != nil {
		return nil, nil, nil, err
	}
	defer tx.Rollback()
	settings := map[string]string{}
	rows, err := tx.Query("SELECT key,value FROM settings")
	if err != nil {
		return nil, nil, nil, err
	}
	for rows.Next() {
		var key, value string
		if err = rows.Scan(&key, &value); err != nil {
			break
		}
		settings[key] = value
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, nil, nil, err
	}
	rows, err = tx.Query("SELECT " + recordColumns + " FROM records ORDER BY id")
	if err != nil {
		return nil, nil, nil, err
	}
	records := []Record{}
	for rows.Next() {
		var record *Record
		record, err = scanRecord(rows)
		if err != nil {
			break
		}
		records = append(records, *record)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, nil, nil, err
	}
	rows, err = tx.Query("SELECT id,name,type,url,enabled,events,created_at FROM webhooks ORDER BY id")
	if err != nil {
		return nil, nil, nil, err
	}
	hooks := []Webhook{}
	for rows.Next() {
		var hook Webhook
		err = rows.Scan(&hook.ID, &hook.Name, &hook.Type, &hook.URL, &hook.Enabled, &hook.Events, &hook.CreatedAt)
		if err != nil {
			break
		}
		hooks = append(hooks, hook)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, nil, nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, nil, nil, err
	}
	return settings, records, hooks, nil
}
