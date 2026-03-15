package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// LinkInfo describes a symlink found in node_modules.
type LinkInfo struct {
	Name   string // package name (e.g. "lodash-es" or "@scope/pkg")
	Target string // symlink target path
}

// ScanLinks finds all symlinks in the given node_modules directory.
func ScanLinks(nodeModulesDir string) ([]LinkInfo, error) {
	entries, err := os.ReadDir(nodeModulesDir)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", nodeModulesDir, err)
	}

	var links []LinkInfo

	for _, entry := range entries {
		name := entry.Name()
		fullPath := filepath.Join(nodeModulesDir, name)

		// Handle scoped packages (@scope/pkg)
		if name[0] == '@' {
			scopeLinks, err := scanScopeDir(nodeModulesDir, name)
			if err != nil {
				return nil, err
			}
			links = append(links, scopeLinks...)
			continue
		}

		link, ok, err := checkSymlink(fullPath, name)
		if err != nil {
			return nil, err
		}
		if ok {
			links = append(links, link)
		}
	}

	return links, nil
}

func scanScopeDir(nodeModulesDir, scope string) ([]LinkInfo, error) {
	scopePath := filepath.Join(nodeModulesDir, scope)
	entries, err := os.ReadDir(scopePath)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", scopePath, err)
	}

	var links []LinkInfo
	for _, entry := range entries {
		fullPath := filepath.Join(scopePath, entry.Name())
		pkgName := scope + "/" + entry.Name()

		link, ok, err := checkSymlink(fullPath, pkgName)
		if err != nil {
			return nil, err
		}
		if ok {
			links = append(links, link)
		}
	}
	return links, nil
}

func checkSymlink(path, name string) (LinkInfo, bool, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return LinkInfo{}, false, fmt.Errorf("lstat %s: %w", path, err)
	}

	if fi.Mode()&os.ModeSymlink == 0 {
		return LinkInfo{}, false, nil
	}

	target, err := os.Readlink(path)
	if err != nil {
		return LinkInfo{}, false, fmt.Errorf("readlink %s: %w", path, err)
	}

	return LinkInfo{Name: name, Target: target}, true, nil
}

// RemoveLinks removes all symlinks from node_modules.
func RemoveLinks(nodeModulesDir string) error {
	links, err := ScanLinks(nodeModulesDir)
	if err != nil {
		return err
	}

	if len(links) == 0 {
		printInfo("No symlinks found.")
		return nil
	}

	for _, link := range links {
		path := filepath.Join(nodeModulesDir, link.Name)
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("removing symlink %s: %w", path, err)
		}
		printRemoved(link.Name)
	}

	return nil
}
