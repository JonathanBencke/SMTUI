package tui

import (
	"strings"
	"testing"

	"github.com/JonathanBencke/ServiceManagerTUI/internal/config"
	"github.com/JonathanBencke/ServiceManagerTUI/internal/service"
)

// namedModel builds a model with services named after the given list and a
// fixed terminal width.
func namedModel(width int, names ...string) Model {
	cfg := &config.Config{Presets: map[string]config.Preset{"stub": {Run: "cmd /c echo running"}}}
	for _, n := range names {
		cfg.Services = append(cfg.Services, config.ServiceConfig{Name: n, Runner: "stub", Workdir: "."})
	}
	m := NewModel(service.NewManager(cfg))
	m.width = width
	m.height = 40
	return m
}

func TestRenderTableHeader_IncludesBranchColumn(t *testing.T) {
	m := namedModel(120, "Web")

	header := m.renderTableHeader()

	if !strings.Contains(header, "BRANCH") {
		t.Errorf("renderTableHeader() = %q, want it to contain the BRANCH column label", header)
	}
}

func TestRenderTableHeader_IncludesHealthColumn(t *testing.T) {
	m := namedModel(120, "Web")

	header := m.renderTableHeader()

	if !strings.Contains(header, "HEALTH") {
		t.Errorf("renderTableHeader() = %q, want it to contain the HEALTH column label", header)
	}
}

func TestHealthCell_NoHealthPortConfigured(t *testing.T) {
	m := namedModel(120, "Web") // config.ServiceConfig{}.HealthPort == 0

	got, _ := m.healthCell(m.services[0], string(service.StatusRunning))

	if got != "-" {
		t.Errorf("healthCell() = %q, want %q (no health_port configured)", got, "-")
	}
}

func TestHealthCell_NotRunning(t *testing.T) {
	cfg := &config.Config{Presets: map[string]config.Preset{"stub": {Run: "cmd /c echo running"}}}
	cfg.Services = append(cfg.Services, config.ServiceConfig{Name: "Web", Runner: "stub", Workdir: ".", HealthPort: 8080})
	m := NewModel(service.NewManager(cfg))

	got, _ := m.healthCell(m.services[0], string(service.StatusStopped))

	if got != "-" {
		t.Errorf("healthCell() = %q, want %q (not running, health_port not applicable yet)", got, "-")
	}
}

func TestHealthCell_RunningWithoutDataYetShowsPending(t *testing.T) {
	cfg := &config.Config{Presets: map[string]config.Preset{"stub": {Run: "cmd /c echo running"}}}
	cfg.Services = append(cfg.Services, config.ServiceConfig{Name: "Web", Runner: "stub", Workdir: ".", HealthPort: 8080})
	m := NewModel(service.NewManager(cfg))
	// m.health fica vazio de proposito: nenhum tick de collectHealth rodou ainda.

	got, _ := m.healthCell(m.services[0], string(service.StatusRunning))

	if !strings.HasPrefix(got, "…") {
		t.Errorf("healthCell() = %q, want a pending indicator (no data yet)", got)
	}
}

func TestHealthCell_RunningHealthy(t *testing.T) {
	cfg := &config.Config{Presets: map[string]config.Preset{"stub": {Run: "cmd /c echo running"}}}
	cfg.Services = append(cfg.Services, config.ServiceConfig{Name: "Web", Runner: "stub", Workdir: ".", HealthPort: 8080})
	m := NewModel(service.NewManager(cfg))
	m.health["Web"] = true

	got, style := m.healthCell(m.services[0], string(service.StatusRunning))

	if !strings.Contains(got, "8080") {
		t.Errorf("healthCell() = %q, want it to mention the port 8080", got)
	}
	if style.GetForeground() != statusRunning.GetForeground() {
		t.Error("healthCell() style should match statusRunning's color when healthy")
	}
}

func TestHealthCell_RunningDown(t *testing.T) {
	cfg := &config.Config{Presets: map[string]config.Preset{"stub": {Run: "cmd /c echo running"}}}
	cfg.Services = append(cfg.Services, config.ServiceConfig{Name: "Web", Runner: "stub", Workdir: ".", HealthPort: 8080})
	m := NewModel(service.NewManager(cfg))
	m.health["Web"] = false

	got, style := m.healthCell(m.services[0], string(service.StatusRunning))

	if !strings.Contains(got, "8080") {
		t.Errorf("healthCell() = %q, want it to mention the port 8080", got)
	}
	if style.GetForeground() != statusCrashed.GetForeground() {
		t.Error("healthCell() style should match statusCrashed's color when the port is down")
	}
}

func TestViewInfo_ShowsResolvedCommandsAndEnv(t *testing.T) {
	cfg := &config.Config{
		Presets: map[string]config.Preset{
			"stub": {
				Run: "cmd /c echo {{.Name}}",
				Env: map[string]string{"FOO": "bar", "DB_PASSWORD": "supersecret"},
			},
		},
	}
	cfg.Services = append(cfg.Services, config.ServiceConfig{Name: "Web", Runner: "stub", Workdir: "."})
	m := NewModel(service.NewManager(cfg))
	m.mode = ModeInfo
	m.selectedLog = 0

	out := stripAnsi(m.viewInfo())

	if !strings.Contains(out, "Web") {
		t.Errorf("viewInfo() missing service name:\n%s", out)
	}
	if !strings.Contains(out, "cmd /c echo Web") {
		t.Errorf("viewInfo() missing resolved run command:\n%s", out)
	}
	if !strings.Contains(out, "FOO=bar") {
		t.Errorf("viewInfo() missing plain env var FOO=bar:\n%s", out)
	}
	if strings.Contains(out, "supersecret") {
		t.Errorf("viewInfo() leaked a secret-looking env value:\n%s", out)
	}
	if !strings.Contains(out, "***redacted***") {
		t.Errorf("viewInfo() should mask DB_PASSWORD's value:\n%s", out)
	}
}

func TestRenderServiceRow_ShowsDashWhenWorkdirIsNotAGitRepo(t *testing.T) {
	dir := t.TempDir() // diretorio comum, sem .git

	cfg := &config.Config{Presets: map[string]config.Preset{"stub": {Run: "cmd /c echo running"}}}
	cfg.Services = append(cfg.Services, config.ServiceConfig{Name: "Web", Runner: "stub", Workdir: dir})
	m := NewModel(service.NewManager(cfg))
	m.width = 120
	m.height = 40

	row := m.renderServiceRow(0, m.services[0])

	wantBranchCell := fitCell("-", colBranch)
	if !strings.Contains(stripAnsi(row), wantBranchCell) {
		t.Errorf("renderServiceRow() = %q, want it to contain the dash cell %q (workdir is not a git repo)", stripAnsi(row), wantBranchCell)
	}
}

func TestVisibleWidth_IgnoresAnsiCodes(t *testing.T) {
	plain := " ● Benefits "
	styled := tabActiveStyle.Render(plain)

	if got, want := visibleWidth(plain), len([]rune(plain)); got != want {
		t.Errorf("visibleWidth(plain) = %d, want %d", got, want)
	}
	if got, want := visibleWidth(styled), visibleWidth(plain)+2; got != want {
		// +2: padding horizontal do estilo da aba.
		t.Errorf("visibleWidth(styled) = %d, want %d (ANSI must not count)", got, want)
	}
}

func TestPackItems_WrapsInsteadOfOverflowing(t *testing.T) {
	items := []string{"aaaa", "bbbb", "cccc", "dddd"}

	lines := packItems(items, "  ", 12)

	if len(lines) != 2 {
		t.Fatalf("packItems() = %d lines, want 2: %q", len(lines), lines)
	}
	for _, line := range lines {
		if w := visibleWidth(line); w > 12 {
			t.Errorf("line %q width = %d, want <= 12", line, w)
		}
	}
	joined := strings.Join(lines, " ")
	for _, item := range items {
		if !strings.Contains(joined, item) {
			t.Errorf("item %q was dropped: %q", item, lines)
		}
	}
}

func TestPackItems_SingleLineWhenItFits(t *testing.T) {
	lines := packItems([]string{"aa", "bb"}, "  ", 80)

	if len(lines) != 1 {
		t.Fatalf("packItems() = %d lines, want 1: %q", len(lines), lines)
	}
	if lines[0] != "aa  bb" {
		t.Errorf("line = %q, want %q", lines[0], "aa  bb")
	}
}

func TestPackItems_OversizedItemKeepsItsOwnLine(t *testing.T) {
	long := strings.Repeat("x", 20)

	lines := packItems([]string{"aa", long, "bb"}, "  ", 10)

	if len(lines) != 3 {
		t.Fatalf("packItems() = %d lines, want 3 (oversized item alone): %q", len(lines), lines)
	}
	if lines[1] != long {
		t.Errorf("lines[1] = %q, want the oversized item untouched", lines[1])
	}
}

func TestPackItems_Empty(t *testing.T) {
	if lines := packItems(nil, "  ", 10); lines != nil {
		t.Errorf("packItems(nil) = %q, want nil", lines)
	}
}

func TestFooterLines_NarrowTerminalKeepsEveryHint(t *testing.T) {
	m := namedModel(60)

	lines := m.footerLines()

	if len(lines) < 2 {
		t.Fatalf("footerLines() = %d line(s), want it to wrap on a 60-column terminal: %q", len(lines), lines)
	}
	for _, line := range lines {
		if w := visibleWidth(line); w > m.layoutWidth()-2 {
			t.Errorf("line %q width = %d, want <= %d", line, w, m.layoutWidth()-2)
		}
	}
	joined := strings.Join(lines, "  ")
	for _, hint := range footerHints {
		if !strings.Contains(joined, hint) {
			t.Errorf("hint %q is hidden on a narrow terminal:\n%s", hint, joined)
		}
	}
}

func TestFooterLines_WideTerminalUsesSingleLine(t *testing.T) {
	m := namedModel(300)

	if lines := m.footerLines(); len(lines) != 1 {
		t.Errorf("footerLines() = %d lines, want 1 on a wide terminal: %q", len(lines), lines)
	}
}

func TestRenderFooter_LinesShareTheSameWidth(t *testing.T) {
	m := namedModel(60)

	rendered := strings.Split(m.renderFooter(), "\n")

	if len(rendered) < 2 {
		t.Fatalf("renderFooter() = %d line(s), want it to wrap", len(rendered))
	}
	want := visibleWidth(rendered[0])
	for i, line := range rendered {
		if got := visibleWidth(line); got != want {
			t.Errorf("line %d width = %d, want %d (footer background must form a block)", i, got, want)
		}
	}
}

func TestTabBarLines_NarrowTerminalWrapsKeepingServiceNames(t *testing.T) {
	names := []string{"Database", "Benefits", "BenefitsData", "hcm-integration"}
	m := namedModel(60, names...)

	lines := m.tabBarLines("")

	if len(lines) < 2 {
		t.Fatalf("tabBarLines() = %d line(s), want it to wrap on a 60-column terminal: %q", len(lines), lines)
	}
	joined := stripAnsi(strings.Join(lines, "\n"))
	for _, name := range names {
		if !strings.Contains(joined, name) {
			t.Errorf("service %q lost its name on a narrow terminal:\n%s", name, joined)
		}
	}
	for _, line := range lines {
		if w := visibleWidth(line); w > m.layoutWidth()-4 {
			t.Errorf("line %q width = %d, want <= %d", stripAnsi(line), w, m.layoutWidth()-4)
		}
	}
}

func TestTabBarLines_WideTerminalUsesSingleLine(t *testing.T) {
	m := namedModel(220, "Database", "Benefits", "BenefitsData", "hcm-integration")

	if lines := m.tabBarLines(""); len(lines) != 1 {
		t.Errorf("tabBarLines() = %d lines, want 1 on a wide terminal: %q", len(lines), lines)
	}
}

func TestTabBarLines_ScrollIndicatorIsKept(t *testing.T) {
	m := namedModel(60, "Database", "Benefits", "BenefitsData", "hcm-integration")
	indicator := scrollIndicatorFor(12)

	lines := m.tabBarLines(indicator)

	joined := stripAnsi(strings.Join(lines, "\n"))
	if !strings.Contains(joined, "↑ 12 lines") {
		t.Errorf("scroll indicator was dropped:\n%s", joined)
	}
}

func TestScrollIndicatorFor_EmptyAtBottom(t *testing.T) {
	if got := scrollIndicatorFor(0); got != "" {
		t.Errorf("scrollIndicatorFor(0) = %q, want empty", got)
	}
}

func TestView_FillsTerminalHeightExactly(t *testing.T) {
	// Larguras diferentes fazem abas e rodape quebrarem em 1..3 linhas; o
	// painel de log deve ceder o espaco correspondente para o layout continuar
	// caindo dentro da altura do terminal (chromeLines em sincronia com o que
	// e realmente desenhado).
	for _, width := range []int{60, 100, 160} {
		m := namedModel(width, "Database", "Benefits", "BenefitsData", "hcm-integration")
		m.height = 30

		lines := strings.Count(m.View(), "\n") + 1

		if lines != m.height {
			t.Errorf("width=%d: View() rendered %d lines, want exactly %d (tabs=%d, footer=%d)",
				width, lines, m.height, len(m.tabBarLines("")), len(m.footerLines()))
		}
	}
}
