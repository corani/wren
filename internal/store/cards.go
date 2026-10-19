package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/corani/wren/internal/study"
)

type querier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

const cardColumns = `c.id,c.term_id,c.direction,t.simplified,t.pinyin,t.english,t.hsk_level_min,
	c.assessed_at,c.initial_known,c.due,c.stability,c.difficulty,c.scheduled_days,c.reps,
	c.lapses,c.state,c.last_review,c.remaining_steps`

func loadCard(ctx context.Context, q querier, id int64) (study.Card, error) {
	var card study.Card
	var due string
	var assessed, last sql.NullString
	var known sql.NullBool
	err := q.QueryRowContext(ctx, "SELECT "+cardColumns+" FROM cards c JOIN terms t ON t.id=c.term_id WHERE c.id=?", id).
		Scan(&card.ID, &card.TermID, &card.Direction, &card.Simplified, &card.Pinyin, &card.English,
			&card.Level, &assessed, &known, &due, &card.FSRS.Stability, &card.FSRS.Difficulty,
			&card.FSRS.ScheduledDays, &card.FSRS.Reps, &card.FSRS.Lapses, &card.FSRS.State,
			&last, &card.FSRS.RemainingSteps)
	if errors.Is(err, sql.ErrNoRows) {
		return card, study.ErrNotFound
	}
	if err != nil {
		return card, err
	}
	card.FSRS.Due, err = parseTime(due)
	if err != nil {
		return card, fmt.Errorf("store: card due: %w", err)
	}
	if assessed.Valid {
		t, err := parseTime(assessed.String)
		if err != nil {
			return card, err
		}
		card.AssessedAt = &t
	}
	if last.Valid {
		card.FSRS.LastReview, err = parseTime(last.String)
		if err != nil {
			return card, err
		}
	}
	if known.Valid {
		card.InitialKnown = &known.Bool
	}
	return card, nil
}

func saveCard(ctx context.Context, tx *sql.Tx, card study.Card) error {
	var assessed, last any
	if card.AssessedAt != nil {
		assessed = timestamp(*card.AssessedAt)
	}
	if !card.FSRS.LastReview.IsZero() {
		last = timestamp(card.FSRS.LastReview)
	}
	_, err := tx.ExecContext(ctx, `UPDATE cards SET assessed_at=?,initial_known=?,due=?,
		stability=?,difficulty=?,scheduled_days=?,reps=?,lapses=?,state=?,last_review=?,remaining_steps=?
		WHERE id=?`, assessed, card.InitialKnown, timestamp(card.FSRS.Due), card.FSRS.Stability,
		card.FSRS.Difficulty, card.FSRS.ScheduledDays, card.FSRS.Reps, card.FSRS.Lapses,
		card.FSRS.State, last, card.FSRS.RemainingSteps, card.ID)
	return err
}
