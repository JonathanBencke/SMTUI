package tui

import (
	"github.com/JonathanBencke/ServiceManagerTUI/internal/service"
	tea "github.com/charmbracelet/bubbletea"
)

type logMsg struct {
	name string
	line string
}

type statusMsg struct {
	name   string
	status service.Status
	pid    int
}

type tickMsg struct{}

type statsMsg struct {
	stats map[string]service.ProcStats
}

// healthMsg carries the outcome of the periodic health_port check: true when
// the port accepted a connection, false when it did not. Only services with
// a configured health_port and status "running" are present as keys.
type healthMsg struct {
	health map[string]bool
}

type profileSelectedMsg struct {
	profiles []string
}

type mcpLogMsg struct {
	line string
}

type mcpToggleMsg struct {
	running bool
	err     error
}

// reloadRequestMsg asks the model to reload the configuration from disk. Sent
// by the web config server after a successful save.
type reloadRequestMsg struct{}

func LogMsg(name, line string) tea.Msg {
	return logMsg{name: name, line: line}
}

func StatusMsg(name string, status service.Status, pid int) tea.Msg {
	return statusMsg{name: name, status: status, pid: pid}
}

func MCPLogMsg(line string) tea.Msg {
	return mcpLogMsg{line: line}
}

// ReloadRequestMsg is the exported constructor used by main to trigger a reload
// from the web config server's OnSaved callback.
func ReloadRequestMsg() tea.Msg {
	return reloadRequestMsg{}
}
