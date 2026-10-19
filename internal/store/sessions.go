package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/corani/wren/internal/study"
)

func readSession(ctx context.Context, q querier, id int64) (study.Session, error) {
	var session study.Session
	var settings string
	var undoFloor int64
	err := q.QueryRowContext(ctx, `SELECT id,settings,status,revision,initial_size,last_term_id,undo_floor,notice
		FROM sessions WHERE id=?`, id).Scan(&session.ID, &settings, &session.Status, &session.Revision,
		&session.InitialSize, &session.LastTermID, &undoFloor, &session.Notice)
	if errors.Is(err, sql.ErrNoRows) {
		return session, study.ErrNotFound
	}
	if err != nil {
		return session, err
	}
	if err := json.Unmarshal([]byte(settings), &session.Settings); err != nil {
		return session, err
	}
	rows, err := q.QueryContext(ctx, `SELECT sc.card_id,c.term_id,sc.cleared FROM session_cards sc
		JOIN cards c ON c.id=sc.card_id WHERE session_id=? ORDER BY position`, id)
	if err != nil {
		return session, err
	}
	for rows.Next() {
		var item study.QueueItem
		if err := rows.Scan(&item.CardID, &item.TermID, &item.Cleared); err != nil {
			return session, errors.Join(err, rows.Close())
		}
		session.Queue = append(session.Queue, item)
		if !item.Cleared {
			session.Remaining++
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return session, err
	}
	session.Cleared = session.InitialSize - session.Remaining
	for _, item := range session.Queue {
		if !item.Cleared {
			card, err := loadCard(ctx, q, item.CardID)
			if err != nil {
				return session, err
			}
			session.Current = &card
			break
		}
	}
	err = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM review_events
		WHERE session_id=? AND undone_at IS NULL AND id>?)`, id, undoFloor).Scan(&session.CanUndo)
	if session.Status == "ended" {
		session.CanUndo = false
	}
	return session, err
}

func (s *Store) Session(ctx context.Context, id int64) (study.Session, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return study.Session{}, err
	}
	defer tx.Rollback() //nolint:errcheck // Read transaction is closed on return.
	session, err := readSession(ctx, tx, id)
	if err != nil {
		return session, err
	}
	return session, tx.Commit()
}

func (s *Store) Current(ctx context.Context) (*study.Session, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck // Read transaction is closed on return.
	var id int64
	err = tx.QueryRowContext(ctx, "SELECT id FROM sessions WHERE status IN ('active','paused')").Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	session, err := readSession(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	return &session, tx.Commit()
}

func (s *Store) mutate(ctx context.Context, id string, request any,
	fn func(*sql.Tx) (study.Session, error),
) (study.Session, error) {
	if len(id) < 8 || len(id) > 128 || strings.TrimSpace(id) != id {
		return study.Session{}, fmt.Errorf("%w: invalid mutation ID", study.ErrInvalid)
	}
	data, err := json.Marshal(request)
	if err != nil {
		return study.Session{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return study.Session{}, err
	}
	defer tx.Rollback() //nolint:errcheck // Commit closes successful mutations.
	var previous, response string
	err = tx.QueryRowContext(ctx, "SELECT request,response FROM mutations WHERE id=?", id).Scan(&previous, &response)
	if err == nil {
		if previous != string(data) {
			return study.Session{}, fmt.Errorf("%w: mutation ID reused with different input", study.ErrConflict)
		}
		var session study.Session
		err := json.Unmarshal([]byte(response), &session)
		return session, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return study.Session{}, err
	}
	session, err := fn(tx)
	if err != nil {
		return study.Session{}, err
	}
	encoded, err := json.Marshal(session)
	if err != nil {
		return study.Session{}, err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO mutations(id,request,response) VALUES(?,?,?)",
		id, string(data), string(encoded)); err != nil {
		return study.Session{}, err
	}
	return session, tx.Commit()
}

type startRequest struct {
	Action   string
	Settings study.Settings
}

func (s *Store) Start(ctx context.Context, settings study.Settings, mutationID string) (study.Session, error) {
	if err := settings.Validate(); err != nil {
		return study.Session{}, err
	}
	return s.mutate(ctx, mutationID, startRequest{"start", settings}, func(tx *sql.Tx) (study.Session, error) {
		var unfinished bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sessions WHERE status IN ('active','paused'))").
			Scan(&unfinished); err != nil {
			return study.Session{}, err
		}
		if unfinished {
			return study.Session{}, fmt.Errorf("%w: resume or end the unfinished session first", study.ErrConflict)
		}
		var lastTermID int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT last_term_id FROM sessions ORDER BY id DESC LIMIT 1),0)`).
			Scan(&lastTermID); err != nil {
			return study.Session{}, err
		}
		queue, err := s.selectCards(ctx, tx, settings, lastTermID)
		if err != nil {
			return study.Session{}, err
		}
		if len(queue) == 0 {
			return study.Session{}, fmt.Errorf("%w: no eligible cards; reviews may be scheduled for later", study.ErrInvalid)
		}
		raw, err := json.Marshal(settings)
		if err != nil {
			return study.Session{}, err
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO sessions(settings,status,initial_size,last_term_id,started_at)
			VALUES(?,'active',?,?,?)`, string(raw), len(queue), lastTermID, timestamp(s.now()))
		if err != nil {
			return study.Session{}, err
		}
		id, err := result.LastInsertId()
		if err != nil {
			return study.Session{}, err
		}
		for i, item := range queue {
			if _, err := tx.ExecContext(ctx, "INSERT INTO session_cards(session_id,card_id,position) VALUES(?,?,?)",
				id, item.CardID, i); err != nil {
				return study.Session{}, err
			}
		}
		if _, err := tx.ExecContext(ctx, "UPDATE settings SET value=? WHERE id=1", string(raw)); err != nil {
			return study.Session{}, err
		}
		return readSession(ctx, tx, id)
	})
}

func (s *Store) selectCards(ctx context.Context, tx *sql.Tx, settings study.Settings, lastTermID int64) ([]study.QueueItem, error) {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(settings.Directions)), ",")
	query := `SELECT c.id,c.term_id,(c.assessed_at IS NULL) FROM cards c JOIN terms t ON t.id=c.term_id
		WHERE t.active=1 AND t.available=1 AND c.direction IN (` + placeholders + `)`
	args := []any{}
	for _, direction := range settings.Directions {
		args = append(args, direction)
	}
	if settings.IncludeLower {
		query += " AND t.hsk_level_min<=?"
	} else {
		query += " AND t.hsk_level_min=?"
	}
	args = append(args, settings.Level)
	if settings.Mode == "assessment" {
		query += " AND c.assessed_at IS NULL ORDER BY RANDOM()"
	} else {
		query += ` AND (c.assessed_at IS NULL OR c.due<=?)
			ORDER BY (c.assessed_at IS NULL), CASE WHEN c.assessed_at IS NOT NULL THEN c.due END, RANDOM()`
		args = append(args, timestamp(s.now()))
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	type candidate struct {
		item  study.QueueItem
		isNew bool
	}
	var candidates []candidate
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.item.CardID, &item.item.TermID, &item.isNew); err != nil {
			return nil, errors.Join(err, rows.Close())
		}
		candidates = append(candidates, item)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	// Choose before limiting, but never advance a new card ahead of a due review.
	queue := make([]study.QueueItem, 0, min(len(candidates), settings.BatchSize))
	for len(candidates) > 0 && len(queue) < settings.BatchSize {
		pick := 0
		if candidates[0].item.TermID == lastTermID {
			for i, candidate := range candidates {
				if candidate.isNew != candidates[0].isNew {
					break
				}
				if candidate.item.TermID != lastTermID {
					pick = i
					break
				}
			}
		}
		item := candidates[pick].item
		queue = append(queue, item)
		lastTermID = item.TermID
		candidates = slices.Delete(candidates, pick, pick+1)
	}
	return queue, nil
}

type snapshot struct {
	Card    study.Card
	Session study.Session
	Queue   []study.QueueItem
}

func writeSession(ctx context.Context, tx *sql.Tx, session study.Session, now string) error {
	var completed any
	if session.Status == "complete" || session.Status == "ended" {
		completed = now
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET status=?,revision=?,last_term_id=?,completed_at=? WHERE id=?`,
		session.Status, session.Revision, session.LastTermID, completed, session.ID); err != nil {
		return err
	}
	for i, item := range session.Queue {
		if _, err := tx.ExecContext(ctx, "UPDATE session_cards SET position=?,cleared=? WHERE session_id=? AND card_id=?",
			i, item.Cleared, session.ID, item.CardID); err != nil {
			return err
		}
	}
	return nil
}

type actionRequest struct {
	Action   string
	Session  int64
	Mutation study.Mutation
}

func (s *Store) Act(ctx context.Context, id int64, action string, mutation study.Mutation) (study.Session, error) {
	if !slices.Contains([]string{"answers", "undo", "pause", "end"}, action) {
		return study.Session{}, study.ErrNotFound
	}
	return s.mutate(ctx, mutation.MutationID, actionRequest{action, id, mutation}, func(tx *sql.Tx) (study.Session, error) {
		session, err := readSession(ctx, tx, id)
		if err != nil {
			return session, err
		}
		var latest int64
		if err := tx.QueryRowContext(ctx, "SELECT MAX(id) FROM sessions").Scan(&latest); err != nil {
			return session, err
		}
		if session.Revision != mutation.Revision || latest != id || session.Status == "ended" {
			return session, study.ErrConflict
		}
		now := s.now()
		switch action {
		case "answers":
			if session.Current == nil || session.Current.ID != mutation.CardID {
				return session, study.ErrConflict
			}
			before := snapshot{Card: *session.Current, Session: session, Queue: slices.Clone(session.Queue)}
			card, kind, err := study.Answer(*session.Current, mutation.Right,
				session.Settings.Mode == "assessment", now)
			if err != nil {
				return session, err
			}
			if err := saveCard(ctx, tx, card); err != nil {
				return session, err
			}
			session.LastTermID = card.TermID
			for i, item := range session.Queue {
				if item.CardID == card.ID {
					if mutation.Right {
						session.Queue[i].Cleared = true
					} else {
						session.Queue = append(slices.Delete(session.Queue, i, i+1), item)
					}
					break
				}
			}
			study.SeparateHead(session.Queue, session.LastTermID)
			session.Status = "complete"
			for _, item := range session.Queue {
				if !item.Cleared {
					session.Status = "active"
					break
				}
			}
			session.Revision++
			if err := writeSession(ctx, tx, session, timestamp(now)); err != nil {
				return session, err
			}
			beforeJSON, err := json.Marshal(before)
			if err != nil {
				return session, err
			}
			afterJSON, err := json.Marshal(snapshot{Card: card, Session: session, Queue: session.Queue})
			if err != nil {
				return session, err
			}
			parameters, err := json.Marshal(study.Parameters())
			if err != nil {
				return session, err
			}
			var rating any
			if kind == "review" {
				rating = 1
				if mutation.Right {
					rating = 3
				}
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO review_events
				(session_id,card_id,occurred_at,kind,right_answer,rating,before_state,after_state,scheduler_config)
				VALUES(?,?,?,?,?,?,?,?,?)`, id, card.ID, timestamp(now), kind, mutation.Right, rating,
				string(beforeJSON), string(afterJSON), `{"library":"go-fsrs/v4@v4.0.0","parameters":`+string(parameters)+`}`)
			if err != nil {
				return session, err
			}
		case "undo":
			if !session.CanUndo {
				return session, fmt.Errorf("%w: nothing to undo", study.ErrInvalid)
			}
			var eventID int64
			var raw string
			if err := tx.QueryRowContext(ctx, `SELECT id,before_state FROM review_events
				WHERE session_id=? AND undone_at IS NULL ORDER BY id DESC LIMIT 1`, id).Scan(&eventID, &raw); err != nil {
				return session, err
			}
			var before snapshot
			if err := json.Unmarshal([]byte(raw), &before); err != nil {
				return session, err
			}
			if err := saveCard(ctx, tx, before.Card); err != nil {
				return session, err
			}
			before.Session.Queue = before.Queue
			before.Session.Revision = session.Revision + 1
			if err := writeSession(ctx, tx, before.Session, timestamp(now)); err != nil {
				return session, err
			}
			if _, err := tx.ExecContext(ctx, "UPDATE review_events SET undone_at=? WHERE id=?",
				timestamp(now), eventID); err != nil {
				return session, err
			}
		case "pause", "end":
			if session.Status == "complete" {
				return session, study.ErrConflict
			}
			session.Status = "paused"
			if session.Current != nil {
				session.LastTermID = session.Current.TermID
			}
			if action == "end" {
				session.Status = "ended"
			}
			session.Revision++
			if err := writeSession(ctx, tx, session, timestamp(now)); err != nil {
				return session, err
			}
		}
		return readSession(ctx, tx, id)
	})
}
