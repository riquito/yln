package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// StashData holds the stashed link state.
type StashData struct {
	Monorepo string   `json:"monorepo"`
	Packages []string `json:"packages"`
	Date     string   `json:"date"`
}

// stashFilePathFunc can be overridden in tests to use a temporary directory.
var stashFilePathFunc = defaultStashFilePath

func stashFilePath() (string, error) {
	return stashFilePathFunc()
}

func defaultStashFilePath() (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("determining cache directory: %w", err)
	}
	return filepath.Join(cacheDir, "yln", "stash.json"), nil
}

func saveStash(data *StashData) error {
	path, err := stashFilePath()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating stash directory: %w", err)
	}

	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling stash: %w", err)
	}

	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("writing stash file: %w", err)
	}

	return nil
}

func loadStash() (*StashData, error) {
	path, err := stashFilePath()
	if err != nil {
		return nil, err
	}

	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading stash file: %w", err)
	}

	var data StashData
	if err := json.Unmarshal(b, &data); err != nil {
		return nil, fmt.Errorf("parsing stash file: %w", err)
	}

	return &data, nil
}

func deleteStash() error {
	path, err := stashFilePath()
	if err != nil {
		return err
	}

	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing stash file: %w", err)
	}

	return nil
}

func cmdStash(args []string, nmDir string) error {
	var monorepoPath string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--monorepo":
			if i+1 >= len(args) {
				return fmt.Errorf("--monorepo requires a path argument")
			}
			i++
			monorepoPath = args[i]
		}
	}

	// Resolve monorepo path (needed at pop time)
	monorepo, err := resolveMonorepo(monorepoPath)
	if err != nil {
		return err
	}

	links, err := ScanLinks(nmDir)
	if err != nil {
		return err
	}

	if len(links) == 0 {
		printInfo("No symlinks found — nothing to stash.")
		return nil
	}

	pkgNames := make([]string, len(links))
	for i, link := range links {
		pkgNames[i] = link.Name
	}

	data := &StashData{
		Monorepo: monorepo.RootDir,
		Packages: pkgNames,
		Date:     time.Now().UTC().Format(time.RFC3339),
	}

	if err := saveStash(data); err != nil {
		return err
	}

	if err := RemoveLinks(nmDir); err != nil {
		return err
	}

	fmt.Printf("Stashed %d package(s).\n", len(pkgNames))
	return nil
}

func cmdPop(args []string, nmDir string) error {
	data, err := loadStash()
	if err != nil {
		return err
	}

	if data == nil {
		printInfo("Nothing to pop.")
		return nil
	}

	var monorepoPath string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--monorepo":
			if i+1 >= len(args) {
				return fmt.Errorf("--monorepo requires a path argument")
			}
			i++
			monorepoPath = args[i]
		}
	}

	// Use stash's monorepo as fallback
	if monorepoPath == "" {
		monorepoPath = data.Monorepo
	}

	monorepo, err := resolveMonorepo(monorepoPath)
	if err != nil {
		return err
	}

	if _, err := os.Stat(nmDir); err != nil {
		return fmt.Errorf("node_modules not found in current directory (run yarn install first)")
	}

	// Filter packages to those still in monorepo workspaces
	var validPackages []string
	for _, pkg := range data.Packages {
		if _, ok := monorepo.Workspaces[pkg]; ok {
			validPackages = append(validPackages, pkg)
		}
	}

	if len(validPackages) == 0 {
		printInfo("No stashed packages found in current monorepo workspaces.")
		_ = deleteStash()
		return nil
	}

	printHeader("Restoring packages:")
	if err := Link(monorepo, nmDir, validPackages, false); err != nil {
		return err
	}

	if err := deleteStash(); err != nil {
		return err
	}

	fmt.Printf("Restored %d package(s).\n", len(validPackages))
	return nil
}
