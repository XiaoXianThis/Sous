package store

import (
	"context"
	"database/sql"
	"net/url"
	"path/filepath"
	"runtime"
)

// InspectWorkspace reads only the settings needed to classify a stopped Hub.
// Empty means no application tables or views (or an unused zero schema marker
// from an interrupted initialization). It never opens a writer or migrates.
func InspectWorkspace(ctx context.Context, file string, keys ...string) (settings map[string]string, empty bool, err error) {
	abs, err := filepath.Abs(file)
	if err != nil {
		return nil, false, err
	}
	name := filepath.ToSlash(abs)
	if runtime.GOOS == "windows" {
		name = "/" + name
	}
	uri := url.URL{Scheme: "file", Path: name, RawQuery: "mode=ro&_pragma=busy_timeout(2000)"}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, false, err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	var tables, versions, hasSettings int
	err = tx.QueryRowContext(ctx, `SELECT count(*),count(*) FILTER(WHERE type='table' AND name='schema_version'),count(*) FILTER(WHERE type='table' AND name='settings')
 FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%'`).Scan(&tables, &versions, &hasSettings)
	if err != nil {
		return nil, false, err
	}
	settings = map[string]string{}
	empty = tables == 0
	if tables == 1 && versions == 1 {
		var count int
		var v sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT count(*),min(version) FROM schema_version`).Scan(&count, &v); err != nil {
			return nil, false, err
		}
		empty = count == 0 || count == 1 && v.Valid && v.Int64 == 0
	}
	if hasSettings == 0 {
		return settings, empty, nil
	}
	for _, key := range keys {
		var value string
		e := tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, key).Scan(&value)
		if e == nil {
			settings[key] = value
		} else if e != sql.ErrNoRows {
			return nil, false, e
		}
	}
	return settings, false, nil
}
