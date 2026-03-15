package main

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// Config holds yln configuration.
type Config struct {
	Monorepo string
}

// LoadConfig reads the config from ~/.config/yln/config.toml.
// Returns nil (not an error) if the file doesn't exist.
func LoadConfig() (*Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, nil
	}

	path := filepath.Join(home, ".config", "yln", "config.toml")
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	cfg := &Config{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || line[0] == '#' {
			continue
		}

		if strings.HasPrefix(line, "monorepo") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) != 2 {
				continue
			}
			val := strings.TrimSpace(parts[1])
			// Strip quotes
			val = strings.Trim(val, `"'`)
			// Expand ~
			if strings.HasPrefix(val, "~/") {
				val = filepath.Join(home, val[2:])
			}
			cfg.Monorepo = val
		}
	}

	if cfg.Monorepo == "" {
		return nil, nil
	}

	return cfg, scanner.Err()
}
