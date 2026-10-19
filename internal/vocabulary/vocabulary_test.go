package vocabulary_test

import (
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"

	"github.com/corani/wren/internal/vocabulary"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name  string
		input string
		err   string
		min   int
		max   int
	}{
		{"single level ignores numeric level", `{"label":"HSK 3","level":99,"totalTerms":1,"terms":[{"simplified":"a","pinyin":"a","english":"a"}]}`, "", 3, 3},
		{"combined group", `{"label":"HSK 7-9","totalTerms":0,"terms":[]}`, "", 7, 9},
		{"missing enrichment is permitted", `{"label":"HSK 1","totalTerms":1,"terms":[{"simplified":"a","pinyin":null,"english":null}]}`, "", 1, 1},
		{"invalid label", `{"label":"HSK 9","terms":[]}`, "unsupported HSK label", 0, 0},
		{"unknown field", `{"label":"HSK 1","typo":1,"terms":[]}`, "unknown field", 0, 0},
		{"wrong count", `{"label":"HSK 1","totalTerms":2,"terms":[]}`, "invalid term counts", 0, 0},
		{"missing terms", `{"label":"HSK 1"}`, "invalid term counts", 0, 0},
		{"wrong field type", `{"label":"HSK 1","terms":"wrong"}`, "cannot unmarshal", 0, 0},
		{"empty identity", `{"label":"HSK 1","totalTerms":1,"terms":[{"simplified":" "}]}`, "empty term identity", 0, 0},
		{"duplicate", `{"label":"HSK 1","totalTerms":2,"terms":[{"simplified":"a"},{"simplified":"a"}]}`, "duplicate", 0, 0},
		{"trailing object", `{"label":"HSK 1","terms":[]} {}`, "one JSON object", 0, 0},
		{"invalid enrichment count", `{"label":"HSK 1","totalTerms":0,"enrichedTerms":1,"terms":[]}`, "invalid term counts", 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fsys := fstest.MapFS{"data/words.json": {Data: []byte(tt.input)}}
			sources, err := vocabulary.Load(fsys)
			if tt.err != "" {
				require.ErrorContains(t, err, tt.err)
				return
			}
			require.NoError(t, err)
			require.Len(t, sources, 1)
			require.Equal(t, tt.min, sources[0].Min)
			require.Equal(t, tt.max, sources[0].Max)
		})
	}
}

func TestSnapshotValidation(t *testing.T) {
	for _, tt := range []struct {
		name  string
		files fstest.MapFS
		err   string
	}{
		{"empty snapshot", fstest.MapFS{}, "no source files"},
		{"cross-file duplicate", fstest.MapFS{
			"data/a.json": {Data: []byte(`{"label":"HSK 1","totalTerms":1,"terms":[{"simplified":"a"}]}`)},
			"data/b.json": {Data: []byte(`{"label":"HSK 2","totalTerms":1,"terms":[{"simplified":"a"}]}`)},
		}, "duplicate"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := vocabulary.Load(tt.files)
			require.ErrorContains(t, err, tt.err)
		})
	}
}
