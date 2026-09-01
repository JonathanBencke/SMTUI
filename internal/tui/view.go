package tui

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/JonathanBencke/ServiceManagerTUI/internal/service"
	"github.com/charmbracelet/lipgloss"
)

func (m Model) View() string {
	if m.quitting {
		return ""
	}

	if m.mode == ModeProfileSelect {
		return m.viewProfileSelect()
	}

	if m.mode == ModeConfirm {
		return m.viewConfirm()
	}

	if m.mode == ModeInfo {
		return m.viewInfo()
	}

	if m.mode == ModeOnboarding {
		return m.viewOnboarding()
	}

	// Sem serviços cadastrados: mostra o onboarding em vez de um painel vazio.
	if len(m.services) == 0 {
		return m.viewOnboarding()
	}

	running := 0
	for _, s := range m.services {
		if s.Status() == service.StatusRunning {
			running++
		}
	}

	mcpStatus := ""
	if m.mcpServer != nil {
		if m.mcpServer.IsRunning() {
			mcpStatus = statusRunning.Render(fmt.Sprintf(" MCP: ON (port %d) ", m.mcpServer.Port()))
		} else {
			mcpStatus = statusIdle.Render(" MCP: OFF ")
		}
	}
	header := headerStyle.Render(fmt.Sprintf(" Service Manager  %d/%d running ", running, len(m.services))) + "  " + mcpStatus

	tableHeader := m.renderTableHeader()
	var serviceRows []string
	for i, s := range m.services {
		serviceRows = append(serviceRows, m.renderServiceRow(i, s))
	}
	servicesBlock := tableHeader + "\n" + strings.Join(serviceRows, "\n")

	logBlock := m.renderLogPanel()

	footer := m.renderFooter()
	if m.mode == ModeLogSearch {
		footer = m.renderLogSearchPrompt()
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		header,
		"",
		servicesBlock,
		"",
		logBlock,
		"",
		footer,
	)
}

var (
	colKey     = 5
	colName    = 22
	colStatus  = 14
	colHealth  = 12
	colProfile = 8
	colBranch  = 18
	colRes     = 20
	colUptime  = 10
	colPID     = 10
)

var tableHeaderStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("#7DD3FC")).
	Bold(true)

var tableBorderStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("#45475A"))

func (m Model) renderTableHeader() string {
	cols := []struct {
		label string
		width int
	}{
		{"KEY", colKey},
		{"NAME", colName},
		{"STATUS", colStatus},
		{"HEALTH", colHealth},
		{"PROFILE", colProfile},
		{"BRANCH", colBranch},
		{"RESOURCES", colRes},
		{"UPTIME", colUptime},
		{"PID", colPID},
	}

	var parts []string
	var visWidth int
	for _, c := range cols {
		parts = append(parts, tableHeaderStyle.Render(fitCell(c.label, c.width)))
		visWidth += c.width
	}

	sep := tableBorderStyle.Render(" │ ")
	row := strings.Join(parts, sep)
	borderWidth := visWidth + (len(cols)+1)*3
	border := tableBorderStyle.Render(strings.Repeat("─", borderWidth))

	return tableBorderStyle.Render(" │ ") + row + tableBorderStyle.Render(" │ ") + "\n" + tableBorderStyle.Render(" ├") + border + tableBorderStyle.Render("┤")
}

func (m Model) renderServiceRow(i int, s *service.Service) string {
	selected := i == m.selectedLog
	status := string(s.Status())
	statusText := fmt.Sprintf("%s %s", statusIcon(status), status)
	stStyle := statusStyle(status)

	pidStr := "-"
	if s.PID() > 0 {
		pidStr = fmt.Sprintf("%d", s.PID())
	}

	profileStr := strings.Join(s.Profiles(), ",")
	if profileStr == "" {
		profileStr = "none"
	}

	healthStr, healthStyle := m.healthCell(s, status)

	branchStr := s.GitBranch()
	if branchStr == "" {
		branchStr = "-"
	}

	resStr := "-"
	if st, ok := m.stats[s.Name()]; ok && st.MemBytes > 0 {
		resStr = fmt.Sprintf("%.1f%% CPU  %s", st.CPUPercent, service.FormatMem(st.MemBytes))
	}

	uptimeStr := "-"
	if uptime := s.Uptime(); uptime > 0 {
		uptimeStr = formatUptime(uptime)
	}

	keyText := fmt.Sprintf(" [%d]", i+1)
	keyStyle := keyActiveStyle
	nameStyle := serviceNameStyle
	if selected {
		keyText = fmt.Sprintf("▸[%d]", i+1)
		keyStyle = selectedStyle
		nameStyle = selectedStyle
	}

	cols := []struct {
		text  string
		width int
		style lipgloss.Style
	}{
		{keyText, colKey, keyStyle},
		{s.Name(), colName, nameStyle},
		{statusText, colStatus, stStyle},
		{healthStr, colHealth, healthStyle},
		{profileStr, colProfile, dimLabelStyle},
		{branchStr, colBranch, dimLabelStyle},
		{resStr, colRes, resStyle},
		{uptimeStr, colUptime, dimLabelStyle},
		{pidStr, colPID, pidStyle},
	}

	var parts []string
	for _, c := range cols {
		parts = append(parts, c.style.Render(fitCell(c.text, c.width)))
	}

	sep := tableBorderStyle.Render(" │ ")
	return tableBorderStyle.Render(" │ ") + strings.Join(parts, sep) + tableBorderStyle.Render(" │ ")
}

// healthCell renders the HEALTH column for a service: "-" when it declares no
// health_port (nothing to check) or while it is not running (not applicable
// yet); "…" while running but the periodic check (collectHealth, every 3s)
// has not completed at least once yet; otherwise the last known outcome.
func (m Model) healthCell(s *service.Service, status string) (string, lipgloss.Style) {
	port := s.HealthPort()
	if port <= 0 || status != string(service.StatusRunning) {
		return "-", dimLabelStyle
	}
	ok, known := m.health[s.Name()]
	switch {
	case !known:
		return fmt.Sprintf("… %d", port), dimLabelStyle
	case ok:
		return fmt.Sprintf("● %d", port), statusRunning
	default:
		return fmt.Sprintf("✖ %d", port), statusCrashed
	}
}

// fitCell ajusta o texto para caber em exatamente `width` colunas visiveis:
// trunca com reticencias se for maior, ou preenche com espacos se for menor.
func fitCell(s string, width int) string {
	runes := []rune(s)
	if len(runes) > width {
		if width <= 1 {
			return string(runes[:width])
		}
		return string(runes[:width-1]) + "…"
	}
	return s + strings.Repeat(" ", width-len(runes))
}

func formatUptime(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd%dh", int(d.Hours()/24), int(d.Hours())%24)
}

func stripAnsi(s string) string {
	var result strings.Builder
	inEscape := false
	for _, r := range s {
		if r == '\x1b' {
			inEscape = true
			continue
		}
		if inEscape {
			if r == 'm' {
				inEscape = false
			}
			continue
		}
		result.WriteRune(r)
	}
	return result.String()
}

func (m Model) renderLogPanel() string {
	panelWidth := m.width
	if panelWidth < 40 {
		panelWidth = 100
	}

	logWidth := panelWidth - 6
	if logWidth < 20 {
		logWidth = 80
	}

	logs := m.logsForTab(m.selectedLog)

	// chromeLines = linhas fixas do layout (nao sao de log):
	//   header(1) + blank(1) + tabela(2+N) + blank(1) + bordas do painel(2)
	//   + blank(1) + margem(1) = N + 9, mais as linhas das abas e do rodape,
	//   que quebram em varias quando o terminal e estreito.
	chromeLines := 9 + len(m.services) +
		len(m.tabBarLines(scrollIndicatorFor(m.logScroll))) +
		len(m.footerLines())
	maxLogLines := 5
	if m.height > chromeLines {
		maxLogLines = m.height - chromeLines
		if maxLogLines < 3 {
			maxLogLines = 3
		}
	}

	scroll := m.logScroll

	// So faz wrapText das linhas brutas necessarias para preencher a janela visivel
	// (as ultimas (scroll + maxLogLines + margem)*2 brutas). Cada bruta vira >=1 linha
	// wrapped, logo isso cobre o scroll atual. Se a estimativa exceder o buffer,
	// processa tudo (fallback para scroll profundo, raro).
	estimate := (scroll + maxLogLines + 4) * 2
	var recent []string
	if estimate >= len(logs) || estimate <= 0 {
		recent = logs
	} else {
		recent = logs[len(logs)-estimate:]
	}

	visibleLines := make([]string, 0, len(recent))
	for _, line := range recent {
		visibleLines = append(visibleLines, wrapText(line, logWidth)...)
	}

	if scroll > len(visibleLines)-maxLogLines {
		scroll = len(visibleLines) - maxLogLines
	}
	if scroll < 0 {
		scroll = 0
	}

	end := len(visibleLines) - scroll
	start := end - maxLogLines
	if start < 0 {
		start = 0
	}
	if end < 0 {
		end = 0
	}

	var logContent strings.Builder
	highlightActive := m.logSearchRe != nil && m.logSearchTab == m.selectedLog
	for _, line := range visibleLines[start:end] {
		if highlightActive {
			line = m.logSearchRe.ReplaceAllStringFunc(line, func(s string) string {
				return searchHighlightStyle.Render(s)
			})
		}
		logContent.WriteString(line + "\n")
	}

	shown := end - start
	for i := shown; i < maxLogLines; i++ {
		logContent.WriteString("\n")
	}

	tabBar := m.renderTabBar(scrollIndicatorFor(scroll))

	panelLines := []string{tabBar}
	if status := m.renderLogSearchStatus(); status != "" {
		panelLines = append(panelLines, status)
	}
	if m.notice != "" {
		panelLines = append(panelLines, m.renderNotice())
	}
	panelLines = append(panelLines, logContent.String())

	inner := lipgloss.JoinVertical(lipgloss.Left, panelLines...)
	return logPanelStyle.Width(panelWidth - 2).Render(inner)
}

// renderLogSearchStatus shows the active search query and match position for
// the currently selected tab (e.g. " 🔍 "erro" — 2/5  [n/N] navegar "), or ""
// when there is no confirmed search for this tab (a search run against
// another tab stays silent until the user switches back to it).
func (m Model) renderLogSearchStatus() string {
	if m.logSearchQuery == "" || m.logSearchTab != m.selectedLog {
		return ""
	}
	if len(m.logSearchMatches) == 0 {
		return keyHintStyle.Render(fmt.Sprintf(" 🔍 %q — nenhuma ocorrência ", m.logSearchQuery))
	}
	return keyHintStyle.Render(fmt.Sprintf(" 🔍 %q — %d/%d  [n/N] navegar ", m.logSearchQuery, m.logSearchMatchIdx+1, len(m.logSearchMatches)))
}

// renderNotice shows the transient one-line feedback set by saveCurrentLog
// (`w`), styled as success (green) or error (red) depending on noticeErr.
func (m Model) renderNotice() string {
	style := successHintStyle
	if m.noticeErr {
		style = statusCrashed
	}
	return style.Render(" " + m.notice + " ")
}

// renderLogSearchPrompt replaces the footer while ModeLogSearch is active,
// showing the pattern typed so far in place of the usual keyboard hints.
func (m Model) renderLogSearchPrompt() string {
	style := footerStyle
	if m.width > 0 {
		style = style.Width(m.layoutWidth())
	}
	prompt := fmt.Sprintf(" Buscar nos logs (regex, sem distinção de maiúsculas): %s█   [Enter] Confirmar   [Esc] Cancelar ", m.logSearchInput)
	return style.Render(prompt)
}

// scrollIndicatorFor renderiza o aviso de rolagem exibido ao lado das abas, ou
// string vazia quando o log esta no fim.
func scrollIndicatorFor(scroll int) string {
	if scroll <= 0 {
		return ""
	}
	return keyHintStyle.Render(fmt.Sprintf(" ↑ %d lines — [G] latest ", scroll))
}

// renderTabBar renderiza as abas de log com o nome completo de cada servico,
// quebrando em varias linhas quando nao couberem na largura do terminal.
// scrollIndicator, quando presente, entra como ultimo item (podendo cair na
// linha seguinte).
func (m Model) renderTabBar(scrollIndicator string) string {
	return strings.Join(m.tabBarLines(scrollIndicator), "\n")
}

func (m Model) tabBarLines(scrollIndicator string) []string {
	render := func(icon, label string, selected bool) string {
		text := fmt.Sprintf(" %s %s ", icon, label)
		if selected {
			return tabActiveStyle.Render(text)
		}
		return tabInactiveStyle.Render(text)
	}

	var tabs []string
	for i, s := range m.services {
		tabs = append(tabs, render(statusIcon(string(s.Status())), s.Name(), i == m.selectedLog))
	}
	if m.mcpServer != nil {
		mcpIdx := len(m.services)
		mcpIcon := "○"
		if m.mcpServer.IsRunning() {
			mcpIcon = "●"
		}
		tabs = append(tabs, render(mcpIcon, "MCP", mcpIdx == m.selectedLog))
	}
	if scrollIndicator != "" {
		tabs = append(tabs, scrollIndicator)
	}

	// -4: bordas e padding do painel de log em volta das abas.
	return packItems(tabs, tabSeparator, m.layoutWidth()-4)
}

// footerHints sao os atalhos do rodape, na ordem de exibicao.
var footerHints = []string{
	"[←→]Selecionar",
	"[clique]Selecionar",
	"[Enter/Espaço]Iniciar/Parar",
	"[R]Reiniciar",
	"[1-9]Atalho",
	"[a]Todos",
	"[x]Parar",
	"[p]Profile",
	"[i]Info",
	"[r]Gerar fontes",
	"[/ n/N]Buscar logs",
	"[w]Salvar log",
	"[↑↓]Logs",
	"[g/G]Topo/Fim",
	"[c]Configurar",
	"[m]MCP",
	"[q]Sair",
}

const footerSeparator = "  "

// footerLines quebra os atalhos em varias linhas quando nao couberem na largura
// do terminal, para que nenhum comando fique escondido.
func (m Model) footerLines() []string {
	// -2: padding horizontal do footerStyle.
	return packItems(footerHints, footerSeparator, m.layoutWidth()-2)
}

// renderFooter desenha o rodape, uma linha por grupo de atalhos que couber na
// largura do terminal. Todas as linhas recebem a mesma largura para o fundo do
// rodape formar um bloco continuo.
func (m Model) renderFooter() string {
	style := footerStyle
	if m.width > 0 {
		style = style.Width(m.layoutWidth())
	}

	lines := m.footerLines()
	rendered := make([]string, 0, len(lines))
	for _, line := range lines {
		rendered = append(rendered, style.Render(line))
	}
	return strings.Join(rendered, "\n")
}

// packItems agrupa itens ja renderizados em linhas cuja largura visivel nao
// passa de max, unindo-os com sep. Um item maior que max fica sozinho na linha
// (preferimos deixar transbordar a esconder o item).
func packItems(items []string, sep string, max int) []string {
	if len(items) == 0 {
		return nil
	}

	sepWidth := visibleWidth(sep)
	var lines []string
	var current strings.Builder
	currentWidth := 0

	for _, item := range items {
		itemWidth := visibleWidth(item)
		switch {
		case currentWidth == 0:
			current.WriteString(item)
			currentWidth = itemWidth
		case currentWidth+sepWidth+itemWidth > max:
			lines = append(lines, current.String())
			current.Reset()
			current.WriteString(item)
			currentWidth = itemWidth
		default:
			current.WriteString(sep)
			current.WriteString(item)
			currentWidth += sepWidth + itemWidth
		}
	}
	if current.Len() > 0 {
		lines = append(lines, current.String())
	}
	return lines
}

// visibleWidth conta as colunas ocupadas por s, ignorando codigos ANSI.
func visibleWidth(s string) int {
	return len([]rune(stripAnsi(s)))
}

// defaultLayoutWidth e usado enquanto o terminal nao informou seu tamanho
// (primeiro render, antes do WindowSizeMsg).
const defaultLayoutWidth = 100

// layoutWidth retorna a largura usada para dimensionar o layout.
func (m Model) layoutWidth() int {
	if m.width < 40 {
		return defaultLayoutWidth
	}
	return m.width
}

func (m Model) viewOnboarding() string {
	title := headerStyle.Render(" Bem-vindo ao Service Manager ")

	intro := []string{
		"",
		"  Configure seus serviços por uma página web simples — sem editar arquivos na mão.",
		"",
		"  Um arquivo " + serviceNameStyle.Render("services.toml") + " de exemplo (comentado) foi criado ao lado do executável,",
		"  já com os presets prontos: " + serviceNameStyle.Render("java-maven, spring-boot, wildfly, hcm-integration, node-npm") + ".",
		"",
		"  Como funciona:",
		"    1. Pressione " + keyActiveStyle.Render("c") + " para abrir a página de configuração no navegador.",
		"    2. Cadastre seus serviços, o tenant/ambiente e ajuste os presets.",
		"    3. Clique em Salvar — o TUI recarrega e seus serviços aparecem aqui.",
		"",
	}

	body := logPanelStyle.Render(strings.Join(intro, "\n"))
	hint := successHintStyle.Render(" [c] ou [Enter] Abrir configuração no navegador   [q] Sair ")

	return lipgloss.JoinVertical(lipgloss.Left, title, "", body, "", hint)
}

// viewConfirm renders the "are you sure?" gate opened by a destructive
// shortcut (x, q/ctrl+c) while services are running.
func (m Model) viewConfirm() string {
	title := headerStyle.Render(" Confirmar ")
	body := confirmMessageStyle.Render("  " + m.confirmMessage)
	hint := keyHintStyle.Render(" [y] Sim   [n/Esc] Cancelar ")

	return lipgloss.JoinVertical(lipgloss.Left, title, "", body, "", hint)
}

// infoSecretKeyRe matches environment variable names whose value is masked in
// the `i` info screen even though it is otherwise shown in full — unlike the
// MCP get_service_config tool (which hides all values by default since it
// answers a possibly less trusted agent), this screen is only ever seen by
// the developer operating their own terminal, so showing real values is more
// useful; secret-looking keys stay masked either way as a safety net.
var infoSecretKeyRe = regexp.MustCompile(`(?i)(pass|secret|token|key|credential|auth)`)

// viewInfo renders the read-only "info" screen opened with `i`: the effective
// configuration of the selected service — resolved build/run commands,
// workdir, git branch, profiles, health port and environment variables.
func (m Model) viewInfo() string {
	idx := m.selectedLog
	if idx < 0 || idx >= len(m.services) {
		// Nao deveria acontecer (handleNormalKey so abre ModeInfo com um indice
		// valido), mas evita um crash caso o conjunto de servicos mude enquanto
		// a tela esta aberta (ex.: reload vindo da pagina web).
		return m.viewOnboarding()
	}
	s := m.services[idx]

	title := headerStyle.Render(fmt.Sprintf(" Info: %s ", s.Name()))

	label := func(text string) string {
		return dimLabelStyle.Render(fmt.Sprintf("%-22s", text))
	}

	var lines []string
	if runner := s.Runner(); runner != "" {
		lines = append(lines, label("Preset:")+" "+runner)
	}
	lines = append(lines, label("Workdir configurado:")+" "+s.ConfiguredWorkdir())
	if override := s.WorkdirOverride(); override != "" {
		lines = append(lines, label("Workdir efetivo:")+" "+s.Workdir()+"  (override ativo, ex.: git worktree via MCP)")
	}
	if branch := s.GitBranch(); branch != "" {
		lines = append(lines, label("Branch git:")+" "+branch)
	}
	if profiles := s.Profiles(); len(profiles) > 0 {
		lines = append(lines, label("Profiles:")+" "+strings.Join(profiles, ","))
	}
	if port := s.HealthPort(); port > 0 {
		lines = append(lines, label("Health port:")+" "+fmt.Sprintf("%d", port))
	}

	lines = append(lines, "")
	if build, err := s.ResolvedCommand("build"); err != nil {
		lines = append(lines, label("Build:")+" "+statusCrashed.Render("indisponível ("+err.Error()+")"))
	} else if build != "" {
		lines = append(lines, label("Build:")+" "+build)
	}
	if run, err := s.ResolvedCommand("run"); err != nil {
		lines = append(lines, label("Run:")+" "+statusCrashed.Render("indisponível ("+err.Error()+")"))
	} else if run != "" {
		lines = append(lines, label("Run:")+" "+run)
	}

	if env := s.ConfiguredEnv(); len(env) > 0 {
		keys := make([]string, 0, len(env))
		for k := range env {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		lines = append(lines, "", label("Variáveis de ambiente:")+fmt.Sprintf(" (%d)", len(keys)))
		for _, k := range keys {
			v := env[k]
			if infoSecretKeyRe.MatchString(k) {
				v = "***redacted***"
			}
			lines = append(lines, "    "+serviceNameStyle.Render(k)+"="+v)
		}
	}

	hint := keyHintStyle.Render(" [i/Esc] Voltar ")

	return lipgloss.JoinVertical(lipgloss.Left, title, "", strings.Join(lines, "\n"), "", hint)
}

func (m Model) viewProfileSelect() string {
	title := headerStyle.Render(" Select Profile ")

	var items []string
	for i, p := range m.availableProfiles {
		cursor := "  "
		if i == m.profileCursor {
			cursor = "▶ "
		}
		line := fmt.Sprintf("  %s%s", cursor, p)
		if i == m.profileCursor {
			line = selectedStyle.Render(line)
		}
		items = append(items, line)
	}

	hint := keyHintStyle.Render(" [↑↓] Navigate  [Enter] Select  [Esc] Cancel ")

	return lipgloss.JoinVertical(lipgloss.Left, title, "", strings.Join(items, "\n"), "", hint)
}

func wrapText(s string, max int) []string {
	if max <= 0 {
		return []string{s}
	}
	if len(s) <= max {
		return []string{s}
	}

	var result []string
	remaining := s
	for len(remaining) > max {
		chunk := remaining[:max]

		breakAt := max
		if spaceIdx := strings.LastIndex(chunk, " "); spaceIdx > max/2 {
			breakAt = spaceIdx
		}

		result = append(result, remaining[:breakAt])
		remaining = remaining[breakAt:]
		remaining = strings.TrimLeft(remaining, " ")
	}
	if remaining != "" {
		result = append(result, remaining)
	}
	return result
}
