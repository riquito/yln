package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"syscall"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
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

// setTestStashDir overrides the stash file path to use a temporary directory.
// Returns a cleanup function that restores the original.
func setTestStashDir(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	stashPath := filepath.Join(tmpDir, "stash.json")
	original := stashFilePathFunc
	stashFilePathFunc = func() (string, error) { return stashPath, nil }
	t.Cleanup(func() { stashFilePathFunc = original })
	return stashPath
}

func setupLinkedNodeModules(t *testing.T, monorepo *Monorepo, packages []string) string {
	t.Helper()
	tmpDir := t.TempDir()
	nmDir := filepath.Join(tmpDir, "node_modules")
	if err := os.MkdirAll(nmDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Create fake dirs for all known packages so Link can replace them
	for _, name := range packages {
		dir := filepath.Join(nmDir, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := Link(monorepo, nmDir, packages, false); err != nil {
		t.Fatalf("Link: %v", err)
	}
	return nmDir
}

func TestStashAndPop(t *testing.T) {
	stashPath := setTestStashDir(t)

	projectsDir := testdataDir(t)
	monorepoDir := filepath.Join(projectsDir, "monorepo-yarn4")

	m, err := LoadMonorepo(monorepoDir)
	if err != nil {
		t.Fatalf("LoadMonorepo: %v", err)
	}

	nmDir := setupLinkedNodeModules(t, m, []string{"lodash-es"})

	// Verify links exist before stash
	links, _ := ScanLinks(nmDir)
	if len(links) != 2 { // lodash-es + underscore (transitive)
		t.Fatalf("expected 2 links before stash, got %d", len(links))
	}

	// Stash
	if err := cmdStash([]string{"--monorepo", monorepoDir}, nmDir); err != nil {
		t.Fatalf("cmdStash: %v", err)
	}

	// Verify symlinks removed
	links, _ = ScanLinks(nmDir)
	if len(links) != 0 {
		t.Errorf("expected 0 links after stash, got %d", len(links))
	}

	// Verify stash file exists
	if _, err := os.Stat(stashPath); err != nil {
		t.Fatalf("stash file should exist: %v", err)
	}

	// Read stash data and verify contents
	var data StashData
	b, _ := os.ReadFile(stashPath)
	if err := json.Unmarshal(b, &data); err != nil {
		t.Fatalf("parsing stash: %v", err)
	}
	if data.Monorepo != monorepoDir {
		t.Errorf("stash monorepo: expected %q, got %q", monorepoDir, data.Monorepo)
	}
	sort.Strings(data.Packages)
	if len(data.Packages) != 2 || data.Packages[0] != "lodash-es" || data.Packages[1] != "underscore" {
		t.Errorf("stash packages: expected [lodash-es underscore], got %v", data.Packages)
	}

	// Pop
	if err := cmdPop(nil, nmDir); err != nil {
		t.Fatalf("cmdPop: %v", err)
	}

	// Verify symlinks restored
	links, _ = ScanLinks(nmDir)
	if len(links) != 2 {
		t.Errorf("expected 2 links after pop, got %d", len(links))
	}

	// Verify stash file deleted
	if _, err := os.Stat(stashPath); !os.IsNotExist(err) {
		t.Error("stash file should be deleted after pop")
	}
}

func TestDoubleStash(t *testing.T) {
	stashPath := setTestStashDir(t)

	projectsDir := testdataDir(t)
	monorepoDir := filepath.Join(projectsDir, "monorepo-yarn4")

	m, err := LoadMonorepo(monorepoDir)
	if err != nil {
		t.Fatalf("LoadMonorepo: %v", err)
	}

	// First stash with lodash-es + underscore
	nmDir := setupLinkedNodeModules(t, m, []string{"lodash-es"})
	if err := cmdStash([]string{"--monorepo", monorepoDir}, nmDir); err != nil {
		t.Fatalf("first stash: %v", err)
	}

	// Second stash with ramda only
	nmDir2 := setupLinkedNodeModules(t, m, []string{"ramda"})
	if err := cmdStash([]string{"--monorepo", monorepoDir}, nmDir2); err != nil {
		t.Fatalf("second stash: %v", err)
	}

	// Stash should contain only ramda
	var data StashData
	b, _ := os.ReadFile(stashPath)
	if err := json.Unmarshal(b, &data); err != nil {
		t.Fatalf("parsing stash: %v", err)
	}
	if len(data.Packages) != 1 || data.Packages[0] != "ramda" {
		t.Errorf("double stash: expected [ramda], got %v", data.Packages)
	}
}

func TestPopEmpty(t *testing.T) {
	setTestStashDir(t)

	// Pop with no stash file should not error
	tmpDir := t.TempDir()
	nmDir := filepath.Join(tmpDir, "node_modules")
	if err := os.MkdirAll(nmDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := cmdPop(nil, nmDir); err != nil {
		t.Fatalf("cmdPop with no stash: %v", err)
	}
}

func TestPopMissingPackage(t *testing.T) {
	setTestStashDir(t)

	projectsDir := testdataDir(t)
	monorepoDir := filepath.Join(projectsDir, "monorepo-yarn4")

	m, err := LoadMonorepo(monorepoDir)
	if err != nil {
		t.Fatalf("LoadMonorepo: %v", err)
	}

	nmDir := setupLinkedNodeModules(t, m, []string{"lodash-es"})

	// Stash
	if err := cmdStash([]string{"--monorepo", monorepoDir}, nmDir); err != nil {
		t.Fatalf("cmdStash: %v", err)
	}

	// Tamper with stash: add a package that doesn't exist in monorepo
	stashPath, _ := stashFilePath()
	b, _ := os.ReadFile(stashPath)
	var data StashData
	json.Unmarshal(b, &data)
	data.Packages = append(data.Packages, "nonexistent-pkg")
	b, _ = json.Marshal(data)
	os.WriteFile(stashPath, b, 0o644)

	// Pop should succeed, skipping nonexistent-pkg
	if err := cmdPop(nil, nmDir); err != nil {
		t.Fatalf("cmdPop with missing package: %v", err)
	}

	// Verify only real packages got linked
	links, _ := ScanLinks(nmDir)
	linkedNames := make(map[string]bool)
	for _, link := range links {
		linkedNames[link.Name] = true
	}
	if !linkedNames["lodash-es"] {
		t.Error("expected lodash-es to be restored")
	}
	if !linkedNames["underscore"] {
		t.Error("expected underscore to be restored (transitive)")
	}
	if linkedNames["nonexistent-pkg"] {
		t.Error("nonexistent-pkg should not be linked")
	}
}

func setupWatchTestMonorepo(t *testing.T) (monorepoDir string, monorepo *Monorepo) {
	t.Helper()
	tmpDir := t.TempDir()
	monorepoDir = filepath.Join(tmpDir, "monorepo")

	// Create workspace packages
	for _, pkg := range []struct {
		dir  string
		json string
	}{
		{"packages/pkg-a", `{"name": "pkg-a", "version": "1.0.0", "dependencies": {"pkg-b": "workspace:*"}}`},
		{"packages/pkg-b", `{"name": "pkg-b", "version": "1.0.0"}`},
		{"packages/pkg-c", `{"name": "pkg-c", "version": "1.0.0"}`},
	} {
		dir := filepath.Join(monorepoDir, pkg.dir)
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg.json), 0o644)
	}

	// Root package.json
	os.WriteFile(filepath.Join(monorepoDir, "package.json"),
		[]byte(`{"private": true, "workspaces": ["packages/*"]}`), 0o644)

	m, err := LoadMonorepo(monorepoDir)
	if err != nil {
		t.Fatalf("LoadMonorepo: %v", err)
	}
	return monorepoDir, m
}

func TestWatchDetectsSymlinkRemoval(t *testing.T) {
	_, monorepo := setupWatchTestMonorepo(t)

	tmpDir := t.TempDir()
	nmDir := filepath.Join(tmpDir, "node_modules")
	os.MkdirAll(nmDir, 0o755)

	// Create fake dirs then link
	for _, name := range []string{"pkg-a", "pkg-b"} {
		os.MkdirAll(filepath.Join(nmDir, name), 0o755)
	}
	if err := Link(monorepo, nmDir, []string{"pkg-a"}, false); err != nil {
		t.Fatalf("Link: %v", err)
	}

	// Build watch set
	links, _ := ScanLinks(nmDir)
	watchSet := buildWatchSet(links, monorepo)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer watcher.Close()

	if err := addWatches(watcher, watchSet, nmDir); err != nil {
		t.Fatalf("addWatches: %v", err)
	}

	eventCh := make(chan watchEvent, 10)
	sigCh := make(chan os.Signal, 1)

	// Start watch loop in goroutine
	done := make(chan error, 1)
	go func() {
		done <- runWatchLoop(watcher, watchSet, &monorepo, nmDir, "", sigCh, eventCh)
	}()

	// Remove a symlink
	os.Remove(filepath.Join(nmDir, "pkg-a"))

	// Wait for event
	select {
	case evt := <-eventCh:
		if evt.kind != "symlink_gone" || evt.pkgName != "pkg-a" {
			t.Errorf("expected symlink_gone for pkg-a, got %+v", evt)
		}
	case err := <-done:
		if err != nil {
			t.Fatalf("watchLoop error: %v", err)
		}
	case <-time.After(5 * time.Second):
		sigCh <- syscall.SIGINT
		t.Fatal("timeout waiting for symlink removal event")
	}
}

func TestWatchDetectsPackageJSONChange(t *testing.T) {
	_, monorepo := setupWatchTestMonorepo(t)

	tmpDir := t.TempDir()
	nmDir := filepath.Join(tmpDir, "node_modules")
	os.MkdirAll(nmDir, 0o755)

	for _, name := range []string{"pkg-a", "pkg-b", "pkg-c"} {
		os.MkdirAll(filepath.Join(nmDir, name), 0o755)
	}
	if err := Link(monorepo, nmDir, []string{"pkg-a"}, false); err != nil {
		t.Fatalf("Link: %v", err)
	}

	links, _ := ScanLinks(nmDir)
	watchSet := buildWatchSet(links, monorepo)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer watcher.Close()

	if err := addWatches(watcher, watchSet, nmDir); err != nil {
		t.Fatalf("addWatches: %v", err)
	}

	eventCh := make(chan watchEvent, 10)
	sigCh := make(chan os.Signal, 1)

	done := make(chan error, 1)
	go func() {
		done <- runWatchLoop(watcher, watchSet, &monorepo, nmDir, "", sigCh, eventCh)
	}()

	// Modify pkg-a's package.json to add pkg-c as a workspace dep
	pkgADir := monorepo.Workspaces["pkg-a"].Dir
	newJSON := `{"name": "pkg-a", "version": "1.0.0", "dependencies": {"pkg-b": "workspace:*", "pkg-c": "workspace:*"}}`
	os.WriteFile(filepath.Join(pkgADir, "package.json"), []byte(newJSON), 0o644)

	select {
	case evt := <-eventCh:
		if evt.kind != "pkg_json_changed" || evt.pkgName != "pkg-a" {
			t.Errorf("expected pkg_json_changed for pkg-a, got %+v", evt)
		}
	case err := <-done:
		if err != nil {
			t.Fatalf("watchLoop error: %v", err)
		}
	case <-time.After(5 * time.Second):
		sigCh <- syscall.SIGINT
		t.Fatal("timeout waiting for package.json change event")
	}

	// Verify pkg-c got linked as a result
	fi, err := os.Lstat(filepath.Join(nmDir, "pkg-c"))
	if err != nil {
		t.Fatalf("pkg-c should exist: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Error("pkg-c should be a symlink after re-link")
	}
}

func TestWatchDetectsTargetDeletion(t *testing.T) {
	_, monorepo := setupWatchTestMonorepo(t)

	tmpDir := t.TempDir()
	nmDir := filepath.Join(tmpDir, "node_modules")
	os.MkdirAll(nmDir, 0o755)

	// Link only pkg-c (no transitive deps)
	os.MkdirAll(filepath.Join(nmDir, "pkg-c"), 0o755)
	if err := Link(monorepo, nmDir, []string{"pkg-c"}, false); err != nil {
		t.Fatalf("Link: %v", err)
	}

	links, _ := ScanLinks(nmDir)
	watchSet := buildWatchSet(links, monorepo)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer watcher.Close()

	if err := addWatches(watcher, watchSet, nmDir); err != nil {
		t.Fatalf("addWatches: %v", err)
	}

	eventCh := make(chan watchEvent, 10)
	sigCh := make(chan os.Signal, 1)

	done := make(chan error, 1)
	go func() {
		done <- runWatchLoop(watcher, watchSet, &monorepo, nmDir, "", sigCh, eventCh)
	}()

	// Delete the workspace directory
	os.RemoveAll(monorepo.Workspaces["pkg-c"].Dir)

	select {
	case evt := <-eventCh:
		if evt.kind != "target_gone" || evt.pkgName != "pkg-c" {
			t.Errorf("expected target_gone for pkg-c, got %+v", evt)
		}
	case err := <-done:
		if err != nil {
			t.Fatalf("watchLoop error: %v", err)
		}
	case <-time.After(5 * time.Second):
		sigCh <- syscall.SIGINT
		t.Fatal("timeout waiting for target deletion event")
	}

	// Symlink should be cleaned up
	if _, err := os.Lstat(filepath.Join(nmDir, "pkg-c")); !os.IsNotExist(err) {
		t.Error("pkg-c symlink should be removed after target deletion")
	}
}

func TestWatchExitsWhenAllGone(t *testing.T) {
	_, monorepo := setupWatchTestMonorepo(t)

	tmpDir := t.TempDir()
	nmDir := filepath.Join(tmpDir, "node_modules")
	os.MkdirAll(nmDir, 0o755)

	// Link only pkg-c
	os.MkdirAll(filepath.Join(nmDir, "pkg-c"), 0o755)
	if err := Link(monorepo, nmDir, []string{"pkg-c"}, false); err != nil {
		t.Fatalf("Link: %v", err)
	}

	links, _ := ScanLinks(nmDir)
	watchSet := buildWatchSet(links, monorepo)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer watcher.Close()

	if err := addWatches(watcher, watchSet, nmDir); err != nil {
		t.Fatalf("addWatches: %v", err)
	}

	sigCh := make(chan os.Signal, 1)

	done := make(chan error, 1)
	go func() {
		// No eventCh — loop should exit on its own when all packages gone
		done <- runWatchLoop(watcher, watchSet, &monorepo, nmDir, "", sigCh, nil)
	}()

	// Remove the only symlink
	os.Remove(filepath.Join(nmDir, "pkg-c"))

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("watchLoop should exit cleanly, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		sigCh <- syscall.SIGINT
		t.Fatal("timeout: watcher should have exited when all packages gone")
	}

	if len(watchSet) != 0 {
		t.Errorf("watchSet should be empty, has %d entries", len(watchSet))
	}
}

func TestDepsEqual(t *testing.T) {
	tests := []struct {
		a, b []string
		want bool
	}{
		{nil, nil, true},
		{[]string{"a"}, []string{"a"}, true},
		{[]string{"b", "a"}, []string{"a", "b"}, true},
		{[]string{"a"}, []string{"b"}, false},
		{[]string{"a"}, nil, false},
	}
	for _, tt := range tests {
		got := depsEqual(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("depsEqual(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

// setTestStateDir overrides the state file path to use the given node_modules dir
// (or a temp dir). Returns the state file path for assertions.
func setTestStateDir(t *testing.T, nmDir string) string {
	t.Helper()
	original := stateFilePathFunc
	stateFilePathFunc = func(dir string) string {
		return filepath.Join(nmDir, stateFileName)
	}
	t.Cleanup(func() { stateFilePathFunc = original })
	return filepath.Join(nmDir, stateFileName)
}

func TestLinkStateWrittenOnLink(t *testing.T) {
	projectsDir := testdataDir(t)
	monorepoDir := filepath.Join(projectsDir, "monorepo-yarn4")

	m, err := LoadMonorepo(monorepoDir)
	if err != nil {
		t.Fatalf("LoadMonorepo: %v", err)
	}

	tmpDir := t.TempDir()
	nmDir := filepath.Join(tmpDir, "node_modules")
	os.MkdirAll(nmDir, 0o755)

	// Create fake dirs
	for _, name := range []string{"lodash-es", "underscore"} {
		os.MkdirAll(filepath.Join(nmDir, name), 0o755)
	}

	statePath := setTestStateDir(t, nmDir)

	// Link lodash-es (which transitively pulls in underscore)
	if err := Link(m, nmDir, []string{"lodash-es"}, false); err != nil {
		t.Fatalf("Link: %v", err)
	}

	// Save state with only the requested package
	if err := saveLinkState(nmDir, &LinkState{
		Monorepo:  m.RootDir,
		Requested: []string{"lodash-es"},
	}); err != nil {
		t.Fatalf("saveLinkState: %v", err)
	}

	// Read and verify state file
	state, err := loadLinkState(nmDir)
	if err != nil {
		t.Fatalf("loadLinkState: %v", err)
	}
	if state == nil {
		t.Fatal("state should not be nil")
	}

	// State should contain only "lodash-es", NOT "underscore"
	if len(state.Requested) != 1 || state.Requested[0] != "lodash-es" {
		t.Errorf("state.Requested: expected [lodash-es], got %v", state.Requested)
	}

	// But both symlinks should exist
	assertSymlink(t, filepath.Join(nmDir, "lodash-es"), m.Workspaces["lodash-es"].Dir)
	assertSymlink(t, filepath.Join(nmDir, "underscore"), m.Workspaces["underscore"].Dir)

	// Verify the state file is at the right location
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("state file should exist at %s: %v", statePath, err)
	}
}

func TestLinkStateDeletedOnClean(t *testing.T) {
	projectsDir := testdataDir(t)
	monorepoDir := filepath.Join(projectsDir, "monorepo-yarn4")

	m, err := LoadMonorepo(monorepoDir)
	if err != nil {
		t.Fatalf("LoadMonorepo: %v", err)
	}

	tmpDir := t.TempDir()
	nmDir := filepath.Join(tmpDir, "node_modules")
	os.MkdirAll(nmDir, 0o755)
	for _, name := range []string{"lodash-es", "underscore"} {
		os.MkdirAll(filepath.Join(nmDir, name), 0o755)
	}

	statePath := setTestStateDir(t, nmDir)

	// Link and save state
	if err := Link(m, nmDir, []string{"lodash-es"}, false); err != nil {
		t.Fatalf("Link: %v", err)
	}
	if err := saveLinkState(nmDir, &LinkState{
		Monorepo:  m.RootDir,
		Requested: []string{"lodash-es"},
	}); err != nil {
		t.Fatalf("saveLinkState: %v", err)
	}

	// Verify state file exists
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("state file should exist: %v", err)
	}

	// Clean
	if err := cmdClean(nmDir); err != nil {
		t.Fatalf("cmdClean: %v", err)
	}

	// State file should be gone
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Error("state file should be deleted after clean")
	}
}

func TestWatchCleansRemovedDeps(t *testing.T) {
	_, monorepo := setupWatchTestMonorepo(t)

	tmpDir := t.TempDir()
	nmDir := filepath.Join(tmpDir, "node_modules")
	os.MkdirAll(nmDir, 0o755)

	for _, name := range []string{"pkg-a", "pkg-b"} {
		os.MkdirAll(filepath.Join(nmDir, name), 0o755)
	}

	// Link pkg-a (which pulls in pkg-b transitively)
	if err := Link(monorepo, nmDir, []string{"pkg-a"}, false); err != nil {
		t.Fatalf("Link: %v", err)
	}

	// Save state with only the requested package
	setTestStateDir(t, nmDir)
	if err := saveLinkState(nmDir, &LinkState{
		Monorepo:  monorepo.RootDir,
		Requested: []string{"pkg-a"},
	}); err != nil {
		t.Fatalf("saveLinkState: %v", err)
	}

	// Verify both symlinks exist
	assertSymlink(t, filepath.Join(nmDir, "pkg-a"), monorepo.Workspaces["pkg-a"].Dir)
	assertSymlink(t, filepath.Join(nmDir, "pkg-b"), monorepo.Workspaces["pkg-b"].Dir)

	links, _ := ScanLinks(nmDir)
	watchSet := buildWatchSet(links, monorepo)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer watcher.Close()

	if err := addWatches(watcher, watchSet, nmDir); err != nil {
		t.Fatalf("addWatches: %v", err)
	}

	eventCh := make(chan watchEvent, 10)
	sigCh := make(chan os.Signal, 1)

	done := make(chan error, 1)
	go func() {
		done <- runWatchLoop(watcher, watchSet, &monorepo, nmDir, "", sigCh, eventCh)
	}()

	// Modify pkg-a's package.json to remove pkg-b dependency
	pkgADir := monorepo.Workspaces["pkg-a"].Dir
	newJSON := `{"name": "pkg-a", "version": "1.0.0"}`
	os.WriteFile(filepath.Join(pkgADir, "package.json"), []byte(newJSON), 0o644)

	select {
	case evt := <-eventCh:
		if evt.kind != "pkg_json_changed" || evt.pkgName != "pkg-a" {
			t.Errorf("expected pkg_json_changed for pkg-a, got %+v", evt)
		}
	case err := <-done:
		if err != nil {
			t.Fatalf("watchLoop error: %v", err)
		}
	case <-time.After(5 * time.Second):
		sigCh <- syscall.SIGINT
		t.Fatal("timeout waiting for package.json change event")
	}

	// pkg-b symlink should be removed (it was a transitive dep that's no longer needed)
	if _, err := os.Lstat(filepath.Join(nmDir, "pkg-b")); !os.IsNotExist(err) {
		t.Error("pkg-b symlink should be removed after pkg-a drops the dependency")
	}

	// pkg-a should still be linked
	assertSymlink(t, filepath.Join(nmDir, "pkg-a"), monorepo.Workspaces["pkg-a"].Dir)
}

func TestWatchCleansTransitiveDepsOnTargetDeletion(t *testing.T) {
	_, monorepo := setupWatchTestMonorepo(t)

	tmpDir := t.TempDir()
	nmDir := filepath.Join(tmpDir, "node_modules")
	os.MkdirAll(nmDir, 0o755)

	for _, name := range []string{"pkg-a", "pkg-b"} {
		os.MkdirAll(filepath.Join(nmDir, name), 0o755)
	}

	// Link pkg-a (which pulls in pkg-b transitively)
	if err := Link(monorepo, nmDir, []string{"pkg-a"}, false); err != nil {
		t.Fatalf("Link: %v", err)
	}

	// Save state
	setTestStateDir(t, nmDir)
	if err := saveLinkState(nmDir, &LinkState{
		Monorepo:  monorepo.RootDir,
		Requested: []string{"pkg-a"},
	}); err != nil {
		t.Fatalf("saveLinkState: %v", err)
	}

	links, _ := ScanLinks(nmDir)
	watchSet := buildWatchSet(links, monorepo)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer watcher.Close()

	if err := addWatches(watcher, watchSet, nmDir); err != nil {
		t.Fatalf("addWatches: %v", err)
	}

	eventCh := make(chan watchEvent, 10)
	sigCh := make(chan os.Signal, 1)

	done := make(chan error, 1)
	go func() {
		done <- runWatchLoop(watcher, watchSet, &monorepo, nmDir, "", sigCh, eventCh)
	}()

	// Delete pkg-a's workspace directory
	os.RemoveAll(monorepo.Workspaces["pkg-a"].Dir)

	select {
	case evt := <-eventCh:
		if evt.kind != "target_gone" || evt.pkgName != "pkg-a" {
			t.Errorf("expected target_gone for pkg-a, got %+v", evt)
		}
	case err := <-done:
		if err != nil {
			t.Fatalf("watchLoop error: %v", err)
		}
	case <-time.After(5 * time.Second):
		sigCh <- syscall.SIGINT
		t.Fatal("timeout waiting for target deletion event")
	}

	// Both pkg-a and pkg-b symlinks should be removed
	if _, err := os.Lstat(filepath.Join(nmDir, "pkg-a")); !os.IsNotExist(err) {
		t.Error("pkg-a symlink should be removed after target deletion")
	}
	if _, err := os.Lstat(filepath.Join(nmDir, "pkg-b")); !os.IsNotExist(err) {
		t.Error("pkg-b symlink should be removed (transitive dep of deleted pkg-a)")
	}
}

func TestStashUsesRequestedPackages(t *testing.T) {
	stashPath := setTestStashDir(t)

	projectsDir := testdataDir(t)
	monorepoDir := filepath.Join(projectsDir, "monorepo-yarn4")

	m, err := LoadMonorepo(monorepoDir)
	if err != nil {
		t.Fatalf("LoadMonorepo: %v", err)
	}

	nmDir := setupLinkedNodeModules(t, m, []string{"lodash-es"})

	// Save state with only the requested package (not transitive deps)
	setTestStateDir(t, nmDir)
	if err := saveLinkState(nmDir, &LinkState{
		Monorepo:  m.RootDir,
		Requested: []string{"lodash-es"},
	}); err != nil {
		t.Fatalf("saveLinkState: %v", err)
	}

	// Stash should use the requested set from state
	if err := cmdStash([]string{"--monorepo", monorepoDir}, nmDir); err != nil {
		t.Fatalf("cmdStash: %v", err)
	}

	// Read stash data
	var data StashData
	b, _ := os.ReadFile(stashPath)
	if err := json.Unmarshal(b, &data); err != nil {
		t.Fatalf("parsing stash: %v", err)
	}

	// Should contain only "lodash-es" (not "underscore")
	if len(data.Packages) != 1 || data.Packages[0] != "lodash-es" {
		t.Errorf("stash packages: expected [lodash-es], got %v", data.Packages)
	}
}

func TestWatchReloadsMonorepoOnHEADChange(t *testing.T) {
	monorepoDir, monorepo := setupWatchTestMonorepo(t)

	// Create a fake .git/HEAD file
	gitDir := filepath.Join(monorepoDir, ".git")
	os.MkdirAll(gitDir, 0o755)
	gitHeadPath := filepath.Join(gitDir, "HEAD")
	os.WriteFile(gitHeadPath, []byte("ref: refs/heads/main\n"), 0o644)

	tmpDir := t.TempDir()
	nmDir := filepath.Join(tmpDir, "node_modules")
	os.MkdirAll(nmDir, 0o755)

	// Link pkg-a (pulls in pkg-b transitively)
	for _, name := range []string{"pkg-a", "pkg-b"} {
		os.MkdirAll(filepath.Join(nmDir, name), 0o755)
	}
	if err := Link(monorepo, nmDir, []string{"pkg-a"}, false); err != nil {
		t.Fatalf("Link: %v", err)
	}

	// Save state
	setTestStateDir(t, nmDir)
	if err := saveLinkState(nmDir, &LinkState{
		Monorepo:  monorepo.RootDir,
		Requested: []string{"pkg-a"},
	}); err != nil {
		t.Fatalf("saveLinkState: %v", err)
	}

	links, _ := ScanLinks(nmDir)
	watchSet := buildWatchSet(links, monorepo)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer watcher.Close()

	if err := addWatches(watcher, watchSet, nmDir); err != nil {
		t.Fatalf("addWatches: %v", err)
	}
	watcher.Add(gitHeadPath)

	eventCh := make(chan watchEvent, 10)
	sigCh := make(chan os.Signal, 1)

	done := make(chan error, 1)
	go func() {
		done <- runWatchLoop(watcher, watchSet, &monorepo, nmDir, gitHeadPath, sigCh, eventCh)
	}()

	// Add a new workspace pkg-d to the monorepo
	pkgDDir := filepath.Join(monorepoDir, "packages", "pkg-d")
	os.MkdirAll(pkgDDir, 0o755)
	os.WriteFile(filepath.Join(pkgDDir, "package.json"),
		[]byte(`{"name": "pkg-d", "version": "1.0.0"}`), 0o644)

	// Make pkg-a depend on pkg-d
	pkgADir := monorepo.Workspaces["pkg-a"].Dir
	os.WriteFile(filepath.Join(pkgADir, "package.json"),
		[]byte(`{"name": "pkg-a", "version": "1.0.0", "dependencies": {"pkg-b": "workspace:*", "pkg-d": "workspace:*"}}`), 0o644)

	// Simulate branch switch by modifying .git/HEAD
	os.WriteFile(gitHeadPath, []byte("ref: refs/heads/feature-branch\n"), 0o644)

	select {
	case evt := <-eventCh:
		if evt.kind != "monorepo_reloaded" {
			t.Errorf("expected monorepo_reloaded, got %+v", evt)
		}
	case err := <-done:
		if err != nil {
			t.Fatalf("watchLoop error: %v", err)
		}
	case <-time.After(5 * time.Second):
		sigCh <- syscall.SIGINT
		t.Fatal("timeout waiting for monorepo_reloaded event")
	}

	// Verify pkg-d is now symlinked (new workspace discovered after reload)
	fi, err := os.Lstat(filepath.Join(nmDir, "pkg-d"))
	if err != nil {
		t.Fatalf("pkg-d should exist after reload: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Error("pkg-d should be a symlink after reload")
	}

	// pkg-a and pkg-b should still be linked
	assertSymlink(t, filepath.Join(nmDir, "pkg-a"), monorepo.Workspaces["pkg-a"].Dir)
	assertSymlink(t, filepath.Join(nmDir, "pkg-b"), monorepo.Workspaces["pkg-b"].Dir)
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
