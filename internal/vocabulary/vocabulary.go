package vocabulary

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"strconv"
	"strings"
)

type Term struct {
	Simplified  string `json:"simplified"`
	Traditional string `json:"traditional"`
	Pinyin      string `json:"pinyin"`
	English     string `json:"english"`
}

func (t Term) Available() bool {
	return strings.TrimSpace(t.Simplified) != "" &&
		strings.TrimSpace(t.Pinyin) != "" && strings.TrimSpace(t.English) != ""
}

type Source struct {
	Path          string          `json:"-"`
	Hash          string          `json:"-"`
	Level         int             `json:"level"`
	Label         string          `json:"label"`
	Min           int             `json:"-"`
	Max           int             `json:"-"`
	Metadata      json.RawMessage `json:"metadata"`
	TotalTerms    int             `json:"totalTerms"`
	EnrichedTerms int             `json:"enrichedTerms"`
	Terms         []Term          `json:"terms"`
}

func ParseLevel(label string) (int, int, error) {
	if label == "HSK 7-9" {
		return 7, 9, nil
	}
	for n := 1; n <= 6; n++ {
		if label == "HSK "+strconv.Itoa(n) {
			return n, n, nil
		}
	}
	return 0, 0, fmt.Errorf("unsupported HSK label %q", label)
}

func Load(fsys fs.FS) ([]Source, error) {
	paths, err := fs.Glob(fsys, "data/*.json")
	if err != nil {
		return nil, fmt.Errorf("vocabulary: enumerate: %w", err)
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("vocabulary: no source files found")
	}
	var sources []Source
	seen := map[string]string{}
	for _, path := range paths {
		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			return nil, fmt.Errorf("vocabulary: read %s: %w", path, err)
		}
		var source Source
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&source); err != nil {
			return nil, fmt.Errorf("vocabulary: parse %s: %w", path, err)
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return nil, fmt.Errorf("vocabulary: %s must contain one JSON object", path)
		}
		source.Min, source.Max, err = ParseLevel(source.Label)
		if err != nil {
			return nil, fmt.Errorf("vocabulary: %s: %w", path, err)
		}
		if source.Terms == nil || source.TotalTerms != len(source.Terms) ||
			source.EnrichedTerms < 0 || source.EnrichedTerms > source.TotalTerms {
			return nil, fmt.Errorf("vocabulary: %s has invalid term counts", path)
		}
		for _, term := range source.Terms {
			if strings.TrimSpace(term.Simplified) == "" {
				return nil, fmt.Errorf("vocabulary: %s has an empty term identity", path)
			}
			if previous, ok := seen[term.Simplified]; ok {
				return nil, fmt.Errorf("vocabulary: duplicate %q in %s and %s", term.Simplified, previous, path)
			}
			seen[term.Simplified] = path
		}
		hash := sha256.Sum256(data)
		source.Path, source.Hash = path, hex.EncodeToString(hash[:])
		sources = append(sources, source)
	}
	return sources, nil
}
