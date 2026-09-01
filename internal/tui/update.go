package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/JonathanBencke/ServiceManagerTUI/internal/mcp"
	"github.com/JonathanBencke/ServiceManagerTUI/internal/service"
	"github.com/JonathanBencke/ServiceManagerTUI/internal/webconfig"
	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		if m.mode == ModeProfileSelect {
			return m.handleProfileKey(msg)
		}
		if m.mode == ModeOnboarding {
			return m.handleOnboardingKey(msg)
		}
		if m.mode == ModeConfirm {
			return m.handleConfirmKey(msg)
		}
		if m.mode == ModeLogSearch {
			return m.handleLogSearchKey(msg)
		}
		if m.mode == ModeInfo {
			return m.handleInfoKey(msg)
		}
		return m.handleNormalKey(msg)

	case tea.MouseMsg:
		if m.mode == ModeNormal {
			return m.handleMouse(msg)
		}
		return m, nil

	case logMsg:
		return m, nil

	case statusMsg:
		return m, nil

	case mcpLogMsg:
		return m, nil

	case mcpToggleMsg:
		return m, nil

	case reloadRequestMsg:
		// Triggered when the web config editor saves; reload from disk.
		return m, reloadConfig(m.cfgPath, m.manager)

	case tickMsg:
		m.notice = ""
		return m, tea.Batch(
			tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
				return tickMsg{}
			}),
			collectStats(m.services),
			collectHealth(m.services),
		)

	case statsMsg:
		m.stats = msg.stats
		return m, nil

	case healthMsg:
		m.health = msg.health
		return m, nil

	case reloadMsg:
		if msg.err != nil {
			return m, nil
		}
		// Track the selected tab by name (not raw index) since Reload can
		// insert/remove services and shift positions around.
		isMCPSelected := m.mcpServer != nil && m.selectedLog == len(m.services)
		var selectedName string
		if !isMCPSelected && m.selectedLog >= 0 && m.selectedLog < len(m.services) {
			selectedName = m.services[m.selectedLog].Name()
		}

		m.services = m.manager.Services()
		m.manager.SetCallbacks(m.onLog, m.onStatus)

		switch {
		case isMCPSelected:
			m.selectedLog = len(m.services)
		case selectedName != "":
			m.selectedLog = 0
			for i, s := range m.services {
				if s.Name() == selectedName {
					m.selectedLog = i
					break
				}
			}
		default:
			m.selectedLog = 0
		}

		// Leaving onboarding once services exist.
		if m.mode == ModeOnboarding && len(m.services) > 0 {
			m.mode = ModeNormal
		}
		return m, nil

	case profileSelectedMsg:
		m.profiles = msg.profiles
		m.manager.SetProfilesForAll(msg.profiles)
		m.mode = ModeNormal
		return m, nil
	}

	return m, nil
}

func toggleMCP(srv *mcp.MCPServer) tea.Cmd {
	return func() tea.Msg {
		if srv.IsRunning() {
			err := srv.Stop()
			return mcpToggleMsg{running: false, err: err}
		}
		err := srv.Start()
		return mcpToggleMsg{running: true, err: err}
	}
}

// openConfig starts the web config server (if needed) and opens the browser.
func openConfig(srv *webconfig.Server) tea.Cmd {
	return func() tea.Msg {
		if srv == nil {
			return nil
		}
		if err := srv.Start(); err != nil {
			return nil
		}
		openBrowser(srv.URL())
		return nil
	}
}

// openBrowser opens url in the default browser (Windows).
func openBrowser(url string) {
	_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}

// handleMouse supports two interactions, enabled by
// tea.WithMouseCellMotion() in main.go: the scroll wheel adjusts the log
// scroll anywhere on screen (same as k/j), and a left click on a service
// table row selects that service's tab (same as clicking its number key or
// pressing left/right until it is highlighted). Clicking the tab bar or log
// panel itself is intentionally not handled: unlike the table (fixed one
// line per service, always at the same offset below the header), the tab
// bar and footer wrap dynamically with terminal width, so mapping a click to
// a specific tab there would require mirroring that wrapping logic exactly
// and could easily select the wrong one — not worth the risk for a
// secondary, nice-to-have interaction when the keyboard already covers it.
func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		m.logScroll++
		return m, nil
	case tea.MouseButtonWheelDown:
		if m.logScroll > 0 {
			m.logScroll--
		}
		return m, nil
	}

	if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress {
		if idx, ok := m.serviceRowAt(msg.Y); ok {
			m.selectedLog = idx
			m.logScroll = 0
		}
	}
	return m, nil
}

// serviceRowAt maps a clicked terminal row y to a service index. It relies on
// the fixed layout at the top of View(): header (line 0), a blank line
// (1), the table's two header lines - labels and border - (2-3), then
// exactly one line per service starting at line 4. That part of the layout
// never wraps regardless of terminal width (unlike the tab bar/footer below
// it), so the mapping is exact.
func (m Model) serviceRowAt(y int) (int, bool) {
	const tableStartY = 4
	idx := y - tableStartY
	if idx < 0 || idx >= len(m.services) {
		return 0, false
	}
	return idx, true
}

func (m Model) handleNormalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {

	case "q", "ctrl+c":
		if n := m.manager.RunningCount(); n > 0 {
			m.mode = ModeConfirm
			m.confirmAction = "quit"
			m.confirmMessage = fmt.Sprintf("Sair e parar %d serviço(s) em execução?", n)
			return m, nil
		}
		m.quitting = true
		return m, tea.Quit

	case "a":
		m.manager.StartAllConfigured()
		return m, nil

	case "x":
		if n := m.manager.RunningCount(); n > 0 {
			m.mode = ModeConfirm
			m.confirmAction = "stopAll"
			m.confirmMessage = fmt.Sprintf("Parar todos os %d serviço(s) em execução?", n)
		}
		return m, nil

	case "right":
		tabCount := len(m.services)
		if m.mcpServer != nil {
			tabCount++
		}
		if tabCount == 0 {
			return m, nil
		}
		m.selectedLog = (m.selectedLog + 1) % tabCount
		m.logScroll = 0
		return m, nil

	case "left":
		tabCount := len(m.services)
		if m.mcpServer != nil {
			tabCount++
		}
		if tabCount == 0 {
			return m, nil
		}
		m.selectedLog = (m.selectedLog - 1 + tabCount) % tabCount
		m.logScroll = 0
		return m, nil

	case "enter", " ":
		// Alterna o servico da aba selecionada. Funciona para qualquer numero
		// de servicos (a aba do MCP nao e alternavel por aqui).
		m.toggleService(m.selectedLog)
		return m, nil

	case "R":
		// Reinicia (stop + start) so o servico da aba selecionada, preservando
		// um workdir customizado (git worktree) setado via MCP.
		m.restartService(m.selectedLog)
		return m, nil

	case "m":
		if m.mcpServer == nil {
			return m, nil
		}
		return m, toggleMCP(m.mcpServer)

	case "p":
		// Abre a tela de selecao rapida de profile (Spring-like), aplicada a
		// todos os servicos de uma vez (mesmo comportamento de SetProfilesForAll).
		m.mode = ModeProfileSelect
		m.profileCursor = profileCursorFor(m.profiles, m.availableProfiles)
		return m, nil

	case "i":
		// Mostra o comando resolvido (build/run) e o env efetivo do servico da
		// aba selecionada. No-op na aba do MCP (nao ha *service.Service ali).
		if m.selectedLog >= 0 && m.selectedLog < len(m.services) {
			m.mode = ModeInfo
		}
		return m, nil

	case "r":
		// Roda apenas o generate-sources do servico selecionado; para o
		// servico antes se estiver ativo e nao o reinicia depois.
		m.generateSources(m.selectedLog)
		return m, nil

	case "c":
		return m, openConfig(m.webServer)

	case "up", "k":
		m.logScroll++
		return m, nil

	case "down", "j":
		if m.logScroll > 0 {
			m.logScroll--
		}
		return m, nil

	case "pgup":
		m.logScroll += 10
		return m, nil

	case "pgdown":
		m.logScroll -= 10
		if m.logScroll < 0 {
			m.logScroll = 0
		}
		return m, nil

	case "g":
		if m.selectedLog < len(m.services) {
			m.logScroll = len(m.services[m.selectedLog].Logs())
		} else if m.mcpServer != nil {
			m.logScroll = len(m.mcpServer.Logs())
		}
		return m, nil

	case "G":
		m.logScroll = 0
		return m, nil

	case "/":
		// Abre a busca nos logs da aba selecionada. O restante da tela
		// (tabela, painel de log, abas) continua visivel; so o rodape vira o
		// prompt de digitacao ate confirmar (Enter) ou cancelar (Esc).
		m.mode = ModeLogSearch
		m.logSearchInput = ""
		return m, nil

	case "n":
		m.nextLogMatch(1)
		return m, nil

	case "N":
		m.nextLogMatch(-1)
		return m, nil

	case "w":
		m = m.saveCurrentLog()
		return m, nil

	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		m.toggleService(int(msg.String()[0] - '1'))
	}

	return m, nil
}

// handleLogSearchKey collects the pattern typed after `/`. Enter compiles it
// as a case-insensitive regular expression (falling back to a literal
// substring match if it does not parse) and jumps to the closest match in the
// currently selected tab's log; Esc cancels without touching any previously
// confirmed search.
func (m Model) handleLogSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {

	case "esc":
		m.mode = ModeNormal
		return m, nil

	case "enter":
		m.mode = ModeNormal
		return m.runLogSearch(m.logSearchInput), nil

	case "backspace":
		if len(m.logSearchInput) > 0 {
			runes := []rune(m.logSearchInput)
			m.logSearchInput = string(runes[:len(runes)-1])
		}
		return m, nil

	case "ctrl+u":
		m.logSearchInput = ""
		return m, nil
	}

	if msg.Type == tea.KeyRunes {
		m.logSearchInput += string(msg.Runes)
	} else if msg.Type == tea.KeySpace {
		m.logSearchInput += " "
	}
	return m, nil
}

// runLogSearch compiles pattern and searches the raw log lines of the
// currently selected tab, clearing any previous search when pattern is
// empty. It jumps logScroll to the most recent match, so the user sees it
// right away, and leaves logSearchMatchIdx positioned there for n/N.
func (m Model) runLogSearch(pattern string) Model {
	if pattern == "" {
		m.logSearchQuery = ""
		m.logSearchRe = nil
		m.logSearchMatches = nil
		m.logSearchMatchIdx = -1
		return m
	}

	re, err := regexp.Compile("(?i)(" + pattern + ")")
	if err != nil {
		re = regexp.MustCompile("(?i)(" + regexp.QuoteMeta(pattern) + ")")
	}

	logs := m.logsForTab(m.selectedLog)
	var matches []int
	for i, line := range logs {
		if re.MatchString(line) {
			matches = append(matches, i)
		}
	}

	m.logSearchQuery = pattern
	m.logSearchRe = re
	m.logSearchTab = m.selectedLog
	m.logSearchMatches = matches
	m.logSearchMatchIdx = -1

	if len(matches) > 0 {
		m.logSearchMatchIdx = len(matches) - 1 // o mais recente, perto do fim do log
		m.jumpToLogMatch(matches[m.logSearchMatchIdx], len(logs))
	}
	return m
}

// nextLogMatch moves logSearchMatchIdx by dir (+1 = próximo/mais recente,
// -1 = anterior/mais antigo), wrapping around, and scrolls to it. It is a
// no-op without an active search or after switching to a different tab than
// the one the search was run against.
func (m *Model) nextLogMatch(dir int) {
	if len(m.logSearchMatches) == 0 || m.logSearchTab != m.selectedLog {
		return
	}
	idx := m.logSearchMatchIdx + dir
	if idx < 0 {
		idx = len(m.logSearchMatches) - 1
	} else if idx >= len(m.logSearchMatches) {
		idx = 0
	}
	m.logSearchMatchIdx = idx
	m.jumpToLogMatch(m.logSearchMatches[idx], len(m.logsForTab(m.selectedLog)))
}

// jumpToLogMatch sets logScroll so raw line index rawIdx (out of total raw
// lines) lands near the bottom of the visible window. This is an
// approximation: logScroll actually counts wrapped display lines from the
// end, while rawIdx counts unwrapped log lines, so an entry that wraps into
// several display lines can leave the match a few lines off from dead
// center. Good enough to bring it into view; arrow keys refine from there.
func (m *Model) jumpToLogMatch(rawIdx, total int) {
	scroll := total - 1 - rawIdx
	if scroll < 0 {
		scroll = 0
	}
	m.logScroll = scroll
}

// logSaveDir is where `w` writes the exported log file, relative to the
// process' current directory (the same place services.toml normally lives).
const logSaveDir = "logs"

// saveCurrentLog writes the full raw log buffer of the selected tab to a
// timestamped file under logSaveDir, so the user does not lose the tail of a
// long build once the bounded ring buffer (500 lines per service) rotates.
// Feedback is left as a transient notice in the log panel (m.notice),
// auto-cleared on the next 3s tick.
func (m Model) saveCurrentLog() Model {
	name := "MCP"
	if m.selectedLog >= 0 && m.selectedLog < len(m.services) {
		name = m.services[m.selectedLog].Name()
	} else if m.mcpServer == nil {
		m.notice = "Nada selecionado para salvar"
		m.noticeErr = true
		return m
	}

	logs := m.logsForTab(m.selectedLog)
	if len(logs) == 0 {
		m.notice = fmt.Sprintf("%s: log vazio, nada para salvar", name)
		m.noticeErr = true
		return m
	}

	path, err := writeLogFile(name, logs)
	if err != nil {
		m.notice = fmt.Sprintf("Falha ao salvar log: %v", err)
		m.noticeErr = true
		return m
	}

	m.notice = fmt.Sprintf("Log salvo em %s (%d linhas)", path, len(logs))
	m.noticeErr = false
	return m
}

// writeLogFile creates logSaveDir if needed and writes lines to a new file
// named after the service/tab and the current timestamp, returning the
// relative path written.
func writeLogFile(name string, lines []string) (string, error) {
	if err := os.MkdirAll(logSaveDir, 0755); err != nil {
		return "", fmt.Errorf("create %s: %w", logSaveDir, err)
	}

	filename := fmt.Sprintf("%s_%s.log", sanitizeFileName(name), time.Now().Format("20060102_150405"))
	path := filepath.Join(logSaveDir, filename)

	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return "", err
	}
	return path, nil
}

// sanitizeFileName replaces characters that are invalid (or awkward to quote)
// in a Windows file name with "_", so any service name is safe to use as-is.
func sanitizeFileName(name string) string {
	replacer := strings.NewReplacer(
		"/", "_", `\`, "_", ":", "_", "*", "_", "?", "_",
		`"`, "_", "<", "_", ">", "_", "|", "_", " ", "_",
	)
	return replacer.Replace(name)
}

func collectStats(services []*service.Service) tea.Cmd {
	return func() tea.Msg {
		stats := make(map[string]service.ProcStats)
		for _, s := range services {
			if s.Status() == service.StatusRunning {
				stats[s.Name()] = s.GetStats()
			}
		}
		return statsMsg{stats: stats}
	}
}

// collectHealth dials the configured health_port of every running service
// concurrently, so the total wall time is the slowest single check instead of
// their sum. It only checks services that are both running and declare a
// health_port > 0 - matching the readiness criteria of the MCP
// wait_until_ready tool. CheckPort has its own short dial timeout, and this
// always runs inside a tea.Cmd (off the render path), so a slow/unresponsive
// port never blocks View().
func collectHealth(services []*service.Service) tea.Cmd {
	return func() tea.Msg {
		health := make(map[string]bool)
		var mu sync.Mutex
		var wg sync.WaitGroup

		for _, s := range services {
			if s.Status() != service.StatusRunning || s.HealthPort() <= 0 {
				continue
			}
			wg.Add(1)
			go func(s *service.Service) {
				defer wg.Done()
				ok := service.CheckPort(s.HealthPort())
				mu.Lock()
				health[s.Name()] = ok
				mu.Unlock()
			}(s)
		}
		wg.Wait()

		return healthMsg{health: health}
	}
}

func (m Model) handleProfileKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {

	case "esc", "q":
		m.mode = ModeNormal
		return m, nil

	case "up", "k":
		if m.profileCursor > 0 {
			m.profileCursor--
		}
		return m, nil

	case "down", "j":
		if m.profileCursor < len(m.availableProfiles)-1 {
			m.profileCursor++
		}
		return m, nil

	case "enter":
		return m, func() tea.Msg {
			selected := m.availableProfiles[m.profileCursor]
			if selected == "none" {
				return profileSelectedMsg{profiles: []string{}}
			}
			return profileSelectedMsg{profiles: []string{selected}}
		}
	}

	return m, nil
}

// handleConfirmKey resolves the pending "are you sure?" gate opened by a
// destructive shortcut (x, q/ctrl+c) while services are running. [y]/[enter]
// executes the recorded action, anything else ([n]/[esc]/[q]) cancels back to
// ModeNormal without side effects.
func (m Model) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {

	case "y", "Y", "enter":
		action := m.confirmAction
		m.mode = ModeNormal
		m.confirmAction = ""
		m.confirmMessage = ""

		switch action {
		case "stopAll":
			m.manager.StopAll()
			return m, nil
		case "quit":
			m.quitting = true
			return m, tea.Quit
		}
		return m, nil

	default:
		// n, N, esc, q, ou qualquer outra tecla: cancela sem executar a acao.
		m.mode = ModeNormal
		m.confirmAction = ""
		m.confirmMessage = ""
		return m, nil
	}
}

// handleInfoKey closes the read-only service info screen (opened with `i`)
// on any key press — there is nothing to confirm or cancel, just to dismiss.
func (m Model) handleInfoKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.mode = ModeNormal
	return m, nil
}

func (m Model) handleOnboardingKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {

	case "q", "ctrl+c":
		m.quitting = true
		return m, tea.Quit

	case "enter", "c":
		return m, openConfig(m.webServer)
	}

	return m, nil
}
