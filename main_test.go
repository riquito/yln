package main

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
)

func testdataDir(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine test file location")
	}
	return filepath.Join(filepath.Dir(filename), "..", "projects")
}

func TestLoadMonorepo(t *testing.T) {
	projectsDir := testdataDir(t)
	monorepoDir := filepath.Join(projectsDir, "monorepo-yarn4")

	m, err := LoadMonorepo(monorepoDir)
	if err != nil {
		t.Fatalf("LoadMonorepo: %v", err)
	}

	expectedNames := []string{"lodash-es", "ramda", "underscore"}
	var gotNames []string
	for name := range m.Workspaces {
		gotNames = append(gotNames, name)
	}
	sort.Strings(gotNames)

	if len(gotNames) != len(expectedNames) {
		t.Fatalf("expected %d workspaces, got %d: %v", len(expectedNames), len(gotNames), gotNames)
	}
	for i, name := range expectedNames {
		if gotNames[i] != name {
			t.Errorf("workspace[%d]: expected %q, got %q", i, name, gotNames[i])
		}
	}
}

func TestWorkspaceDeps(t *testing.T) {
	projectsDir := testdataDir(t)
	monorepoDir := filepath.Join(projectsDir, "monorepo-yarn4")

	m, err := LoadMonorepo(monorepoDir)
	if err != nil {
		t.Fatalf("LoadMonorepo: %v", err)
	}

	// lodash-es depends on underscore via workspace:*
	lodashES := m.Workspaces["lodash-es"]
	if lodashES == nil {
		t.Fatal("lodash-es workspace not found")
	}
	if len(lodashES.WorkDeps) != 1 || lodashES.WorkDeps[0] != "underscore" {
		t.Errorf("lodash-es.WorkDeps: expected [underscore], got %v", lodashES.WorkDeps)
	}

	// underscore has no workspace deps
	underscore := m.Workspaces["underscore"]
	if underscore == nil {
		t.Fatal("underscore workspace not found")
	}
	if len(underscore.WorkDeps) != 0 {
		t.Errorf("underscore.WorkDeps: expected [], got %v", underscore.WorkDeps)
	}

	// ramda has no workspace deps
	ramda := m.Workspaces["ramda"]
	if ramda == nil {
		t.Fatal("ramda workspace not found")
	}
	if len(ramda.WorkDeps) != 0 {
		t.Errorf("ramda.WorkDeps: expected [], got %v", ramda.WorkDeps)
	}
}

func TestLinkAndScanAndClean(t *testing.T) {
	projectsDir := testdataDir(t)
	monorepoDir := filepath.Join(projectsDir, "monorepo-yarn4")

	m, err := LoadMonorepo(monorepoDir)
	if err != nil {
		t.Fatalf("LoadMonorepo: %v", err)
	}

	// Create a temporary node_modules directory for testing
	tmpDir := t.TempDir()
	nodeModulesDir := filepath.Join(tmpDir, "node_modules")
	if err := os.MkdirAll(nodeModulesDir, 0o755); err != nil {
		t.Fatalf("creating node_modules: %v", err)
	}

	// Create some fake existing dirs to simulate a real node_modules
	for _, name := range []string{"lodash-es", "underscore", "ramda"} {
		dir := filepath.Join(nodeModulesDir, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("creating fake %s: %v", name, err)
		}
		// Write a marker file so we can verify it gets replaced
		os.WriteFile(filepath.Join(dir, "marker.txt"), []byte("real"), 0o644)
	}

	// Link only lodash-es — should also link underscore (transitive) but NOT ramda
	if err := Link(m, nodeModulesDir, []string{"lodash-es"}, false); err != nil {
		t.Fatalf("Link: %v", err)
	}

	// Verify lodash-es is a symlink
	assertSymlink(t, filepath.Join(nodeModulesDir, "lodash-es"), m.Workspaces["lodash-es"].Dir)

	// Verify underscore is a symlink (transitive dep)
	assertSymlink(t, filepath.Join(nodeModulesDir, "underscore"), m.Workspaces["underscore"].Dir)

	// Verify ramda is NOT a symlink (still a directory)
	fi, err := os.Lstat(filepath.Join(nodeModulesDir, "ramda"))
	if err != nil {
		t.Fatalf("lstat ramda: %v", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Error("ramda should NOT be a symlink, but it is")
	}

	// Test ScanLinks
	links, err := ScanLinks(nodeModulesDir)
	if err != nil {
		t.Fatalf("ScanLinks: %v", err)
	}
	if len(links) != 2 {
		t.Fatalf("expected 2 symlinks, got %d", len(links))
	}

	linkedNames := make(map[string]bool)
	for _, link := range links {
		linkedNames[link.Name] = true
	}
	if !linkedNames["lodash-es"] {
		t.Error("expected lodash-es in scanned links")
	}
	if !linkedNames["underscore"] {
		t.Error("expected underscore in scanned links")
	}

	// Test RemoveLinks
	if err := RemoveLinks(nodeModulesDir); err != nil {
		t.Fatalf("RemoveLinks: %v", err)
	}

	// Verify symlinks are gone
	linksAfter, err := ScanLinks(nodeModulesDir)
	if err != nil {
		t.Fatalf("ScanLinks after clean: %v", err)
	}
	if len(linksAfter) != 0 {
		t.Errorf("expected 0 symlinks after clean, got %d", len(linksAfter))
	}
}

func TestIsWorkspaceVersion(t *testing.T) {
	tests := []struct {
		version string
		want    bool
	}{
		{"workspace:*", true},
		{"workspace:^", true},
		{"workspace:~", true},
		{"workspace:^1.0.0", true},
		{"^1.0.0", false},
		{"~1.0.0", false},
		{"1.0.0", false},
		{"*", false},
	}

	for _, tt := range tests {
		got := isWorkspaceVersion(tt.version)
		if got != tt.want {
			t.Errorf("isWorkspaceVersion(%q) = %v, want %v", tt.version, got, tt.want)
		}
	}
}

func TestDryRun(t *testing.T) {
	projectsDir := testdataDir(t)
	monorepoDir := filepath.Join(projectsDir, "monorepo-yarn4")

	m, err := LoadMonorepo(monorepoDir)
	if err != nil {
		t.Fatalf("LoadMonorepo: %v", err)
	}

	tmpDir := t.TempDir()
	nodeModulesDir := filepath.Join(tmpDir, "node_modules")
	if err := os.MkdirAll(nodeModulesDir, 0o755); err != nil {
		t.Fatalf("creating node_modules: %v", err)
	}

	// Create a fake existing dir for lodash-es
	lodashDir := filepath.Join(nodeModulesDir, "lodash-es")
	if err := os.MkdirAll(lodashDir, 0o755); err != nil {
		t.Fatalf("creating fake lodash-es: %v", err)
	}
	os.WriteFile(filepath.Join(lodashDir, "marker.txt"), []byte("real"), 0o644)

	// Dry-run should NOT create any symlinks
	if err := Link(m, nodeModulesDir, []string{"lodash-es"}, true); err != nil {
		t.Fatalf("Link dry-run: %v", err)
	}

	// lodash-es should still be a regular directory, not a symlink
	fi, err := os.Lstat(filepath.Join(nodeModulesDir, "lodash-es"))
	if err != nil {
		t.Fatalf("lstat lodash-es: %v", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Error("lodash-es should NOT be a symlink after dry-run, but it is")
	}

	// marker.txt should still exist (directory wasn't replaced)
	if _, err := os.Stat(filepath.Join(lodashDir, "marker.txt")); err != nil {
		t.Error("marker.txt should still exist after dry-run")
	}

	// underscore (transitive dep) should not exist as symlink either
	underscorePath := filepath.Join(nodeModulesDir, "underscore")
	if _, err := os.Lstat(underscorePath); err == nil {
		fi, _ := os.Lstat(underscorePath)
		if fi.Mode()&os.ModeSymlink != 0 {
			t.Error("underscore should NOT be a symlink after dry-run")
		}
	}
}

func TestScopedPackageLink(t *testing.T) {
	// Create a minimal monorepo with a scoped package in a temp dir
	tmpDir := t.TempDir()

	// Create a fake workspace package @scope/pkg
	pkgDir := filepath.Join(tmpDir, "monorepo", "packages", "scoped-pkg")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(pkgDir, "package.json"), []byte(`{"name": "@scope/pkg", "version": "1.0.0"}`), 0o644)

	// Create monorepo root package.json
	monorepoDir := filepath.Join(tmpDir, "monorepo")
	os.WriteFile(filepath.Join(monorepoDir, "package.json"), []byte(`{"private": true, "workspaces": ["packages/*"]}`), 0o644)

	m, err := LoadMonorepo(monorepoDir)
	if err != nil {
		t.Fatalf("LoadMonorepo: %v", err)
	}

	if _, ok := m.Workspaces["@scope/pkg"]; !ok {
		t.Fatalf("expected @scope/pkg workspace, got workspaces: %v", m.Workspaces)
	}

	// Create node_modules dir
	nodeModulesDir := filepath.Join(tmpDir, "app", "node_modules")
	if err := os.MkdirAll(nodeModulesDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Link the scoped package
	if err := Link(m, nodeModulesDir, []string{"@scope/pkg"}, false); err != nil {
		t.Fatalf("Link: %v", err)
	}

	// Verify @scope/ directory was created
	scopeDir := filepath.Join(nodeModulesDir, "@scope")
	fi, err := os.Stat(scopeDir)
	if err != nil {
		t.Fatalf("@scope/ directory not created: %v", err)
	}
	if !fi.IsDir() {
		t.Fatal("@scope/ should be a directory")
	}

	// Verify @scope/pkg is a symlink to the right target
	assertSymlink(t, filepath.Join(nodeModulesDir, "@scope", "pkg"), pkgDir)
}

func assertSymlink(t *testing.T, path, expectedTarget string) {
	t.Helper()
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat %s: %v", path, err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s is not a symlink", path)
	}
	target, err := os.Readlink(path)
	if err != nil {
		t.Fatalf("readlink %s: %v", path, err)
	}
	if target != expectedTarget {
		t.Errorf("symlink %s: expected target %s, got %s", path, expectedTarget, target)
	}
}
