package main

import (
	"fmt"
	"os"
	"path/filepath"
)

const usage = `yln — yarn linker

Usage:
  yln [--monorepo <path>] [--dry-run]                     Interactive package picker
  yln <pkg1> [pkg2...] [--monorepo <path>] [--dry-run]    Link packages from a monorepo
  yln list                                                 Show currently linked packages
  yln clean                                                Remove all symlinks
  yln stash [--monorepo <path>]                            Save links and remove them
  yln pop [--monorepo <path>]                              Restore previously stashed links

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
		return cmdLink(args, nodeModulesDir())
	}

	switch args[0] {
	case "list":
		return cmdList(nodeModulesDir())
	case "clean":
		return cmdClean(nodeModulesDir())
	case "stash":
		return cmdStash(args[1:], nodeModulesDir())
	case "pop":
		return cmdPop(args[1:], nodeModulesDir())
	case "--help", "-h":
		fmt.Print(usage)
		return nil
	default:
		return cmdLink(args, nodeModulesDir())
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

func cmdList(nodeModulesDir string) error {
	links, err := ScanLinks(nodeModulesDir)
	if err != nil {
		return err
	}

	if len(links) == 0 {
		printInfo("No symlinks found in node_modules/")
		return nil
	}

	for _, link := range links {
		fmt.Printf("  %s → %s\n", link.Name, dimStyle.Render(link.Target))
	}
	return nil
}

func cmdClean(nodeModulesDir string) error {
	return RemoveLinks(nodeModulesDir)
}

func cmdLink(args []string, nmDir string) error {
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

	// No packages specified: launch interactive TUI
	if len(packages) == 0 {
		return cmdTUI(monorepoPath, nmDir, dryRun)
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
	return Link(monorepo, nmDir, packages, dryRun)
}
