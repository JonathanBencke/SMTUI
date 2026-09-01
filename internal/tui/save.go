package tui

import (
	"github.com/JonathanBencke/ServiceManagerTUI/internal/config"
	"github.com/JonathanBencke/ServiceManagerTUI/internal/service"
	tea "github.com/charmbracelet/bubbletea"
)

type reloadMsg struct {
	err error
}

// reloadConfig reloads services.toml and merges it into mgr in place,
// reusing existing *Service instances by name so a running service keeps
// its pid/status/logs instead of being replaced and orphaned. Used after the
// web config editor persists changes.
func reloadConfig(path string, mgr *service.Manager) tea.Cmd {
	return func() tea.Msg {
		cfg, err := config.Load(path)
		if err != nil {
			return reloadMsg{err: err}
		}
		mgr.Reload(cfg)
		return reloadMsg{}
	}
}
