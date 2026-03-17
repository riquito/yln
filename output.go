package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	successStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("10")) // green
	warnStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("11")) // yellow
	dimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))  // gray
	boldStyle    = lipgloss.NewStyle().Bold(true)
)

func printLinked(name, target string) {
	fmt.Printf("  %s %s → %s\n", successStyle.Render("✓"), name, dimStyle.Render(target))
}

func printRemoved(name string) {
	fmt.Printf("  %s %s\n", warnStyle.Render("✗"), name)
}

func printDryRun(name, target string) {
	fmt.Printf("  %s %s → %s\n", dimStyle.Render("~"), dimStyle.Render(name), dimStyle.Render(target))
}

func printHeader(msg string) {
	fmt.Println(boldStyle.Render(msg))
}

func printInfo(msg string) {
	fmt.Println(dimStyle.Render(msg))
}

// shortenTarget replaces the monorepo prefix in a path with <alias> or <basename>.
// e.g. "/home/user/projects/my-mono/packages/pkg-a" → "<my-mono>/packages/pkg-a"
func shortenTarget(target, monorepoDir, alias string) string {
	if monorepoDir == "" {
		return target
	}
	// Ensure trailing slash for clean prefix matching
	prefix := monorepoDir
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	if !strings.HasPrefix(target, prefix) {
		return target
	}
	label := alias
	if label == "" {
		label = filepath.Base(monorepoDir)
	}
	return "<" + label + ">/" + target[len(prefix):]
}
