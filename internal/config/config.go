package config

import (
	"fmt"
	"os"
	"path/filepath"
)

type Config struct {
	Addr     string
	Database string
}

func Default() (Config, error) {
	dir := os.Getenv("XDG_DATA_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Config{}, fmt.Errorf("config: resolve home: %w", err)
		}
		dir = filepath.Join(home, ".local", "share")
	}
	return Config{
		Addr:     "127.0.0.1:8080",
		Database: filepath.Join(dir, "wren", "wren.db"),
	}, nil
}
