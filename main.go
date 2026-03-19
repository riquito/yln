package main

import (
	"fmt"
	"os"
	"path/filepath"
)

const usage = `yln — yarn linker

Usage:
  yln add <pkg1> [pkg2...] [--monorepo <path>] [--dry-run]    Link packages from a monorepo
  yln rm <pkg1> [pkg2...] [--monorepo <path>]                 Remove specific package links
  yln edit [--monorepo <path>] [--dry-run]                    Interactive package picker (TUI)
  yln status                                                  Show currently linked packages
  yln clean                                                   Remove all symlinks
  yln watch [--monorepo <path>]                               Watch linked packages for changes
  yln stash [--monorepo <path>]                               Save links and remove them
  yln pop [--monorepo <path>]                                 Restore previously stashed links
  yln check                                                   Exit 1 if yln symlinks found

Options:
  --monorepo <path>   Path to the monorepo root (overrides config file)
  --dry-run           Show what would be linked without making changes

Config file: ~/.config/yln/config.toml
  monorepo = "/path/to/monorepo"
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		fmt.Print(usage)
		return nil
	}

	switch args[0] {
	case "add":
		return cmdAdd(args[1:], nodeModulesDir())
	case "edit":
		return cmdEdit(args[1:], nodeModulesDir())
	case "rm":
		return cmdRm(args[1:], nodeModulesDir())
	case "status":
		return cmdStatus(nodeModulesDir())
	case "clean":
		return cmdClean(nodeModulesDir())
	case "watch":
		return cmdWatch(args[1:], nodeModulesDir())
	case "stash":
		return cmdStash(args[1:], nodeModulesDir())
	case "pop":
		return cmdPop(args[1:], nodeModulesDir())
	case "check":
		return cmdCheck(nodeModulesDir())
	case "--help", "-h":
		fmt.Print(usage)
		return nil
	default:
		return fmt.Errorf("unknown command %q (see 'yln --help' for usage)", args[0])
	}
}

func nodeModulesDir() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "node_modules"
	}
	return filepath.Join(cwd, "node_modules")
}

// resolveMonorepo resolves --monorepo flag > config > error, loads and returns Monorepo.
func resolveMonorepo(monorepoPath string) (*Monorepo, error) {
	if monorepoPath == "" {
		cfg, err := LoadConfig()
		if err != nil {
			return nil, fmt.Errorf("loading config: %w", err)
		}
		if cfg != nil {
			monorepoPath = cfg.Monorepo
		}
	}

	if monorepoPath == "" {
		return nil, fmt.Errorf("no monorepo path specified (use --monorepo or set it in ~/.config/yln/config.toml)")
	}

	monorepoPath, err := filepath.Abs(monorepoPath)
	if err != nil {
		return nil, fmt.Errorf("resolving monorepo path: %w", err)
	}

	monorepo, err := LoadMonorepo(monorepoPath)
	if err != nil {
		return nil, fmt.Errorf("loading monorepo: %w", err)
	}

	return monorepo, nil
}

func cmdStatus(nodeModulesDir string) error {
	links, err := ScanLinks(nodeModulesDir)
	if err != nil {
		return err
	}

	if len(links) == 0 {
		printInfo("No symlinks found in node_modules/")
		return nil
	}

	monorepoDir, alias := resolveDisplayInfo(nodeModulesDir)
	for _, link := range links {
		display := shortenTarget(link.Target, monorepoDir, alias)
		fmt.Printf("  %s → %s\n", link.Name, dimStyle.Render(display))
	}
	return nil
}

// resolveDisplayInfo returns the monorepo path and alias for shortening displayed paths.
// It checks the link state first, then falls back to the config file.
func resolveDisplayInfo(nmDir string) (monorepoDir, alias string) {
	if state, err := loadLinkState(nmDir); err == nil && state != nil {
		monorepoDir = state.Monorepo
	}
	if cfg, err := LoadConfig(); err == nil && cfg != nil {
		if monorepoDir == "" {
			monorepoDir = cfg.Monorepo
		}
		alias = cfg.Alias
	}
	return
}

func cmdClean(nodeModulesDir string) error {
	if err := RemoveLinks(nodeModulesDir); err != nil {
		return err
	}
	if err := deleteLinkState(nodeModulesDir); err != nil {
		return err
	}
	fmt.Println(warnStyle.Render("Run 'yarn install' to restore removed packages."))
	return nil
}

func cmdAdd(args []string, nmDir string) error {
	var monorepoPath string
	var packages []string
	var dryRun bool

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--monorepo":
			if i+1 >= len(args) {
				return fmt.Errorf("--monorepo requires a path argument")
			}
			i++
			monorepoPath = args[i]
		case "--dry-run":
			dryRun = true
		case "--help", "-h":
			fmt.Print(usage)
			return nil
		default:
			packages = append(packages, args[i])
		}
	}

	if len(packages) == 0 {
		return fmt.Errorf("no packages specified (usage: yln add <pkg1> [pkg2...])")
	}

	monorepo, err := resolveMonorepo(monorepoPath)
	if err != nil {
		return err
	}

	// Check node_modules exists (skip in dry-run mode)
	if !dryRun {
		if _, err := os.Stat(nmDir); err != nil {
			return fmt.Errorf("node_modules not found in current directory (run yarn install first)")
		}
	}

	// Validate requested packages exist in the monorepo
	for _, pkg := range packages {
		if _, ok := monorepo.Workspaces[pkg]; !ok {
			return fmt.Errorf("package %q not found in monorepo workspaces", pkg)
		}
	}

	if dryRun {
		printHeader("Would link packages (dry run):")
	} else {
		printHeader("Linking packages:")
	}
	if err := Link(monorepo, nmDir, packages, dryRun); err != nil {
		return err
	}

	if !dryRun {
		if err := saveLinkState(nmDir, &LinkState{
			Monorepo:  monorepo.RootDir,
			Requested: packages,
		}); err != nil {
			return err
		}
	}

	return nil
}

func cmdEdit(args []string, nmDir string) error {
	var monorepoPath string
	var dryRun bool

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--monorepo":
			if i+1 >= len(args) {
				return fmt.Errorf("--monorepo requires a path argument")
			}
			i++
			monorepoPath = args[i]
		case "--dry-run":
			dryRun = true
		case "--help", "-h":
			fmt.Print(usage)
			return nil
		default:
			return fmt.Errorf("unexpected argument %q (usage: yln edit [--monorepo <path>] [--dry-run])", args[i])
		}
	}

	return cmdTUI(monorepoPath, nmDir, dryRun)
}

func cmdRm(args []string, nmDir string) error {
	var monorepoPath string
	var packages []string

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
			packages = append(packages, args[i])
		}
	}

	if len(packages) == 0 {
		return fmt.Errorf("no packages specified (usage: yln rm <pkg1> [pkg2...])")
	}

	// Verify named packages are currently linked
	links, err := ScanLinks(nmDir)
	if err != nil {
		return err
	}
	linkedSet := make(map[string]bool, len(links))
	for _, link := range links {
		linkedSet[link.Name] = true
	}
	for _, pkg := range packages {
		if !linkedSet[pkg] {
			return fmt.Errorf("package %q is not currently linked", pkg)
		}
	}

	// Remove specified symlinks
	for _, pkg := range packages {
		path := filepath.Join(nmDir, pkg)
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("removing symlink %s: %w", path, err)
		}
		printRemoved(pkg)
	}

	// Update state: remove specified packages from requested set
	state, err := loadLinkState(nmDir)
	if err != nil {
		return err
	}

	if state != nil {
		removeSet := make(map[string]bool, len(packages))
		for _, pkg := range packages {
			removeSet[pkg] = true
		}

		var remaining []string
		for _, req := range state.Requested {
			if !removeSet[req] {
				remaining = append(remaining, req)
			}
		}

		if len(remaining) == 0 {
			// Nothing left, clean up all symlinks and state
			if err := RemoveLinks(nmDir); err != nil {
				return err
			}
			if err := deleteLinkState(nmDir); err != nil {
				return err
			}
		} else {
			// Resolve the new link set and remove orphaned transitive deps
			monorepo, err := resolveMonorepo(monorepoPath)
			if err != nil {
				return err
			}
			newSet := ResolveLinkSet(monorepo, remaining)
			needed := make(map[string]bool, len(newSet))
			for _, name := range newSet {
				needed[name] = true
			}

			// Remove symlinks that are no longer needed
			currentLinks, err := ScanLinks(nmDir)
			if err != nil {
				return err
			}
			for _, link := range currentLinks {
				if !needed[link.Name] {
					path := filepath.Join(nmDir, link.Name)
					if err := os.Remove(path); err != nil {
						return fmt.Errorf("removing orphaned symlink %s: %w", path, err)
					}
					printRemoved(link.Name)
				}
			}

			if err := saveLinkState(nmDir, &LinkState{
				Monorepo:  state.Monorepo,
				Requested: remaining,
			}); err != nil {
				return err
			}
		}
	}

	fmt.Println(warnStyle.Render("Run 'yarn install' to restore removed packages."))
	return nil
}

func cmdCheck(nmDir string) error {
	links, err := ScanLinks(nmDir)
	if err != nil {
		return err
	}

	if len(links) == 0 {
		printInfo("No yln symlinks found.")
		return nil
	}

	monorepoDir, alias := resolveDisplayInfo(nmDir)
	printHeader("Found yln symlinks:")
	for _, link := range links {
		display := shortenTarget(link.Target, monorepoDir, alias)
		fmt.Printf("  %s → %s\n", link.Name, dimStyle.Render(display))
	}
	return fmt.Errorf("found %d yln symlink(s) in node_modules", len(links))
}
