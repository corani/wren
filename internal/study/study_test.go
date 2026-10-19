package study

import (
	"testing"
	"time"

	fsrs "github.com/open-spaced-repetition/go-fsrs/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnswer(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name       string
		right      bool
		assessment bool
		known      bool
		kind       string
		state      fsrs.State
	}{
		{"known assessment", true, true, true, "assessment", fsrs.Review},
		{"unknown assessment", false, true, false, "assessment", fsrs.Learning},
		{"training success", true, false, false, "review", fsrs.Review},
		{"training failure", false, false, false, "review", fsrs.Learning},
	} {
		t.Run(tt.name, func(t *testing.T) {
			card, kind, err := Answer(Card{FSRS: fsrs.NewCard(now)}, tt.right, tt.assessment, now)
			require.NoError(t, err)
			assert.Equal(t, tt.kind, kind)
			assert.Equal(t, tt.state, card.FSRS.State)
			if tt.assessment {
				require.NotNil(t, card.InitialKnown)
				assert.Equal(t, tt.known, *card.InitialKnown)
			}
			if tt.known {
				assert.Equal(t, now.Add(7*24*time.Hour), card.FSRS.Due)
				assert.Zero(t, card.FSRS.Reps)
			} else if !tt.right {
				assert.Equal(t, now.Add(time.Minute), card.FSRS.Due)
			}
			if !tt.right {
				original := card.FSRS.Stability
				retry, retryKind, err := Answer(card, true, tt.assessment, now.Add(15*time.Second))
				require.NoError(t, err)
				assert.Equal(t, "review", retryKind)
				assert.Equal(t, fsrs.Review, retry.FSRS.State)
				assert.LessOrEqual(t, retry.FSRS.Stability, original*2)
				assert.Equal(t, card.InitialKnown, retry.InitialKnown)
			}
		})
	}
}

func TestQueueSeparation(t *testing.T) {
	for _, tt := range []struct {
		name  string
		queue []QueueItem
		want  int64
	}{
		{"alternative", []QueueItem{{TermID: 1}, {TermID: 1}, {TermID: 2}}, 2},
		{"one word", []QueueItem{{TermID: 1}}, 1},
		{"cleared cards ignored", []QueueItem{{TermID: 3, Cleared: true}, {TermID: 1}, {TermID: 2}}, 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			SeparateHead(tt.queue, 1)
			for _, item := range tt.queue {
				if !item.Cleared {
					require.Equal(t, tt.want, item.TermID)
					break
				}
			}
		})
	}
}
