package config

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefault(t *testing.T) {
	home, data := t.TempDir(), t.TempDir()
	for _, tt := range []struct {
		name     string
		home     string
		data     string
		database string
		wantErr  bool
	}{
		{
			name:     "XDG data home takes precedence",
			home:     home,
			data:     data,
			database: filepath.Join(data, "wren", "wren.db"),
		},
		{
			name:     "XDG data home without a user home",
			data:     data,
			database: filepath.Join(data, "wren", "wren.db"),
		},
		{
			name:     "fallback to user home",
			home:     home,
			database: filepath.Join(home, ".local", "share", "wren", "wren.db"),
		},
		{
			name:    "missing user home",
			wantErr: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("XDG_DATA_HOME", tt.data)
			t.Setenv("HOME", tt.home)
			t.Setenv("USERPROFILE", tt.home)

			cfg, err := Default()
			if tt.wantErr {
				require.ErrorContains(t, err, "config: resolve home:")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "127.0.0.1:8080", cfg.Addr)
			assert.Equal(t, tt.database, cfg.Database)
		})
	}
}
