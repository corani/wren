package study

import (
	"errors"
	"fmt"
	"slices"
	"time"

	fsrs "github.com/open-spaced-repetition/go-fsrs/v4"
)

var (
	ErrInvalid  = errors.New("invalid request")
	ErrConflict = errors.New("session changed; refresh and try again")
	ErrNotFound = errors.New("not found")
)

type Settings struct {
	Level        int      `json:"level"`
	IncludeLower bool     `json:"includeLower"`
	BatchSize    int      `json:"batchSize"`
	Directions   []string `json:"directions"`
	Mode         string   `json:"mode"`
}

func Defaults() Settings {
	return Settings{
		Level: 1, IncludeLower: true, BatchSize: 20,
		Directions: []string{"read", "write", "pinyin"}, Mode: "training",
	}
}

func (s Settings) Validate() error {
	if s.Level < 1 || s.Level > 7 || s.BatchSize < 1 || s.BatchSize > 100 {
		return fmt.Errorf("%w: choose an HSK group and a batch size from 1 to 100", ErrInvalid)
	}
	if s.Mode != "training" && s.Mode != "assessment" {
		return fmt.Errorf("%w: unknown session type", ErrInvalid)
	}
	if len(s.Directions) == 0 || len(s.Directions) > 3 {
		return fmt.Errorf("%w: select at least one direction", ErrInvalid)
	}
	seen := map[string]bool{}
	for _, direction := range s.Directions {
		if !slices.Contains([]string{"read", "write", "pinyin"}, direction) || seen[direction] {
			return fmt.Errorf("%w: invalid or repeated direction", ErrInvalid)
		}
		seen[direction] = true
	}
	return nil
}

type Card struct {
	ID           int64      `json:"id"`
	TermID       int64      `json:"termId"`
	Direction    string     `json:"direction"`
	Simplified   string     `json:"simplified"`
	Pinyin       string     `json:"pinyin"`
	English      string     `json:"english"`
	Level        int        `json:"level"`
	AssessedAt   *time.Time `json:"assessedAt"`
	InitialKnown *bool      `json:"initialKnown"`
	FSRS         fsrs.Card  `json:"schedule"`
}

type QueueItem struct {
	CardID  int64 `json:"cardId"`
	TermID  int64 `json:"termId"`
	Cleared bool  `json:"cleared"`
}

type Session struct {
	ID          int64       `json:"id"`
	Settings    Settings    `json:"settings"`
	Status      string      `json:"status"`
	Revision    int64       `json:"revision"`
	InitialSize int         `json:"initialSize"`
	LastTermID  int64       `json:"lastTermId"`
	Queue       []QueueItem `json:"-"`
	Remaining   int         `json:"remaining"`
	Cleared     int         `json:"cleared"`
	Current     *Card       `json:"current"`
	CanUndo     bool        `json:"canUndo"`
	Notice      string      `json:"notice"`
}

type Level struct {
	Level     int    `json:"level"`
	Label     string `json:"label"`
	Total     int    `json:"total"`
	Available int    `json:"available"`
}

type Mutation struct {
	MutationID string `json:"mutationId"`
	Revision   int64  `json:"revision"`
	CardID     int64  `json:"cardId"`
	Right      bool   `json:"right"`
}

func Parameters() fsrs.Parameters {
	p := fsrs.DefaultParam()
	// One learning step lets Good clear a retry without requiring a timed wait.
	p.LearningSteps = []float64{1}
	p.RelearningSteps = []float64{1}
	return p
}

// Answer distinguishes self-assessment from an observed recall attempt.
func Answer(card Card, right, assessment bool, now time.Time) (Card, string, error) {
	p := Parameters()
	kind := "review"
	if assessment && card.AssessedAt == nil {
		kind = "assessment"
		card.AssessedAt = &now
		card.InitialKnown = &right
		if right {
			// Seed a seven-day memory estimate, not a fabricated successful review.
			initial := p.NextState(nil, p.RequestRetention, 0, fsrs.Good)
			card.FSRS = fsrs.NewCard(now)
			card.FSRS.State = fsrs.Review
			card.FSRS.Stability = 7
			card.FSRS.Difficulty = initial.Memory.Difficulty
			card.FSRS.ScheduledDays = 7
			card.FSRS.LastReview = now
			card.FSRS.Due = now.Add(7 * 24 * time.Hour)
			return card, kind, nil
		}
	}
	rating := fsrs.Again
	if right {
		rating = fsrs.Good
	}
	next, err := fsrs.NewFSRS(p).Next(card.FSRS, now, rating)
	if err != nil {
		return Card{}, "", fmt.Errorf("schedule card: %w", err)
	}
	card.FSRS = next.Card
	if card.AssessedAt == nil {
		card.AssessedAt = &now
	}
	return card, kind, nil
}

// SeparateHead avoids answer leakage when an alternative word remains.
func SeparateHead(queue []QueueItem, lastTermID int64) {
	first := -1
	for i, item := range queue {
		if item.Cleared {
			continue
		}
		if first == -1 {
			first = i
			if item.TermID != lastTermID {
				return
			}
		} else if item.TermID != lastTermID {
			candidate := queue[i]
			copy(queue[first+1:i+1], queue[first:i])
			queue[first] = candidate
			return
		}
	}
}
