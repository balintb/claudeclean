package ui

import "charm.land/bubbles/v2/key"

type keyMap struct {
	Up, Down, PageUp, PageDown, Home, End key.Binding
	Collapse, Expand                      key.Binding

	Toggle, SelectAll, SelectNone, SelectFolder, SelectOrphans key.Binding

	Delete key.Binding

	Filter, Sort, View, Detail, Rescan key.Binding
	Help, Quit, ForceQuit, Back        key.Binding

	Confirm, Cancel, SwitchButton, Activate key.Binding
}

func defaultKeys() keyMap {
	return keyMap{
		Up:       key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		Down:     key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
		PageUp:   key.NewBinding(key.WithKeys("pgup", "ctrl+u"), key.WithHelp("pgup", "page up")),
		PageDown: key.NewBinding(key.WithKeys("pgdown", "ctrl+d"), key.WithHelp("pgdn", "page down")),
		Home:     key.NewBinding(key.WithKeys("home", "g"), key.WithHelp("g/home", "first")),
		End:      key.NewBinding(key.WithKeys("end", "G"), key.WithHelp("G/end", "last")),
		Collapse: key.NewBinding(key.WithKeys("left", "h"), key.WithHelp("←/h", "collapse")),
		Expand:   key.NewBinding(key.WithKeys("right", "l"), key.WithHelp("→/l", "expand")),

		Toggle:        key.NewBinding(key.WithKeys("space"), key.WithHelp("space", "select")),
		SelectAll:     key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "select all shown")),
		SelectNone:    key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "select none")),
		SelectFolder:  key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "select folder")),
		SelectOrphans: key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "select folder-gone")),

		Delete: key.NewBinding(key.WithKeys("d", "x", "delete"), key.WithHelp("d", "move to trash")),

		Filter: key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
		Sort:   key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "sort")),
		View:   key.NewBinding(key.WithKeys("v"), key.WithHelp("v", "group/flat")),
		Detail: key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "details")),
		Rescan: key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "rescan")),

		Help:      key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Quit:      key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit")),
		ForceQuit: key.NewBinding(key.WithKeys("ctrl+c")),
		Back:      key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),

		Confirm:      key.NewBinding(key.WithKeys("y", "Y"), key.WithHelp("y", "move to trash")),
		Cancel:       key.NewBinding(key.WithKeys("n", "N", "esc", "q"), key.WithHelp("n/esc", "cancel")),
		SwitchButton: key.NewBinding(key.WithKeys("left", "right", "h", "l", "tab", "shift+tab"), key.WithHelp("←/→", "switch")),
		Activate:     key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "choose")),
	}
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Toggle, k.Delete, k.Filter, k.Sort, k.View, k.SelectAll, k.Help, k.Quit}
}

func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.PageUp, k.PageDown, k.Home, k.End, k.Collapse, k.Expand},
		{k.Toggle, k.SelectAll, k.SelectNone, k.SelectFolder, k.SelectOrphans, k.Delete},
		{k.Filter, k.Sort, k.View, k.Detail, k.Rescan, k.Help, k.Quit},
	}
}
