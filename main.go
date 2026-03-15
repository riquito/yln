package main

import (
	"fmt"
	"os"
	"path/filepath"
)

const usage = `yln — yarn linker

Usage:
  yln <pkg1> [pkg2...] [--monorepo <path>]   Link packages from a monorepo
  yln list                                    Show currently linked packages
  yln clean                                   Remove all symlinks

Options:
  --monorepo <path>   Path to the monorepo root (overrides config file)

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

	// Find node_modules in cwd
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting cwd: %w", err)
	}
	nodeModulesDir := filepath.Join(cwd, "node_modules")

	switch args[0] {
	case "list":
		return cmdList(nodeModulesDir)
	case "clean":
		return cmdClean(nodeModulesDir)
	case "--help", "-h":
		fmt.Print(usage)
		return nil
	default:
		return cmdLink(args, nodeModulesDir)
	}
}

func cmdList(nodeModulesDir string) error {
	links, err := ScanLinks(nodeModulesDir)
	if err != nil {
		return err
	}

	if len(links) == 0 {
		fmt.Println("No symlinks found in node_modules/")
		return nil
	}

	for _, link := range links {
		fmt.Printf("  %s -> %s\n", link.Name, link.Target)
	}
	return nil
}

func cmdClean(nodeModulesDir string) error {
	return RemoveLinks(nodeModulesDir)
}

func cmdLink(args []string, nodeModulesDir string) error {
	var monorepoPath string
	var packages []string

	for i := 0; i < len(args); i++ {
		if args[i] == "--monorepo" {
			if i+1 >= len(args) {
				return fmt.Errorf("--monorepo requires a path argument")
			}
			i++
			monorepoPath = args[i]
		} else if args[i] == "--help" || args[i] == "-h" {
			fmt.Print(usage)
			return nil
		} else {
			packages = append(packages, args[i])
		}
	}

	if len(packages) == 0 {
		return fmt.Errorf("no packages specified\n\n%s", usage)
	}

	// Resolve monorepo path: --monorepo > config > error
	if monorepoPath == "" {
		cfg, err := LoadConfig()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}
		if cfg != nil {
			monorepoPath = cfg.Monorepo
		}
	}

	if monorepoPath == "" {
		return fmt.Errorf("no monorepo path specified (use --monorepo or set it in ~/.config/yln/config.toml)")
	}

	// Resolve to absolute path
	monorepoPath, err := filepath.Abs(monorepoPath)
	if err != nil {
		return fmt.Errorf("resolving monorepo path: %w", err)
	}

	// Check node_modules exists
	if _, err := os.Stat(nodeModulesDir); err != nil {
		return fmt.Errorf("node_modules not found in current directory (run yarn install first)")
	}

	// Load monorepo
	monorepo, err := LoadMonorepo(monorepoPath)
	if err != nil {
		return fmt.Errorf("loading monorepo: %w", err)
	}

	// Validate requested packages exist in the monorepo
	for _, pkg := range packages {
		if _, ok := monorepo.Workspaces[pkg]; !ok {
			return fmt.Errorf("package %q not found in monorepo workspaces", pkg)
		}
	}

	fmt.Println("Linking packages:")
	return Link(monorepo, nodeModulesDir, packages)
}
