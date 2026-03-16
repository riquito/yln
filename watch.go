package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
)

const watchLockFile = ".yln-watch.lock"

// acquireWatchLock creates a PID lockfile. Returns a cleanup function.
func acquireWatchLock(nmDir string) (func(), error) {
	path := filepath.Join(nmDir, watchLockFile)

	// Check for existing lock
	data, err := os.ReadFile(path)
	if err == nil {
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err == nil {
			// Check if process is still alive
			proc, err := os.FindProcess(pid)
			if err == nil {
				// Signal 0 checks existence without actually signaling
				if err := proc.Signal(syscall.Signal(0)); err == nil {
					return nil, fmt.Errorf("another yln watch is already running (pid %d)", pid)
				}
			}
		}
		// Stale lock, remove it
		os.Remove(path)
	}

	// Write our PID
	if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		return nil, fmt.Errorf("creating watch lock: %w", err)
	}

	cleanup := func() {
		os.Remove(path)
	}
	return cleanup, nil
}

func releaseWatchLock(nmDir string) {
	os.Remove(filepath.Join(nmDir, watchLockFile))
}

type watchedPackage struct {
	name        string
	symlinkPath string   // e.g. node_modules/lodash-es
	targetDir   string   // monorepo workspace dir
	pkgJSONPath string   // targetDir/package.json
	workDeps    []string // current workspace:* dep names
}

// watchEvent describes a change detected by the watcher.
type watchEvent struct {
	kind    string // "symlink_gone", "target_gone", "pkg_json_changed", "monorepo_reloaded"
	pkgName string
}

// cmdWatch starts watching currently linked packages for changes.
func cmdWatch(args []string, nmDir string) error {
	var monorepoPath string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--monorepo":
			if i+1 >= len(args) {
				return fmt.Errorf("--monorepo requires a path argument")
			}
			i++
			monorepoPath = args[i]
		case "--help", "-h":
			fmt.Print(usage)
			return nil
		default:
			return fmt.Errorf("unknown argument: %s", args[i])
		}
	}

	// Acquire watch lock to prevent duplicate watchers
	unlock, err := acquireWatchLock(nmDir)
	if err != nil {
		return err
	}
	defer unlock()

	monorepo, err := resolveMonorepo(monorepoPath)
	if err != nil {
		return err
	}

	links, err := ScanLinks(nmDir)
	if err != nil {
		return err
	}
	if len(links) == 0 {
		printInfo("No links to watch. Link some packages first.")
		return nil
	}

	watchSet := buildWatchSet(links, monorepo)
	if len(watchSet) == 0 {
		printInfo("No links to watch. Link some packages first.")
		return nil
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("creating watcher: %w", err)
	}
	defer watcher.Close()

	if err := addWatches(watcher, watchSet, nmDir); err != nil {
		return fmt.Errorf("setting up watches: %w", err)
	}

	// Watch .git/HEAD for branch switches (best-effort)
	gitHeadPath := filepath.Join(monorepo.RootDir, ".git", "HEAD")
	watcher.Add(gitHeadPath) // ignore error (bare repo, worktree, etc.)

	printWatchStatus(watchSet)

	// Set up signal handler
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	return runWatchLoop(watcher, watchSet, &monorepo, nmDir, gitHeadPath, sigCh, nil)
}

func buildWatchSet(links []LinkInfo, monorepo *Monorepo) map[string]*watchedPackage {
	watchSet := make(map[string]*watchedPackage)
	for _, link := range links {
		ws, ok := monorepo.Workspaces[link.Name]
		if !ok {
			continue // symlink target not in monorepo, skip
		}
		watchSet[link.Name] = &watchedPackage{
			name:        link.Name,
			symlinkPath: link.Target, // this is the absolute target path
			targetDir:   ws.Dir,
			pkgJSONPath: filepath.Join(ws.Dir, "package.json"),
			workDeps:    ws.WorkDeps,
		}
	}
	return watchSet
}

func addWatches(watcher *fsnotify.Watcher, watchSet map[string]*watchedPackage, nmDir string) error {
	// Watch the node_modules directory itself for symlink replacements
	if err := watcher.Add(nmDir); err != nil {
		return fmt.Errorf("watching %s: %w", nmDir, err)
	}

	// Watch scoped dirs too (e.g. node_modules/@scope)
	scopeDirs := make(map[string]bool)
	for name := range watchSet {
		if name[0] == '@' {
			scopeDir := filepath.Join(nmDir, filepath.Dir(name))
			scopeDirs[scopeDir] = true
		}
	}
	for dir := range scopeDirs {
		if err := watcher.Add(dir); err != nil {
			return fmt.Errorf("watching %s: %w", dir, err)
		}
	}

	// Watch each package's target dir and package.json
	for _, pkg := range watchSet {
		if err := watcher.Add(pkg.targetDir); err != nil {
			return fmt.Errorf("watching %s: %w", pkg.targetDir, err)
		}
	}

	return nil
}

func printWatchStatus(watchSet map[string]*watchedPackage) {
	names := make([]string, 0, len(watchSet))
	for name := range watchSet {
		names = append(names, name)
	}
	sort.Strings(names)
	fmt.Printf("Watching %d package(s): %s\n", len(watchSet), strings.Join(names, ", "))
	printInfo("Press Ctrl+C to stop.")
}

// runWatchLoop is the core event loop. eventCh, if non-nil, receives events
// for testing purposes. When eventCh is set, the loop exits after processing
// one batch of events instead of looping forever.
func runWatchLoop(
	watcher *fsnotify.Watcher,
	watchSet map[string]*watchedPackage,
	monorepo **Monorepo,
	nmDir string,
	gitHeadPath string,
	sigCh <-chan os.Signal,
	eventCh chan<- watchEvent,
) error {
	debounce := 100 * time.Millisecond
	var timer *time.Timer
	pendingPaths := make(map[string]bool)

	for {
		select {
		case <-sigCh:
			fmt.Println("\nStopped.")
			return nil

		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			pendingPaths[event.Name] = true
			// Reset debounce timer
			if timer != nil {
				timer.Stop()
			}
			timer = time.NewTimer(debounce)

		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			fmt.Fprintf(os.Stderr, "watch error: %v\n", err)

		case <-timerChan(timer):
			paths := make([]string, 0, len(pendingPaths))
			for p := range pendingPaths {
				paths = append(paths, p)
			}
			pendingPaths = make(map[string]bool)

			processWatchEvents(paths, watcher, watchSet, monorepo, nmDir, gitHeadPath, eventCh)

			if len(watchSet) == 0 {
				printInfo("All watched packages are gone. Exiting.")
				if eventCh != nil {
					close(eventCh)
				}
				return nil
			}

			// In test mode, exit after processing one batch
			if eventCh != nil {
				close(eventCh)
				return nil
			}
		}
	}
}

func timerChan(t *time.Timer) <-chan time.Time {
	if t == nil {
		return nil
	}
	return t.C
}

func processWatchEvents(
	paths []string,
	watcher *fsnotify.Watcher,
	watchSet map[string]*watchedPackage,
	monorepo **Monorepo,
	nmDir string,
	gitHeadPath string,
	eventCh chan<- watchEvent,
) {
	// Check if .git/HEAD changed (branch switch)
	if gitHeadPath != "" {
		for _, path := range paths {
			if path == gitHeadPath {
				handleMonorepoReload(monorepo, watcher, watchSet, nmDir, gitHeadPath, eventCh)
				return
			}
		}
	}

	for _, path := range paths {
		// Check if this path is a symlink in node_modules being changed
		for name, pkg := range watchSet {
			symlinkPath := filepath.Join(nmDir, name)
			if path == symlinkPath || filepath.Dir(path) == filepath.Dir(symlinkPath) && filepath.Base(path) == filepath.Base(symlinkPath) {
				handleSymlinkChange(name, pkg, nmDir, watcher, watchSet, eventCh)
				continue
			}

			// Check if this is a package.json change in the target dir
			if path == pkg.pkgJSONPath || path == filepath.Join(pkg.targetDir, "package.json") {
				handlePackageJSONChange(name, pkg, watcher, watchSet, *monorepo, nmDir, eventCh)
				continue
			}

			// Check if the target dir was removed
			if path == pkg.targetDir {
				if _, err := os.Stat(pkg.targetDir); os.IsNotExist(err) {
					handleTargetDirRemoved(name, pkg, nmDir, watcher, watchSet, *monorepo, eventCh)
				}
			}
		}
	}
}

func handleSymlinkChange(
	name string,
	pkg *watchedPackage,
	nmDir string,
	watcher *fsnotify.Watcher,
	watchSet map[string]*watchedPackage,
	eventCh chan<- watchEvent,
) {
	symlinkPath := filepath.Join(nmDir, name)
	fi, err := os.Lstat(symlinkPath)

	if err != nil {
		// Symlink gone entirely
		fmt.Printf("  %s %s was removed.\n", warnStyle.Render("!"), name)
		removeFromWatchSet(name, pkg, watcher, watchSet)
		if eventCh != nil {
			eventCh <- watchEvent{kind: "symlink_gone", pkgName: name}
		}
		return
	}

	if fi.Mode()&os.ModeSymlink == 0 {
		// Symlink replaced by a directory (yarn install clobbered it)
		fmt.Printf("  %s %s was overwritten (yarn install?). Re-link with `yln`.\n",
			warnStyle.Render("!"), name)
		removeFromWatchSet(name, pkg, watcher, watchSet)
		if eventCh != nil {
			eventCh <- watchEvent{kind: "symlink_gone", pkgName: name}
		}
		return
	}
}

func handlePackageJSONChange(
	name string,
	pkg *watchedPackage,
	watcher *fsnotify.Watcher,
	watchSet map[string]*watchedPackage,
	monorepo *Monorepo,
	nmDir string,
	eventCh chan<- watchEvent,
) {
	pkgJSON, err := readPackageJSON(pkg.pkgJSONPath)
	if err != nil {
		return // file might be mid-write, ignore
	}

	newDeps := findWorkspaceDeps(pkgJSON)
	if depsEqual(pkg.workDeps, newDeps) {
		return // no change in workspace deps
	}

	fmt.Printf("  %s %s dependencies changed. Re-linking...\n",
		successStyle.Render("↻"), name)

	// Load state to get the requested set
	state, _ := loadLinkState(nmDir)

	// Determine the requested packages to re-resolve from
	var requested []string
	if state != nil {
		requested = state.Requested
	} else {
		// Fallback: use all currently watched packages
		for n := range watchSet {
			requested = append(requested, n)
		}
	}

	// Snapshot current symlinks before re-linking
	oldLinks, _ := ScanLinks(nmDir)
	oldSet := make(map[string]bool)
	for _, link := range oldLinks {
		oldSet[link.Name] = true
	}

	// Update monorepo workspace info
	if ws, ok := monorepo.Workspaces[name]; ok {
		ws.WorkDeps = newDeps
		ws.PkgJSON = pkgJSON
	}

	// Re-link from the full requested set
	if err := Link(monorepo, nmDir, requested, false); err != nil {
		fmt.Fprintf(os.Stderr, "  re-link error: %v\n", err)
		return
	}

	// Compute the new expected set and remove orphans
	newExpected := ResolveLinkSet(monorepo, requested)
	newSet := make(map[string]bool)
	for _, n := range newExpected {
		newSet[n] = true
	}
	for n := range oldSet {
		if !newSet[n] {
			symlinkPath := filepath.Join(nmDir, n)
			os.Remove(symlinkPath)
			if wp, ok := watchSet[n]; ok {
				removeFromWatchSet(n, wp, watcher, watchSet)
			}
			printRemoved(n)
		}
	}

	// Save updated state
	if state != nil {
		_ = saveLinkState(nmDir, &LinkState{
			Monorepo:  state.Monorepo,
			Requested: requested,
		})
	}

	// Update watch set with new links
	pkg.workDeps = newDeps
	newLinks, err := ScanLinks(nmDir)
	if err == nil {
		updateWatchSet(watcher, watchSet, newLinks, monorepo, nmDir)
	}

	if eventCh != nil {
		eventCh <- watchEvent{kind: "pkg_json_changed", pkgName: name}
	}
}

func handleTargetDirRemoved(
	name string,
	pkg *watchedPackage,
	nmDir string,
	watcher *fsnotify.Watcher,
	watchSet map[string]*watchedPackage,
	monorepo *Monorepo,
	eventCh chan<- watchEvent,
) {
	fmt.Printf("  %s %s workspace directory was deleted.\n", warnStyle.Render("!"), name)

	// Remove the symlink from node_modules if it still exists
	symlinkPath := filepath.Join(nmDir, name)
	os.Remove(symlinkPath)
	removeFromWatchSet(name, pkg, watcher, watchSet)

	// Load state to clean up transitive deps
	state, _ := loadLinkState(nmDir)
	if state != nil {
		// Remove deleted package from requested set
		var remaining []string
		for _, r := range state.Requested {
			if r != name {
				remaining = append(remaining, r)
			}
		}

		// Resolve what should be linked from the remaining requested set
		newExpected := make(map[string]bool)
		if len(remaining) > 0 {
			for _, n := range ResolveLinkSet(monorepo, remaining) {
				newExpected[n] = true
			}
		}

		// Remove orphaned symlinks (packages no longer in the expected set)
		links, _ := ScanLinks(nmDir)
		for _, link := range links {
			if !newExpected[link.Name] {
				os.Remove(filepath.Join(nmDir, link.Name))
				if wp, ok := watchSet[link.Name]; ok {
					removeFromWatchSet(link.Name, wp, watcher, watchSet)
				}
				printRemoved(link.Name)
			}
		}

		// Save updated state
		_ = saveLinkState(nmDir, &LinkState{
			Monorepo:  state.Monorepo,
			Requested: remaining,
		})
	}

	if eventCh != nil {
		eventCh <- watchEvent{kind: "target_gone", pkgName: name}
	}
}

func handleMonorepoReload(
	monorepo **Monorepo,
	watcher *fsnotify.Watcher,
	watchSet map[string]*watchedPackage,
	nmDir string,
	gitHeadPath string,
	eventCh chan<- watchEvent,
) {
	newMonorepo, err := LoadMonorepo((*monorepo).RootDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  reload monorepo: %v\n", err)
		return
	}

	// Load state to get the requested set
	state, _ := loadLinkState(nmDir)
	var requested []string
	if state != nil {
		requested = state.Requested
	} else {
		for n := range watchSet {
			requested = append(requested, n)
		}
	}

	// Snapshot current symlinks
	oldLinks, _ := ScanLinks(nmDir)
	oldSet := make(map[string]bool)
	for _, link := range oldLinks {
		oldSet[link.Name] = true
	}

	// Re-link with the new monorepo data
	if err := Link(newMonorepo, nmDir, requested, false); err != nil {
		fmt.Fprintf(os.Stderr, "  re-link after reload: %v\n", err)
		return
	}

	// Remove orphaned symlinks
	newExpected := ResolveLinkSet(newMonorepo, requested)
	newSet := make(map[string]bool)
	for _, n := range newExpected {
		newSet[n] = true
	}
	for n := range oldSet {
		if !newSet[n] {
			os.Remove(filepath.Join(nmDir, n))
			printRemoved(n)
		}
	}

	// Rebuild watch set: remove old watches, clear map, rebuild
	for name, pkg := range watchSet {
		watcher.Remove(pkg.targetDir)
		delete(watchSet, name)
	}

	newLinks, _ := ScanLinks(nmDir)
	for _, link := range newLinks {
		ws, ok := newMonorepo.Workspaces[link.Name]
		if !ok {
			continue
		}
		pkg := &watchedPackage{
			name:        link.Name,
			symlinkPath: link.Target,
			targetDir:   ws.Dir,
			pkgJSONPath: filepath.Join(ws.Dir, "package.json"),
			workDeps:    ws.WorkDeps,
		}
		watchSet[link.Name] = pkg
		watcher.Add(pkg.targetDir)
	}

	// Re-watch .git/HEAD (git may replace the file on branch switch)
	watcher.Add(gitHeadPath)

	// Update caller's monorepo reference
	*monorepo = newMonorepo

	fmt.Printf("  %s Monorepo reloaded after branch switch.\n", successStyle.Render("↻"))

	if state != nil {
		_ = saveLinkState(nmDir, &LinkState{
			Monorepo:  newMonorepo.RootDir,
			Requested: requested,
		})
	}

	if eventCh != nil {
		eventCh <- watchEvent{kind: "monorepo_reloaded"}
	}
}

func removeFromWatchSet(
	name string,
	pkg *watchedPackage,
	watcher *fsnotify.Watcher,
	watchSet map[string]*watchedPackage,
) {
	// Best-effort remove watches (may already be gone)
	watcher.Remove(pkg.targetDir)
	delete(watchSet, name)
}

func updateWatchSet(
	watcher *fsnotify.Watcher,
	watchSet map[string]*watchedPackage,
	newLinks []LinkInfo,
	monorepo *Monorepo,
	nmDir string,
) {
	// Add any newly linked packages to the watch set
	for _, link := range newLinks {
		if _, exists := watchSet[link.Name]; exists {
			continue
		}
		ws, ok := monorepo.Workspaces[link.Name]
		if !ok {
			continue
		}
		pkg := &watchedPackage{
			name:        link.Name,
			symlinkPath: link.Target,
			targetDir:   ws.Dir,
			pkgJSONPath: filepath.Join(ws.Dir, "package.json"),
			workDeps:    ws.WorkDeps,
		}
		watchSet[link.Name] = pkg
		watcher.Add(pkg.targetDir)
	}
}

func depsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	aCopy := make([]string, len(a))
	bCopy := make([]string, len(b))
	copy(aCopy, a)
	copy(bCopy, b)
	sort.Strings(aCopy)
	sort.Strings(bCopy)
	for i := range aCopy {
		if aCopy[i] != bCopy[i] {
			return false
		}
	}
	return true
}
