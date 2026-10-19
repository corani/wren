package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"

	"github.com/corani/wren/internal/vocabulary"
)

type SyncResult struct {
	ChangedFiles int
	Terms        int
	Unavailable  int
}

func (s *Store) Sync(ctx context.Context, fsys fs.FS) (SyncResult, error) {
	sources, err := vocabulary.Load(fsys)
	if err != nil {
		return SyncResult{}, err
	}
	var result SyncResult
	for _, source := range sources {
		result.Terms += len(source.Terms)
		for _, term := range source.Terms {
			if !term.Available() {
				result.Unavailable++
			}
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback() //nolint:errcheck // A committed transaction is already closed.
	rows, err := tx.QueryContext(ctx, "SELECT path,sha256 FROM data_imports WHERE present=1")
	if err != nil {
		return result, err
	}
	old := map[string]string{}
	for rows.Next() {
		var path, hash string
		if err := rows.Scan(&path, &hash); err != nil {
			return result, errors.Join(err, rows.Close())
		}
		old[path] = hash
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return result, err
	}
	present := map[string]bool{}
	changed := map[string]bool{}
	for _, source := range sources {
		present[source.Path] = true
		if old[source.Path] != source.Hash {
			changed[source.Path] = true
		}
	}
	for path := range old {
		if !present[path] {
			changed[path] = true
		}
	}
	result.ChangedFiles = len(changed)
	if len(changed) == 0 {
		return result, tx.Commit()
	}
	// Deactivate first, then upsert all changed sources so cross-file moves survive.
	for path := range changed {
		if _, err := tx.ExecContext(ctx, "UPDATE terms SET active=0 WHERE source_path=?", path); err != nil {
			return result, err
		}
		if !present[path] {
			if _, err := tx.ExecContext(ctx, "UPDATE data_imports SET present=0 WHERE path=?", path); err != nil {
				return result, err
			}
		}
	}
	now := timestamp(s.now())
	for _, source := range sources {
		if !changed[source.Path] {
			continue
		}
		metadata := string(source.Metadata)
		if len(source.Metadata) == 0 {
			metadata = "{}"
		}
		if !json.Valid([]byte(metadata)) {
			return result, fmt.Errorf("store: invalid metadata in %s", source.Path)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO data_imports(path,sha256,metadata,imported_at,present)
			VALUES(?,?,?,?,1) ON CONFLICT(path) DO UPDATE SET sha256=excluded.sha256,
			metadata=excluded.metadata,imported_at=excluded.imported_at,present=1`,
			source.Path, source.Hash, metadata, now); err != nil {
			return result, err
		}
		for _, term := range source.Terms {
			if _, err := tx.ExecContext(ctx, `INSERT INTO terms
				(simplified,traditional,pinyin,english,hsk_level_min,hsk_level_max,source_path,active,available)
				VALUES(?,?,?,?,?,?,?,1,?)
				ON CONFLICT(simplified) DO UPDATE SET traditional=excluded.traditional,
				pinyin=excluded.pinyin,english=excluded.english,hsk_level_min=excluded.hsk_level_min,
				hsk_level_max=excluded.hsk_level_max,source_path=excluded.source_path,active=1,available=excluded.available`,
				term.Simplified, term.Traditional, term.Pinyin, term.English, source.Min, source.Max,
				source.Path, term.Available()); err != nil {
				return result, fmt.Errorf("store: sync %s: %w", source.Path, err)
			}
		}
	}
	for _, direction := range []string{"read", "write", "pinyin"} {
		if _, err := tx.ExecContext(ctx, `INSERT INTO cards(term_id,direction,due)
			SELECT id,?,? FROM terms WHERE active=1 ON CONFLICT(term_id,direction) DO NOTHING`,
			direction, now); err != nil {
			return result, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET
		notice='Vocabulary changed: unavailable cards were removed; earlier answers cannot be undone.',
		revision=revision+1, undo_floor=(SELECT COALESCE(MAX(id),0) FROM review_events)
		WHERE id IN (
			SELECT sc.session_id FROM session_cards sc
			JOIN cards c ON c.id=sc.card_id JOIN terms t ON t.id=c.term_id
			WHERE t.active=0 OR t.available=0)`); err != nil {
		return result, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE session_cards SET cleared=1 WHERE card_id IN
		(SELECT c.id FROM cards c JOIN terms t ON t.id=c.term_id WHERE t.active=0 OR t.available=0)`); err != nil {
		return result, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET status='complete',completed_at=?
		WHERE status IN ('active','paused') AND NOT EXISTS (
			SELECT 1 FROM session_cards WHERE session_id=sessions.id AND cleared=0)`, now); err != nil {
		return result, err
	}
	return result, tx.Commit()
}
