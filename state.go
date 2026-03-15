package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// LinkState records the packages the user directly requested, so that watch
// mode can re-resolve the full transitive set and clean up orphans.
type LinkState struct {
	Monorepo  string   `json:"monorepo"`
	Requested []string `json:"requested"`
}

const stateFileName = ".yln-state.json"

// stateFilePathFunc can be overridden in tests to relocate the state file.
var stateFilePathFunc = defaultStateFilePath

func defaultStateFilePath(nmDir string) string {
	return filepath.Join(nmDir, stateFileName)
}

func stateFilePath(nmDir string) string {
	return stateFilePathFunc(nmDir)
}

func saveLinkState(nmDir string, state *LinkState) error {
	path := stateFilePath(nmDir)

	// Sort for deterministic output
	sorted := make([]string, len(state.Requested))
	copy(sorted, state.Requested)
	sort.Strings(sorted)
	state.Requested = sorted

	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling link state: %w", err)
	}

	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return fmt.Errorf("writing link state: %w", err)
	}

	return nil
}

func loadLinkState(nmDir string) (*LinkState, error) {
	path := stateFilePath(nmDir)

	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading link state: %w", err)
	}

	var state LinkState
	if err := json.Unmarshal(b, &state); err != nil {
		return nil, fmt.Errorf("parsing link state: %w", err)
	}

	return &state, nil
}

func deleteLinkState(nmDir string) error {
	path := stateFilePath(nmDir)

	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing link state: %w", err)
	}

	return nil
}
