package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// Link creates symlinks in nodeModulesDir for each requested package and its
// transitive workspace dependencies. If dryRun is true, it prints what would
// be linked without making any changes.
func Link(monorepo *Monorepo, nodeModulesDir string, packageNames []string, dryRun bool) error {
	visited := make(map[string]bool)
	for _, name := range packageNames {
		if err := linkRecursive(monorepo, nodeModulesDir, name, visited, dryRun); err != nil {
			return err
		}
	}
	return nil
}

func linkRecursive(monorepo *Monorepo, nodeModulesDir, pkgName string, visited map[string]bool, dryRun bool) error {
	if visited[pkgName] {
		return nil // already linked or cycle
	}
	visited[pkgName] = true

	ws, ok := monorepo.Workspaces[pkgName]
	if !ok {
		return nil // not a workspace package, skip
	}

	targetPath := filepath.Join(nodeModulesDir, pkgName)

	if dryRun {
		printDryRun(pkgName, ws.Dir)
	} else {
		// Handle scoped packages: ensure @scope/ directory exists
		if pkgName[0] == '@' {
			scopeDir := filepath.Dir(targetPath)
			if err := os.MkdirAll(scopeDir, 0o755); err != nil {
				return fmt.Errorf("creating scope directory %s: %w", scopeDir, err)
			}
		}

		// Remove existing entry (file, dir, or symlink)
		if err := os.RemoveAll(targetPath); err != nil {
			return fmt.Errorf("removing %s: %w", targetPath, err)
		}

		// Create symlink
		if err := os.Symlink(ws.Dir, targetPath); err != nil {
			return fmt.Errorf("creating symlink %s -> %s: %w", targetPath, ws.Dir, err)
		}

		printLinked(pkgName, ws.Dir)
	}

	// Recurse into workspace dependencies
	for _, dep := range ws.WorkDeps {
		if err := linkRecursive(monorepo, nodeModulesDir, dep, visited, dryRun); err != nil {
			return err
		}
	}

	return nil
}
