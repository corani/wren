package store

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	fsrs "github.com/open-spaced-repetition/go-fsrs/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/corani/wren/internal/study"
	"github.com/corani/wren/internal/vocabulary"
)

func testSource(t *testing.T, label string, words ...string) []byte {
	t.Helper()
	source := vocabulary.Source{Label: label, TotalTerms: len(words), Terms: []vocabulary.Term{}}
	for _, word := range words {
		source.Terms = append(source.Terms, vocabulary.Term{Simplified: word, Pinyin: word + " pinyin", English: word + " meaning"})
	}
	data, err := json.Marshal(source)
	require.NoError(t, err)
	return data
}

func testStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wren.db")
	st, err := Open(t.Context(), path)
	require.NoError(t, err)
	st.now = func() time.Time { return time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { require.NoError(t, st.Close()) })
	_, err = st.Sync(t.Context(), fstest.MapFS{
		"data/one.json": {Data: testSource(t, "HSK 1", "one", "two", "three")},
		"data/two.json": {Data: testSource(t, "HSK 2", "four")},
	})
	require.NoError(t, err)
	return st, path
}

func startSession(t *testing.T, st *Store, mode string, size int) study.Session {
	t.Helper()
	settings := study.Defaults()
	settings.Mode, settings.BatchSize = mode, size
	settings.Directions = []string{"read"}
	session, err := st.Start(t.Context(), settings, fmt.Sprintf("start-%d-%s-%d", time.Now().UnixNano(), mode, size))
	require.NoError(t, err)
	return session
}

func answer(t *testing.T, st *Store, session study.Session, right bool) study.Session {
	t.Helper()
	next, err := st.Act(t.Context(), session.ID, "answers", study.Mutation{
		MutationID: fmt.Sprintf("answer-%d-%d", session.ID, session.Revision),
		Revision:   session.Revision, CardID: session.Current.ID, Right: right,
	})
	require.NoError(t, err)
	return next
}

func TestSessionWorkflows(t *testing.T) {
	for _, tt := range []struct {
		name string
		run  func(*testing.T, *Store, study.Session)
	}{
		{"wrong moves to bottom and retries exceed batch", func(t *testing.T, st *Store, session study.Session) {
			first := session.Current.ID
			session = answer(t, st, session, false)
			assert.Equal(t, 2, session.Remaining)
			assert.NotEqual(t, first, session.Current.ID)
			session = answer(t, st, session, true)
			require.Equal(t, first, session.Current.ID)
			session = answer(t, st, session, true)
			assert.Equal(t, "complete", session.Status)
			assert.Equal(t, 2, session.Cleared)
			var events int
			require.NoError(t, st.db.QueryRow("SELECT COUNT(*) FROM review_events").Scan(&events))
			assert.Equal(t, 3, events)
		}},
		{"undo restores wrong card schedule and queue", func(t *testing.T, st *Store, session study.Session) {
			before := *session.Current
			session = answer(t, st, session, false)
			session, err := st.Act(t.Context(), session.ID, "undo", study.Mutation{MutationID: "undo-first-answer", Revision: session.Revision})
			require.NoError(t, err)
			require.Equal(t, before.ID, session.Current.ID)
			assert.Equal(t, before.FSRS, session.Current.FSRS)
			assert.Nil(t, session.Current.AssessedAt)
			assert.False(t, session.CanUndo)
			assert.Equal(t, int64(2), session.Revision)
		}},
		{"undo reopens complete session", func(t *testing.T, st *Store, session study.Session) {
			session = answer(t, st, answer(t, st, session, true), true)
			require.Equal(t, "complete", session.Status)
			next, err := st.Act(t.Context(), session.ID, "undo", study.Mutation{MutationID: "undo-final-answer", Revision: session.Revision})
			require.NoError(t, err)
			assert.Equal(t, "active", next.Status)
			assert.Equal(t, 1, next.Remaining)
		}},
		{"lost response retry is idempotent", func(t *testing.T, st *Store, session study.Session) {
			mutation := study.Mutation{MutationID: "same-answer-retry", Revision: session.Revision, CardID: session.Current.ID, Right: true}
			first, err := st.Act(t.Context(), session.ID, "answers", mutation)
			require.NoError(t, err)
			repeated, err := st.Act(t.Context(), session.ID, "answers", mutation)
			require.NoError(t, err)
			assert.Equal(t, first.Revision, repeated.Revision)
			assert.Equal(t, first.Remaining, repeated.Remaining)
			mutation.Right = false
			_, err = st.Act(t.Context(), session.ID, "answers", mutation)
			assert.ErrorIs(t, err, study.ErrConflict)
		}},
		{"stale answer rejected", func(t *testing.T, st *Store, session study.Session) {
			_ = answer(t, st, session, true)
			_, err := st.Act(t.Context(), session.ID, "answers", study.Mutation{
				MutationID: "stale-second-tab", Revision: session.Revision, CardID: session.Current.ID, Right: true,
			})
			assert.ErrorIs(t, err, study.ErrConflict)
		}},
		{"second unfinished session rejected", func(t *testing.T, st *Store, session study.Session) {
			_, err := st.Start(t.Context(), session.Settings, "second-session-id")
			assert.ErrorIs(t, err, study.ErrConflict)
		}},
		{"direction schedules independent", func(t *testing.T, st *Store, session study.Session) {
			cardID, termID := session.Current.ID, session.Current.TermID
			session = answer(t, st, session, true)
			card, err := loadCard(t.Context(), st.db, cardID)
			require.NoError(t, err)
			assert.Equal(t, st.now().Add(7*24*time.Hour), card.FSRS.Due)
			var assessed int
			require.NoError(t, st.db.QueryRow("SELECT COUNT(*) FROM cards WHERE term_id=? AND assessed_at IS NOT NULL", termID).Scan(&assessed))
			assert.Equal(t, 1, assessed)
		}},
		{"transaction failure rolls back schedule and queue", func(t *testing.T, st *Store, session study.Session) {
			_, err := st.db.Exec(`CREATE TRIGGER fail_history BEFORE INSERT ON review_events BEGIN SELECT RAISE(ABORT,'test failure'); END`)
			require.NoError(t, err)
			_, err = st.Act(t.Context(), session.ID, "answers", study.Mutation{
				MutationID: "rollback-answer-id", Revision: session.Revision, CardID: session.Current.ID, Right: true,
			})
			require.ErrorContains(t, err, "test failure")
			reloaded, err := st.Session(t.Context(), session.ID)
			require.NoError(t, err)
			assert.Equal(t, session.Revision, reloaded.Revision)
			assert.Nil(t, reloaded.Current.AssessedAt)
			assert.Equal(t, session.Queue, reloaded.Queue)
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			st, _ := testStore(t)
			tt.run(t, st, startSession(t, st, "assessment", 2))
		})
	}
}

func TestPersistentResume(t *testing.T) {
	st, path := testStore(t)
	session := answer(t, st, startSession(t, st, "assessment", 3), false)
	paused, err := st.Act(t.Context(), session.ID, "pause", study.Mutation{MutationID: "pause-and-restart", Revision: session.Revision})
	require.NoError(t, err)
	require.NoError(t, st.Close())
	reopened, err := Open(t.Context(), path)
	require.NoError(t, err)
	defer func() { require.NoError(t, reopened.Close()) }()
	current, err := reopened.Current(t.Context())
	require.NoError(t, err)
	require.NotNil(t, current)
	assert.Equal(t, paused.Queue, current.Queue)
	assert.Equal(t, "paused", current.Status)
	assert.Equal(t, paused.Revision, current.Revision)
}

func TestSync(t *testing.T) {
	for _, tt := range []struct {
		name string
		run  func(*testing.T, *Store)
	}{
		{"unchanged snapshot is a no-op", func(t *testing.T, st *Store) {
			result, err := st.Sync(t.Context(), fstest.MapFS{
				"data/one.json": {Data: testSource(t, "HSK 1", "one", "two", "three")},
				"data/two.json": {Data: testSource(t, "HSK 2", "four")},
			})
			require.NoError(t, err)
			assert.Zero(t, result.ChangedFiles)
		}},
		{"move and correction preserve identity and schedule", func(t *testing.T, st *Store) {
			session := answer(t, st, startSession(t, st, "assessment", 1), true)
			var cardID int64
			require.NoError(t, st.db.QueryRow("SELECT card_id FROM review_events WHERE session_id=?", session.ID).Scan(&cardID))
			before, err := loadCard(t.Context(), st.db, cardID)
			require.NoError(t, err)
			_, err = st.Sync(t.Context(), fstest.MapFS{
				"data/moved.json": {Data: testSource(t, "HSK 3", before.Simplified)},
			})
			require.NoError(t, err)
			after, err := loadCard(t.Context(), st.db, cardID)
			require.NoError(t, err)
			assert.Equal(t, before.TermID, after.TermID)
			assert.Equal(t, before.FSRS, after.FSRS)
			assert.Equal(t, 3, after.Level)
			var active int
			require.NoError(t, st.db.QueryRow("SELECT COUNT(*) FROM terms WHERE active=1").Scan(&active))
			assert.Equal(t, 1, active)
		}},
		{"removed terms reconcile active queue", func(t *testing.T, st *Store) {
			session := startSession(t, st, "assessment", 3)
			_, err := st.Sync(t.Context(), fstest.MapFS{"data/empty.json": {Data: testSource(t, "HSK 1")}})
			require.NoError(t, err)
			session, err = st.Session(t.Context(), session.ID)
			require.NoError(t, err)
			assert.Equal(t, "complete", session.Status)
			assert.NotEmpty(t, session.Notice)
			assert.Nil(t, session.Current)
		}},
		{"reactivation preserves card IDs", func(t *testing.T, st *Store) {
			var before int64
			require.NoError(t, st.db.QueryRow("SELECT id FROM terms WHERE simplified='one'").Scan(&before))
			_, err := st.Sync(t.Context(), fstest.MapFS{"data/empty.json": {Data: testSource(t, "HSK 1")}})
			require.NoError(t, err)
			_, err = st.Sync(t.Context(), fstest.MapFS{"data/one.json": {Data: testSource(t, "HSK 1", "one")}})
			require.NoError(t, err)
			var after int64
			require.NoError(t, st.db.QueryRow("SELECT id FROM terms WHERE simplified='one' AND active=1").Scan(&after))
			assert.Equal(t, before, after)
		}},
		{"invalid snapshot leaves hashes untouched", func(t *testing.T, st *Store) {
			var before string
			require.NoError(t, st.db.QueryRow("SELECT sha256 FROM data_imports WHERE path='data/one.json'").Scan(&before))
			_, err := st.Sync(t.Context(), fstest.MapFS{"data/one.json": {Data: []byte(`{"label":"bad"}`)}})
			require.Error(t, err)
			var after string
			require.NoError(t, st.db.QueryRow("SELECT sha256 FROM data_imports WHERE path='data/one.json'").Scan(&after))
			assert.Equal(t, before, after)
		}},
		{"database failure rolls back source hash", func(t *testing.T, st *Store) {
			_, err := st.db.Exec(`CREATE TRIGGER fail_terms BEFORE UPDATE ON terms BEGIN SELECT RAISE(ABORT,'sync failure'); END`)
			require.NoError(t, err)
			_, err = st.Sync(t.Context(), fstest.MapFS{"data/new.json": {Data: testSource(t, "HSK 1", "one")}})
			require.ErrorContains(t, err, "sync failure")
			var count int
			require.NoError(t, st.db.QueryRow("SELECT COUNT(*) FROM data_imports WHERE path='data/new.json'").Scan(&count))
			assert.Zero(t, count)
		}},
		{"incomplete vocabulary excluded", func(t *testing.T, st *Store) {
			_, err := st.Sync(t.Context(), fstest.MapFS{
				"data/one.json": {Data: []byte(`{"label":"HSK 1","totalTerms":1,"terms":[{"simplified":"one","pinyin":null,"english":null}]}`)},
			})
			require.NoError(t, err)
			_, err = st.Start(t.Context(), study.Defaults(), "incomplete-session")
			assert.ErrorIs(t, err, study.ErrInvalid)
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			st, _ := testStore(t)
			tt.run(t, st)
		})
	}
}

func TestSelection(t *testing.T) {
	for _, tt := range []struct {
		name string
		run  func(*testing.T, *Store)
	}{
		{"exact level", func(t *testing.T, st *Store) {
			settings := study.Defaults()
			settings.Level, settings.IncludeLower = 2, false
			session, err := st.Start(t.Context(), settings, "exact-level-start")
			require.NoError(t, err)
			assert.Equal(t, 3, session.InitialSize)
			assert.Equal(t, 2, session.Current.Level)
		}},
		{"lower levels included", func(t *testing.T, st *Store) {
			settings := study.Defaults()
			settings.Level = 2
			session, err := st.Start(t.Context(), settings, "lower-level-start")
			require.NoError(t, err)
			assert.Equal(t, 12, session.InitialSize)
		}},
		{"future cards not pulled forward", func(t *testing.T, st *Store) {
			now := timestamp(st.now())
			future := timestamp(st.now().Add(24 * time.Hour))
			_, err := st.db.Exec("UPDATE cards SET assessed_at=?,due=?", now, future)
			require.NoError(t, err)
			_, err = st.Start(t.Context(), study.Defaults(), "future-cards-start")
			assert.ErrorIs(t, err, study.ErrInvalid)
		}},
		{"due review wins over new cards", func(t *testing.T, st *Store) {
			var id int64
			require.NoError(t, st.db.QueryRow("SELECT id FROM cards LIMIT 1").Scan(&id))
			_, err := st.db.Exec("UPDATE cards SET assessed_at=? WHERE id=?", timestamp(st.now()), id)
			require.NoError(t, err)
			session, err := st.Start(t.Context(), study.Defaults(), "due-priority-start")
			require.NoError(t, err)
			assert.Equal(t, id, session.Current.ID)
		}},
		{"assessment ignores assessed cards", func(t *testing.T, st *Store) {
			_, err := st.db.Exec("UPDATE cards SET assessed_at=?", timestamp(st.now()))
			require.NoError(t, err)
			settings := study.Defaults()
			settings.Mode = "assessment"
			_, err = st.Start(t.Context(), settings, "assessment-empty")
			assert.ErrorIs(t, err, study.ErrInvalid)
		}},
		{"single card batch avoids prior word", func(t *testing.T, st *Store) {
			first := startSession(t, st, "assessment", 1)
			word := first.Current.TermID
			_ = answer(t, st, first, true)
			second := startSession(t, st, "assessment", 1)
			assert.NotEqual(t, word, second.Current.TermID)
		}},
		{"ending without grading preserves last shown word", func(t *testing.T, st *Store) {
			first := startSession(t, st, "assessment", 1)
			_, err := st.Act(t.Context(), first.ID, "end", study.Mutation{
				MutationID: "end-without-answer", Revision: first.Revision,
			})
			require.NoError(t, err)
			second := startSession(t, st, "assessment", 1)
			assert.NotEqual(t, first.Current.TermID, second.Current.TermID)
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			st, _ := testStore(t)
			tt.run(t, st)
		})
	}
}

func TestLapseAndVerification(t *testing.T) {
	st, _ := testStore(t)
	session := startSession(t, st, "assessment", 1)
	id := session.Current.ID
	_ = answer(t, st, session, true)
	st.now = func() time.Time { return time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC) }
	settings := study.Defaults()
	settings.BatchSize, settings.Directions = 1, []string{"read"}
	review, err := st.Start(t.Context(), settings, "verification-start")
	require.NoError(t, err)
	require.Equal(t, id, review.Current.ID)
	review = answer(t, st, review, false)
	require.Equal(t, id, review.Current.ID)
	assert.Equal(t, uint64(1), review.Current.FSRS.Lapses)
	review = answer(t, st, review, true)
	assert.Equal(t, "complete", review.Status)
	card, err := loadCard(context.Background(), st.db, id)
	require.NoError(t, err)
	assert.Equal(t, fsrs.Review, card.FSRS.State)
	assert.Less(t, card.FSRS.ScheduledDays, uint64(7))
}
