package main

import (
	"fmt"

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
