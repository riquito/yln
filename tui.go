package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// workspaceItem implements list.Item and list.DefaultItem for the workspace picker.
type workspaceItem struct {
	name string
	dir  string
}

func (w workspaceItem) FilterValue() string { return w.name }
func (w workspaceItem) Title() string       { return w.name }
func (w workspaceItem) Description() string { return w.dir }

// checkboxDelegate wraps the default delegate to add a checkbox prefix.
type checkboxDelegate struct {
	inner    list.DefaultDelegate
	selected map[string]bool
}

func newCheckboxDelegate(selected map[string]bool) checkboxDelegate {
	d := list.NewDefaultDelegate()
	d.ShowDescription = true
	d.SetSpacing(0)
	return checkboxDelegate{inner: d, selected: selected}
}

func (d checkboxDelegate) Height() int  { return d.inner.Height() }
func (d checkboxDelegate) Spacing() int { return d.inner.Spacing() }

func (d checkboxDelegate) Update(msg tea.Msg, m *list.Model) tea.Cmd {
	return d.inner.Update(msg, m)
}

func (d checkboxDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	wi, ok := item.(workspaceItem)
	if !ok {
		return
	}

	isSelected := d.selected[wi.name]
	isCurrent := index == m.Index()

	checkbox := "[ ]"
	if isSelected {
		checkbox = "[x]"
	}

	titleStyle := d.inner.Styles.NormalTitle
	descStyle := d.inner.Styles.NormalDesc
	if isCurrent {
		titleStyle = d.inner.Styles.SelectedTitle
		descStyle = d.inner.Styles.SelectedDesc
	}

	title := fmt.Sprintf("%s %s", checkbox, wi.name)
	desc := fmt.Sprintf("    %s", wi.dir)

	fmt.Fprintf(w, "%s\n%s", titleStyle.Render(title), descStyle.Render(desc))
}

// tuiModel is the Bubble Tea model for the workspace picker.
type tuiModel struct {
	list     list.Model
	selected map[string]bool
	done     bool
	aborted  bool
}

func initialModel(items []list.Item, preSelected map[string]bool) tuiModel {
	selected := preSelected

	delegate := newCheckboxDelegate(selected)
	l := list.New(items, delegate, 80, 24)
	l.Title = "Select packages to link (space=toggle, enter=confirm)"
	l.SetFilteringEnabled(true)
	l.DisableQuitKeybindings()

	l.AdditionalShortHelpKeys = func() []key.Binding {
		return []key.Binding{
			key.NewBinding(key.WithKeys(" "), key.WithHelp("space", "toggle")),
			key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "confirm")),
			key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "abort")),
		}
	}

	return tuiModel{
		list:     l,
		selected: selected,
	}
}

func (m tuiModel) Init() tea.Cmd {
	return nil
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.list.SetSize(msg.Width, msg.Height)
		return m, nil

	case tea.KeyMsg:
		// Don't handle keys when filtering is active (let the filter input handle them)
		if m.list.FilterState() == list.Filtering {
			break
		}

		switch msg.String() {
		case " ":
			if item, ok := m.list.SelectedItem().(workspaceItem); ok {
				if m.selected[item.name] {
					delete(m.selected, item.name)
				} else {
					m.selected[item.name] = true
				}
			}
			return m, nil

		case "enter":
			m.done = true
			return m, tea.Quit

		case "ctrl+c", "esc":
			m.aborted = true
			return m, tea.Quit
		}
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m tuiModel) View() string {
	if m.done || m.aborted {
		return ""
	}

	var b strings.Builder
	b.WriteString(m.list.View())

	// Show selected count at the bottom
	count := len(m.selected)
	if count > 0 {
		style := lipgloss.NewStyle().Foreground(lipgloss.Color("170")).Bold(true)
		b.WriteString("\n")
		b.WriteString(style.Render(fmt.Sprintf("  %d package(s) selected", count)))
	}

	return b.String()
}

// cmdTUI launches the interactive TUI for selecting packages to link.
func cmdTUI(monorepoPath, nodeModulesDir string, dryRun bool) error {
	monorepo, err := resolveMonorepo(monorepoPath)
	if err != nil {
		return err
	}

	// Check node_modules exists (skip in dry-run mode)
	if !dryRun {
		if _, err := os.Stat(nodeModulesDir); err != nil {
			return fmt.Errorf("node_modules not found in current directory (run yarn install first)")
		}
	}

	// Load existing state to pre-select previously requested packages
	preSelected := make(map[string]bool)
	if state, err := loadLinkState(nodeModulesDir); err == nil && state != nil {
		for _, pkg := range state.Requested {
			if _, ok := monorepo.Workspaces[pkg]; ok {
				preSelected[pkg] = true
			}
		}
	}

	// Build list of workspace items, selected first then alphabetical
	var items []list.Item
	for name, ws := range monorepo.Workspaces {
		items = append(items, workspaceItem{name: name, dir: ws.Dir})
	}
	sort.Slice(items, func(i, j int) bool {
		ni := items[i].(workspaceItem).name
		nj := items[j].(workspaceItem).name
		si := preSelected[ni]
		sj := preSelected[nj]
		if si != sj {
			return si // selected items first
		}
		return ni < nj
	})

	if len(items) == 0 {
		return fmt.Errorf("no workspace packages found in monorepo")
	}

	model := initialModel(items, preSelected)
	p := tea.NewProgram(model, tea.WithAltScreen())
	finalModel, err := p.Run()
	if err != nil {
		return fmt.Errorf("running TUI: %w", err)
	}

	m := finalModel.(tuiModel)
	if m.aborted {
		printInfo("Aborted.")
		return nil
	}

	// Collect selected packages
	var packages []string
	for name := range m.selected {
		if _, ok := monorepo.Workspaces[name]; ok {
			packages = append(packages, name)
		}
	}
	sort.Strings(packages)

	if len(packages) == 0 && len(preSelected) == 0 {
		printInfo("No packages selected.")
		return nil
	}

	// Remove symlinks that are no longer needed
	removedAny := false
	if !dryRun {
		newResolved := make(map[string]bool)
		for _, name := range ResolveLinkSet(monorepo, packages) {
			newResolved[name] = true
		}

		oldLinks, err := ScanLinks(nodeModulesDir)
		if err != nil {
			return err
		}

		for _, link := range oldLinks {
			if !newResolved[link.Name] {
				path := filepath.Join(nodeModulesDir, link.Name)
				if err := os.Remove(path); err != nil {
					return fmt.Errorf("removing symlink %s: %w", path, err)
				}
				printRemoved(link.Name)
				removedAny = true
			}
		}

		if removedAny && len(packages) == 0 {
			if err := deleteLinkState(nodeModulesDir); err != nil {
				return err
			}
		}
	}

	if len(packages) > 0 {
		if dryRun {
			printHeader("Would link packages (dry run):")
		} else {
			printHeader("Linking packages:")
		}
		if err := Link(monorepo, nodeModulesDir, packages, dryRun); err != nil {
			return err
		}
	}

	if !dryRun {
		if len(packages) > 0 {
			if err := saveLinkState(nodeModulesDir, &LinkState{
				Monorepo:  monorepo.RootDir,
				Requested: packages,
			}); err != nil {
				return err
			}
		}
	}

	if removedAny {
		fmt.Println(warnStyle.Render("Run 'yarn install' to restore removed packages."))
	}

	return nil
}
