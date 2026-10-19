package store

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/corani/wren/internal/study"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Store struct {
	db  *sql.DB
	now func() time.Time
}

func Open(ctx context.Context, path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("store: create directory: %w", err)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("store: resolve path: %w", err)
	}
	dsn := url.URL{Scheme: "file", Path: absolute}
	dsn.RawQuery = "_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	s := &Store{db: db, now: func() time.Time { return time.Now().UTC() }}
	if err := s.initialize(ctx); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) initialize(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `
		PRAGMA journal_mode=WAL;
		PRAGMA foreign_keys=ON;
		PRAGMA busy_timeout=5000;`); err != nil {
		return fmt.Errorf("store: configure: %w", err)
	}
	paths, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return fmt.Errorf("store: enumerate migrations: %w", err)
	}
	var version int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("store: read schema version: %w", err)
	}
	if version > len(paths) {
		return fmt.Errorf("store: database schema %d is newer than this binary", version)
	}
	for i := version; i < len(paths); i++ {
		data, err := migrations.ReadFile(paths[i])
		if err != nil {
			return fmt.Errorf("store: read migration: %w", err)
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, string(data))
		if err == nil {
			_, err = tx.ExecContext(ctx, "INSERT INTO schema_migrations(version) VALUES (?)", i+1)
		}
		if err == nil {
			_, err = tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version=%d", i+1))
		}
		if err != nil {
			return errors.Join(fmt.Errorf("store: migration %s: %w", paths[i], err), tx.Rollback())
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("store: commit migration: %w", err)
		}
	}
	defaults, err := json.Marshal(study.Defaults())
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "INSERT INTO settings(id,value) VALUES(1,?) ON CONFLICT DO NOTHING", string(defaults))
	return err
}

// Fixed-width UTC timestamps sort correctly in SQLite, including fractional seconds.
func timestamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000000000Z") }

func parseTime(value string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, value)
}

func (s *Store) Settings(ctx context.Context) (study.Settings, error) {
	var raw string
	if err := s.db.QueryRowContext(ctx, "SELECT value FROM settings WHERE id=1").Scan(&raw); err != nil {
		return study.Settings{}, fmt.Errorf("store: read settings: %w", err)
	}
	var settings study.Settings
	err := json.Unmarshal([]byte(raw), &settings)
	return settings, err
}

func (s *Store) SaveSettings(ctx context.Context, settings study.Settings) error {
	if err := settings.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "UPDATE settings SET value=? WHERE id=1", string(data))
	return err
}

func (s *Store) Levels(ctx context.Context) ([]study.Level, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT hsk_level_min, COUNT(*), SUM(available)
		FROM terms WHERE active=1 GROUP BY hsk_level_min ORDER BY hsk_level_min`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // Read errors are reported by rows.Err.
	levels := []study.Level{}
	for rows.Next() {
		var level study.Level
		if err := rows.Scan(&level.Level, &level.Total, &level.Available); err != nil {
			return nil, err
		}
		level.Label = fmt.Sprintf("HSK %d", level.Level)
		if level.Level == 7 {
			level.Label = "HSK 7-9"
		}
		levels = append(levels, level)
	}
	return levels, rows.Err()
}
