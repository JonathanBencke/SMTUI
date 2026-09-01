package tui

import (
	"regexp"
	"time"

	"github.com/JonathanBencke/ServiceManagerTUI/internal/mcp"
	"github.com/JonathanBencke/ServiceManagerTUI/internal/service"
	"github.com/JonathanBencke/ServiceManagerTUI/internal/webconfig"
	tea "github.com/charmbracelet/bubbletea"
)

type Mode int

const (
	ModeNormal Mode = iota
	ModeProfileSelect
	ModeOnboarding
	ModeConfirm
	ModeLogSearch
	ModeInfo
)

type Model struct {
	manager           *service.Manager
	services          []*service.Service
	selectedLog       int
	logScroll         int
	mode              Mode
	profileCursor     int
	profiles          []string
	availableProfiles []string
	quitting          bool
	width             int
	height            int
	stats             map[string]service.ProcStats
	health            map[string]bool
	mcpServer         *mcp.MCPServer
	webServer         *webconfig.Server
	onLog             func(name, line string)
	onStatus          func(name string, status service.Status, pid int)

	cfgPath string

	// confirmAction/confirmMessage back ModeConfirm: a generic "are you sure?"
	// gate in front of destructive shortcuts (x, q/ctrl+c) that would
	// otherwise force-kill running services without warning.
	confirmAction  string
	confirmMessage string

	// logSearch* back ModeLogSearch (key `/`) and the `n`/`N` navigation in
	// ModeNormal. The search applies to whichever tab (service index, or
	// len(services) for the MCP tab) was selected when it was confirmed —
	// logSearchTab — and is silently ignored by n/N after switching tabs.
	logSearchInput    string
	logSearchQuery    string
	logSearchRe       *regexp.Regexp
	logSearchTab      int
	logSearchMatches  []int
	logSearchMatchIdx int

	// notice/noticeErr back a transient one-line feedback message shown in the
	// log panel (e.g. "Log salvo em logs/Web_....log" after `w`), auto-cleared
	// on the next 3s tick — good enough for a quick confirmation without a
	// dedicated toast/notification component.
	notice    string
	noticeErr bool
}

func NewModel(manager *service.Manager) Model {
	services := manager.Services()
	availableProfiles := []string{"none", "dev", "prod", "stg", "local"}
	var profiles []string
	if len(services) > 0 {
		profiles = services[0].Profiles()
	}
	return Model{
		manager:           manager,
		services:          services,
		selectedLog:       0,
		mode:              ModeNormal,
		availableProfiles: availableProfiles,
		profiles:          profiles,
		stats:             make(map[string]service.ProcStats),
		health:            make(map[string]bool),
	}
}

func (m *Model) SetMCPServer(srv *mcp.MCPServer) {
	m.mcpServer = srv
}

// SetWebServer wires the config web server used by the `c` key.
func (m *Model) SetWebServer(srv *webconfig.Server) {
	m.webServer = srv
}

// SetConfigPath tells the model where the services.toml lives (for reloads).
func (m *Model) SetConfigPath(cfgPath string) {
	m.cfgPath = cfgPath
}

// StartOnboarding opens the model on the welcome screen (used on first run or
// when no services are configured yet).
func (m *Model) StartOnboarding() {
	m.mode = ModeOnboarding
}

func (m Model) Init() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg {
		return tickMsg{}
	})
}

// profileCursorFor returns the index in available that matches the profile
// currently applied to the services (profiles[0], or "none" when empty), so
// opening the profile picker starts on the active selection instead of
// always resetting to the top. Falls back to 0 when there is no match.
func profileCursorFor(profiles, available []string) int {
	current := "none"
	if len(profiles) > 0 {
		current = profiles[0]
	}
	for i, p := range available {
		if p == current {
			return i
		}
	}
	return 0
}

func (m *Model) SetCallbacks(onLog func(name, line string), onStatus func(name string, status service.Status, pid int)) {
	m.onLog = onLog
	m.onStatus = onStatus
}

// toggleService inicia ou para o servico de indice idx. E no-op se idx estiver
// fora do intervalo (ex.: a aba do MCP, cujo indice e len(services)) ou se o
// servico estiver gerando fontes. O start feito daqui sempre usa o workdir
// configurado, descartando um diretorio customizado vindo do MCP
// (start_service_at).
func (m Model) toggleService(idx int) {
	if idx < 0 || idx >= len(m.services) {
		return
	}
	s := m.services[idx]
	switch s.Status() {
	case service.StatusRunning, service.StatusBuilding:
		go s.Stop()
	case service.StatusIdle, service.StatusStopped, service.StatusCrashed:
		go s.StartConfigured()
	}
}

// generateSources dispara o generate-sources isolado do servico de indice idx.
// E no-op fora do intervalo (ex.: a aba do MCP). A execucao e assincrona: o
// status muda para "generating" e o progresso aparece no log do servico.
func (m Model) generateSources(idx int) {
	if idx < 0 || idx >= len(m.services) {
		return
	}
	s := m.services[idx]
	go s.GenerateSources()
}

// restartService para e reinicia o servico de indice idx (assincrono),
// preservando um workdir customizado (git worktree) eventualmente setado via
// MCP — diferente do toggleService, cujo start sempre usa o workdir
// configurado. E no-op fora do intervalo (ex.: a aba do MCP) ou enquanto o
// servico esta gerando fontes.
func (m Model) restartService(idx int) {
	if idx < 0 || idx >= len(m.services) {
		return
	}
	s := m.services[idx]
	if s.Status() == service.StatusGenerating {
		return
	}
	go s.Restart(0)
}

// logsForTab returns the raw (unwrapped) log lines for tab idx: a service's
// own log when idx is a valid service index, or the MCP server's log when it
// equals len(services) and the MCP tab exists. Returns nil otherwise (e.g. no
// tabs at all).
func (m Model) logsForTab(idx int) []string {
	if idx >= 0 && idx < len(m.services) {
		return m.services[idx].Logs()
	}
	if m.mcpServer != nil && idx == len(m.services) {
		return m.mcpServer.Logs()
	}
	return nil
}
