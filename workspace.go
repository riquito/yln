package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// PackageJSON represents the relevant fields of a package.json file.
type PackageJSON struct {
	Name         string            `json:"name"`
	Version      string            `json:"version"`
	Dependencies map[string]string `json:"dependencies"`
	Workspaces   []string          `json:"workspaces"`
}

// Workspace represents a single workspace package in the monorepo.
type Workspace struct {
	Name     string
	Dir      string
	PkgJSON  PackageJSON
	WorkDeps []string // names of workspace:* dependencies
}

// Monorepo represents a monorepo with workspace packages.
type Monorepo struct {
	RootDir    string
	Workspaces map[string]*Workspace // keyed by package name
}

// LoadMonorepo reads the root package.json and discovers all workspace packages.
func LoadMonorepo(rootDir string) (*Monorepo, error) {
	rootPkg, err := readPackageJSON(filepath.Join(rootDir, "package.json"))
	if err != nil {
		return nil, fmt.Errorf("reading root package.json: %w", err)
	}

	if len(rootPkg.Workspaces) == 0 {
		return nil, fmt.Errorf("no workspaces defined in %s/package.json", rootDir)
	}

	m := &Monorepo{
		RootDir:    rootDir,
		Workspaces: make(map[string]*Workspace),
	}

	for _, pattern := range rootPkg.Workspaces {
		fullPattern := filepath.Join(rootDir, pattern)
		matches, err := filepath.Glob(fullPattern)
		if err != nil {
			return nil, fmt.Errorf("expanding workspace glob %q: %w", pattern, err)
		}

		for _, dir := range matches {
			pkgJSONPath := filepath.Join(dir, "package.json")
			if _, err := os.Stat(pkgJSONPath); err != nil {
				continue // skip dirs without package.json
			}

			pkg, err := readPackageJSON(pkgJSONPath)
			if err != nil {
				return nil, fmt.Errorf("reading %s: %w", pkgJSONPath, err)
			}

			if pkg.Name == "" {
				continue // skip packages without a name
			}

			ws := &Workspace{
				Name:     pkg.Name,
				Dir:      dir,
				PkgJSON:  pkg,
				WorkDeps: findWorkspaceDeps(pkg),
			}
			m.Workspaces[pkg.Name] = ws
		}
	}

	return m, nil
}

// findWorkspaceDeps returns the names of dependencies that use workspace: protocol.
func findWorkspaceDeps(pkg PackageJSON) []string {
	var deps []string
	for name, version := range pkg.Dependencies {
		if isWorkspaceVersion(version) {
			deps = append(deps, name)
		}
	}
	return deps
}

// isWorkspaceVersion returns true if the version string uses the workspace: protocol.
func isWorkspaceVersion(version string) bool {
	return len(version) >= 11 && version[:10] == "workspace:"
}

func readPackageJSON(path string) (PackageJSON, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return PackageJSON{}, err
	}

	var pkg PackageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return PackageJSON{}, err
	}

	return pkg, nil
}
