package tui

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/JonathanBencke/ServiceManagerTUI/internal/config"
	"github.com/JonathanBencke/ServiceManagerTUI/internal/service"
	tea "github.com/charmbracelet/bubbletea"
)

func TestStatusIcon_GeneratingIsDistinct(t *testing.T) {
	icon := statusIcon("generating")

	if icon == "" {
		t.Fatal("statusIcon(\"generating\") = empty")
	}
	for _, other := range []string{"running", "building", "crashed", "stopping", "stopped", "idle"} {
		if statusIcon(other) == icon {
			t.Errorf("statusIcon(\"generating\") = %q, same as %q", icon, other)
		}
	}
}

func TestStatusStyle_GeneratingIsDistinctFromIdle(t *testing.T) {
	// Compara a cor configurada no estilo (e nao o texto renderizado, que perde
	// cor quando os testes rodam sem TTY).
	generating := statusStyle("generating").GetForeground()

	if generating == statusStyle("idle").GetForeground() {
		t.Error("statusStyle(\"generating\") falls back to the default/idle style")
	}
	if generating == statusStyle("building").GetForeground() {
		t.Error("statusStyle(\"generating\") uses the same color as \"building\"")
	}
}

// sdlModel builds a model with a single service whose workdir sits inside an
// SDL project, using genCmd as the generate-sources command.
func sdlModel(t *testing.T, genCmd string) Model {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.sdl"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	workdir := filepath.Join(root, "java", "impl")
	if err := os.MkdirAll(workdir, 0755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Presets: map[string]config.Preset{
			"stub": {Run: "cmd /c echo running", SdlGenerateCommand: genCmd},
		},
		Services: []config.ServiceConfig{
			{Name: "svc", Runner: "stub", Workdir: workdir},
		},
	}
	return NewModel(service.NewManager(cfg))
}

func waitForLog(t *testing.T, s *service.Service, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(strings.Join(s.Logs(), "\n"), want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for log %q, logs:\n%s", want, strings.Join(s.Logs(), "\n"))
}

func waitForServiceStatus(t *testing.T, s *service.Service, want service.Status, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if s.Status() == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for status %q, last = %q", want, s.Status())
}

func TestHandleNormalKey_POpensProfileSelect(t *testing.T) {
	m := namedModel(120, "svc")
	m.profiles = []string{"dev"}

	got, cmd := m.handleNormalKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	updated := got.(Model)

	if cmd != nil {
		t.Errorf("handleNormalKey(p) cmd = %v, want nil", cmd)
	}
	if updated.mode != ModeProfileSelect {
		t.Errorf("mode = %v, want ModeProfileSelect", updated.mode)
	}
	if want := profileCursorFor([]string{"dev"}, updated.availableProfiles); updated.profileCursor != want {
		t.Errorf("profileCursor = %d, want %d (matching current profile %q)", updated.profileCursor, want, "dev")
	}
}

func TestProfileCursorFor(t *testing.T) {
	available := []string{"none", "dev", "prod", "stg", "local"}

	tests := []struct {
		profiles []string
		want     int
	}{
		{profiles: nil, want: 0},        // "none"
		{profiles: []string{}, want: 0}, // "none"
		{profiles: []string{"dev"}, want: 1},
		{profiles: []string{"prod"}, want: 2},
		{profiles: []string{"unknown"}, want: 0}, // sem match, cai no default
	}

	for _, tt := range tests {
		if got := profileCursorFor(tt.profiles, available); got != tt.want {
			t.Errorf("profileCursorFor(%v, available) = %d, want %d", tt.profiles, got, tt.want)
		}
	}
}

func TestHandleNormalKey_RTriggersGenerateSources(t *testing.T) {
	m := sdlModel(t, "cmd /c echo generating-from-tui")

	if _, cmd := m.handleNormalKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}}); cmd != nil {
		t.Errorf("handleNormalKey(r) cmd = %v, want nil (runs in background)", cmd)
	}

	waitForLog(t, m.services[0], "generating-from-tui", 15*time.Second)
}

func TestGenerateSources_NoopOnMCPTab(t *testing.T) {
	m := sdlModel(t, "cmd /c echo generating-from-tui")
	m.selectedLog = len(m.services) // aba do MCP

	m.generateSources(m.selectedLog)
	time.Sleep(200 * time.Millisecond)

	if logs := strings.Join(m.services[0].Logs(), "\n"); logs != "" {
		t.Errorf("service logs should stay empty when the MCP tab is selected:\n%s", logs)
	}
}

// workdirModel builds a model with a single run-only service that lists its
// working directory, returning the model plus the configured workdir. Each
// directory carries a uniquely named marker file so the logs reveal where the
// command ran.
func workdirModel(t *testing.T) (Model, string) {
	t.Helper()
	workdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workdir, "configured-marker.txt"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Presets:  map[string]config.Preset{"stub": {Run: "cmd /c dir /b"}},
		Services: []config.ServiceConfig{{Name: "svc", Runner: "stub", Workdir: workdir}},
	}
	return NewModel(service.NewManager(cfg)), workdir
}

func TestToggleService_AlwaysUsesConfiguredWorkdir(t *testing.T) {
	m, configured := workdirModel(t)
	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, "worktree-marker.txt"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	s := m.services[0]

	// Simula o start vindo do MCP (start_service_at) em um git worktree.
	if err := s.StartAt(worktree); err != nil {
		t.Fatalf("StartAt() error = %v", err)
	}
	waitForServiceStatus(t, s, service.StatusStopped, 10*time.Second)
	waitForLog(t, s, "worktree-marker.txt", 10*time.Second)

	m.toggleService(0)

	waitForLog(t, s, "configured-marker.txt", 10*time.Second)
	waitForServiceStatus(t, s, service.StatusStopped, 10*time.Second)
	if got := s.Workdir(); got != configured {
		t.Errorf("Workdir() = %q, want %q (terminal start must obey the config)", got, configured)
	}
	if got := s.WorkdirOverride(); got != "" {
		t.Errorf("WorkdirOverride() = %q, want empty after a terminal start", got)
	}
}

func TestHandleNormalKey_StartAllUsesConfiguredWorkdir(t *testing.T) {
	m, configured := workdirModel(t)
	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, "worktree-marker.txt"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	s := m.services[0]

	if err := s.StartAt(worktree); err != nil {
		t.Fatalf("StartAt() error = %v", err)
	}
	waitForServiceStatus(t, s, service.StatusStopped, 10*time.Second)

	if _, cmd := m.handleNormalKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}}); cmd != nil {
		t.Errorf("handleNormalKey(a) cmd = %v, want nil (runs in background)", cmd)
	}

	waitForLog(t, s, "configured-marker.txt", 10*time.Second)
	waitForServiceStatus(t, s, service.StatusStopped, 10*time.Second)
	if got := s.Workdir(); got != configured {
		t.Errorf("Workdir() = %q, want %q ('a' must obey the config)", got, configured)
	}
}

func TestToggleService_NoopWhileGenerating(t *testing.T) {
	m := sdlModel(t, "cmd /c ping -n 4 127.0.0.1")
	s := m.services[0]

	m.generateSources(0)
	waitForServiceStatus(t, s, service.StatusGenerating, 10*time.Second)

	m.toggleService(0)

	if got := s.Status(); got != service.StatusGenerating {
		t.Errorf("Status() = %q, want %q (Enter/Espaço must be a no-op while generating)", got, service.StatusGenerating)
	}

	// Espera o fim da geração para o TempDir poder ser removido no cleanup.
	waitForServiceStatus(t, s, service.StatusIdle, 20*time.Second)
}

func TestHandleNormalKey_ShiftRRestartsPreservingWorkdirOverride(t *testing.T) {
	worktree := t.TempDir()
	cfg := &config.Config{
		Presets:  map[string]config.Preset{"stub": {Run: "cmd /c ping -n 30 127.0.0.1"}},
		Services: []config.ServiceConfig{{Name: "svc", Runner: "stub", Workdir: t.TempDir()}},
	}
	m := NewModel(service.NewManager(cfg))
	s := m.services[0]

	// Simula um start vindo do MCP (start_service_at) em um git worktree.
	if err := s.StartAt(worktree); err != nil {
		t.Fatalf("StartAt() error = %v", err)
	}
	waitForServiceStatus(t, s, service.StatusRunning, 10*time.Second)
	firstPID := s.PID()

	if _, cmd := m.handleNormalKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}}); cmd != nil {
		t.Errorf("handleNormalKey(R) cmd = %v, want nil (runs in background)", cmd)
	}
	defer s.Stop() // libera o handle do workdir (t.TempDir()) antes do cleanup do teste

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if s.Status() == service.StatusRunning && s.PID() != firstPID && s.PID() > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if got := s.Status(); got != service.StatusRunning {
		t.Fatalf("Status() after R = %q, want running", got)
	}
	if got := s.PID(); got == firstPID {
		t.Errorf("PID() after R = %d, want a new PID (restarted)", got)
	}
	if got := s.Workdir(); !strings.EqualFold(got, worktree) {
		t.Errorf("Workdir() after R = %q, want the preserved override %q (unlike Enter/'a', R must keep it)", got, worktree)
	}
}

func TestHandleNormalKey_ShiftRNoopWhileGenerating(t *testing.T) {
	m := sdlModel(t, "cmd /c ping -n 4 127.0.0.1")
	s := m.services[0]

	m.generateSources(0)
	waitForServiceStatus(t, s, service.StatusGenerating, 10*time.Second)

	if _, cmd := m.handleNormalKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}}); cmd != nil {
		t.Errorf("handleNormalKey(R) cmd = %v, want nil", cmd)
	}

	if got := s.Status(); got != service.StatusGenerating {
		t.Errorf("Status() = %q, want %q (R must be a no-op while generating)", got, service.StatusGenerating)
	}

	waitForServiceStatus(t, s, service.StatusIdle, 20*time.Second)
}

// runningModel builds a model with a single long-running service already
// started, so tests can exercise the confirmation gate in front of
// destructive shortcuts (x, q/ctrl+c).
func runningModel(t *testing.T) Model {
	t.Helper()
	cfg := &config.Config{
		Presets:  map[string]config.Preset{"stub": {Run: "cmd /c ping -n 30 127.0.0.1"}},
		Services: []config.ServiceConfig{{Name: "svc", Runner: "stub", Workdir: t.TempDir()}},
	}
	m := NewModel(service.NewManager(cfg))
	s := m.services[0]
	if err := s.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	waitForServiceStatus(t, s, service.StatusRunning, 10*time.Second)
	t.Cleanup(func() { s.Stop() })
	return m
}

func TestHandleNormalKey_XWithRunningServices_OpensConfirm(t *testing.T) {
	m := runningModel(t)

	got, cmd := m.handleNormalKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	updated := got.(Model)

	if cmd != nil {
		t.Errorf("handleNormalKey(x) cmd = %v, want nil", cmd)
	}
	if updated.mode != ModeConfirm {
		t.Fatalf("mode = %v, want ModeConfirm", updated.mode)
	}
	if updated.confirmAction != "stopAll" {
		t.Errorf("confirmAction = %q, want %q", updated.confirmAction, "stopAll")
	}
	if updated.confirmMessage == "" {
		t.Error("confirmMessage is empty, want a human-readable question")
	}
	// x nao deve ter parado nada ainda - so abriu a confirmacao.
	if got := m.services[0].Status(); got != service.StatusRunning {
		t.Errorf("Status() = %q, want still running before confirmation", got)
	}
}

func TestHandleNormalKey_XWithNoRunningServices_NoConfirm(t *testing.T) {
	m := namedModel(120, "svc") // nenhum servico rodando

	got, cmd := m.handleNormalKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	updated := got.(Model)

	if cmd != nil {
		t.Errorf("handleNormalKey(x) cmd = %v, want nil", cmd)
	}
	if updated.mode != ModeNormal {
		t.Errorf("mode = %v, want ModeNormal (nothing to confirm)", updated.mode)
	}
}

func TestHandleNormalKey_QWithRunningServices_OpensConfirm(t *testing.T) {
	m := runningModel(t)

	got, cmd := m.handleNormalKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	updated := got.(Model)

	if cmd != nil {
		t.Errorf("handleNormalKey(q) cmd = %v, want nil (must not quit yet)", cmd)
	}
	if updated.quitting {
		t.Error("quitting = true, want false before confirmation")
	}
	if updated.mode != ModeConfirm {
		t.Fatalf("mode = %v, want ModeConfirm", updated.mode)
	}
	if updated.confirmAction != "quit" {
		t.Errorf("confirmAction = %q, want %q", updated.confirmAction, "quit")
	}
}

func TestHandleNormalKey_QWithNoRunningServices_QuitsImmediately(t *testing.T) {
	m := namedModel(120, "svc")

	got, cmd := m.handleNormalKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	updated := got.(Model)

	if !updated.quitting {
		t.Error("quitting = false, want true (nothing running, no confirmation needed)")
	}
	if cmd == nil {
		t.Error("cmd = nil, want tea.Quit")
	}
}

func TestHandleConfirmKey_YExecutesStopAll(t *testing.T) {
	m := runningModel(t)
	m.mode = ModeConfirm
	m.confirmAction = "stopAll"
	s := m.services[0]

	got, _ := m.handleConfirmKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	updated := got.(Model)

	if updated.mode != ModeNormal {
		t.Errorf("mode = %v, want ModeNormal after confirming", updated.mode)
	}
	waitForServiceStatus(t, s, service.StatusStopped, 10*time.Second)
}

func TestHandleConfirmKey_YExecutesQuit(t *testing.T) {
	m := runningModel(t)
	m.mode = ModeConfirm
	m.confirmAction = "quit"

	got, cmd := m.handleConfirmKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	updated := got.(Model)

	if !updated.quitting {
		t.Error("quitting = false, want true after confirming quit")
	}
	if cmd == nil {
		t.Error("cmd = nil, want tea.Quit")
	}
}

func TestHandleConfirmKey_NCancelsWithoutSideEffects(t *testing.T) {
	m := runningModel(t)
	m.mode = ModeConfirm
	m.confirmAction = "stopAll"
	s := m.services[0]

	got, cmd := m.handleConfirmKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	updated := got.(Model)

	if cmd != nil {
		t.Errorf("handleConfirmKey(n) cmd = %v, want nil", cmd)
	}
	if updated.mode != ModeNormal {
		t.Errorf("mode = %v, want ModeNormal after cancelling", updated.mode)
	}
	if updated.confirmAction != "" {
		t.Errorf("confirmAction = %q, want empty after cancelling", updated.confirmAction)
	}
	time.Sleep(200 * time.Millisecond)
	if got := s.Status(); got != service.StatusRunning {
		t.Errorf("Status() = %q, want still running (cancel must not stop it)", got)
	}
}

// logSearchModel builds a model with a single service whose log already
// contains a couple of matching lines for the pattern "find-me" (one of them
// via the substring "find-me-again"), stopped by the time it returns so the
// buffer is stable for assertions.
func logSearchModel(t *testing.T) Model {
	t.Helper()
	cfg := &config.Config{
		Presets:  map[string]config.Preset{"stub": {Run: "cmd /c echo lineA && echo find-me && echo lineB && echo find-me-again"}},
		Services: []config.ServiceConfig{{Name: "svc", Runner: "stub", Workdir: t.TempDir()}},
	}
	m := NewModel(service.NewManager(cfg))
	s := m.services[0]
	if err := s.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	waitForServiceStatus(t, s, service.StatusStopped, 10*time.Second)
	return m
}

func TestHandleNormalKey_SlashOpensLogSearch(t *testing.T) {
	m := namedModel(120, "svc")
	m.logSearchInput = "stale" // deve ser limpo ao abrir de novo

	got, cmd := m.handleNormalKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	updated := got.(Model)

	if cmd != nil {
		t.Errorf("handleNormalKey(/) cmd = %v, want nil", cmd)
	}
	if updated.mode != ModeLogSearch {
		t.Fatalf("mode = %v, want ModeLogSearch", updated.mode)
	}
	if updated.logSearchInput != "" {
		t.Errorf("logSearchInput = %q, want empty on a fresh search", updated.logSearchInput)
	}
}

func TestHandleLogSearchKey_TypingBackspaceAndEsc(t *testing.T) {
	m := namedModel(120, "svc")
	m.mode = ModeLogSearch

	for _, r := range "err" {
		got, _ := m.handleLogSearchKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = got.(Model)
	}
	if m.logSearchInput != "err" {
		t.Fatalf("logSearchInput = %q, want %q", m.logSearchInput, "err")
	}

	got, _ := m.handleLogSearchKey(tea.KeyMsg{Type: tea.KeyBackspace})
	m = got.(Model)
	if m.logSearchInput != "er" {
		t.Errorf("logSearchInput after backspace = %q, want %q", m.logSearchInput, "er")
	}

	got, cmd := m.handleLogSearchKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = got.(Model)
	if cmd != nil {
		t.Errorf("handleLogSearchKey(esc) cmd = %v, want nil", cmd)
	}
	if m.mode != ModeNormal {
		t.Errorf("mode = %v, want ModeNormal after Esc", m.mode)
	}
}

func TestRunLogSearch_FindsMatchesAndJumpsToLatest(t *testing.T) {
	m := logSearchModel(t)

	updated := m.runLogSearch("find-me")

	if updated.logSearchQuery != "find-me" {
		t.Errorf("logSearchQuery = %q, want %q", updated.logSearchQuery, "find-me")
	}
	// 3 linhas casam: o próprio log "[1/1] Starting (...)" que ecoa o comando
	// resolvido (contém "find-me" e "find-me-again" no texto do comando) mais
	// as 2 linhas de saída real do echo.
	if got := len(updated.logSearchMatches); got != 3 {
		t.Fatalf("len(logSearchMatches) = %d, want 3 (linha de Starting + find-me + find-me-again)", got)
	}
	last := len(updated.logSearchMatches) - 1
	if updated.logSearchMatchIdx != last {
		t.Errorf("logSearchMatchIdx = %d, want %d (points at the latest/last match)", updated.logSearchMatchIdx, last)
	}
	if updated.logSearchTab != updated.selectedLog {
		t.Errorf("logSearchTab = %d, want %d (selected tab at confirm time)", updated.logSearchTab, updated.selectedLog)
	}
}

func TestRunLogSearch_EmptyPatternClearsPreviousSearch(t *testing.T) {
	m := logSearchModel(t)
	withSearch := m.runLogSearch("find-me")
	if len(withSearch.logSearchMatches) == 0 {
		t.Fatal("precondition: expected matches before clearing")
	}

	cleared := withSearch.runLogSearch("")

	if cleared.logSearchQuery != "" {
		t.Errorf("logSearchQuery = %q, want empty after clearing", cleared.logSearchQuery)
	}
	if cleared.logSearchRe != nil {
		t.Error("logSearchRe != nil, want nil after clearing")
	}
	if len(cleared.logSearchMatches) != 0 {
		t.Errorf("logSearchMatches = %v, want empty after clearing", cleared.logSearchMatches)
	}
}

func TestNextLogMatch_CyclesForwardAndBackward(t *testing.T) {
	m := logSearchModel(t)
	m = m.runLogSearch("find-me")
	last := len(m.logSearchMatches) - 1
	if m.logSearchMatchIdx != last {
		t.Fatalf("precondition: logSearchMatchIdx = %d, want %d", m.logSearchMatchIdx, last)
	}

	m.nextLogMatch(1) // deve dar a volta para o primeiro match
	if m.logSearchMatchIdx != 0 {
		t.Errorf("logSearchMatchIdx after +1 wrap = %d, want 0", m.logSearchMatchIdx)
	}

	m.nextLogMatch(-1) // deve voltar para o ultimo
	if m.logSearchMatchIdx != last {
		t.Errorf("logSearchMatchIdx after -1 wrap = %d, want %d", m.logSearchMatchIdx, last)
	}
}

func TestNextLogMatch_NoopAfterSwitchingTab(t *testing.T) {
	m := logSearchModel(t)
	m = m.runLogSearch("find-me")
	before := m.logSearchMatchIdx

	m.selectedLog = -1 // simula ter saido da aba onde a busca rodou

	m.nextLogMatch(1)

	if m.logSearchMatchIdx != before {
		t.Errorf("logSearchMatchIdx = %d, want unchanged %d after switching tabs", m.logSearchMatchIdx, before)
	}
}

// freePort asks the OS for an available TCP port on 127.0.0.1 by binding a
// throwaway listener and closing it immediately.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func TestCollectHealth_ChecksOnlyRunningServicesWithHealthPort(t *testing.T) {
	openPort := freePort(t)
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(openPort)))
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer listener.Close()
	closedPort := freePort(t) // ninguem escutando nele

	cfg := &config.Config{
		Presets: map[string]config.Preset{"stub": {Run: "cmd /c ping -n 30 127.0.0.1"}},
		Services: []config.ServiceConfig{
			{Name: "open", Runner: "stub", Workdir: t.TempDir(), HealthPort: openPort},
			{Name: "closed", Runner: "stub", Workdir: t.TempDir(), HealthPort: closedPort},
			{Name: "no-port", Runner: "stub", Workdir: t.TempDir()}, // HealthPort == 0
			{Name: "not-started", Runner: "stub", Workdir: t.TempDir(), HealthPort: openPort},
		},
	}
	m := NewModel(service.NewManager(cfg))
	for _, name := range []string{"open", "closed", "no-port"} {
		s := m.services[serviceIndex(m, name)]
		if err := s.Start(); err != nil {
			t.Fatalf("Start(%s) error = %v", name, err)
		}
		waitForServiceStatus(t, s, service.StatusRunning, 10*time.Second)
		defer s.Stop()
	}
	// "not-started" fica idle de proposito.

	msg := collectHealth(m.services)()
	health := msg.(healthMsg).health

	if got, ok := health["open"]; !ok || !got {
		t.Errorf("health[open] = (%v, %v), want (true, true)", got, ok)
	}
	if got, ok := health["closed"]; !ok || got {
		t.Errorf("health[closed] = (%v, %v), want (false, true)", got, ok)
	}
	if _, ok := health["no-port"]; ok {
		t.Error("health[no-port] present, want absent (no health_port configured)")
	}
	if _, ok := health["not-started"]; ok {
		t.Error("health[not-started] present, want absent (service is not running)")
	}
}

func TestHandleNormalKey_IOpensInfoOnValidService(t *testing.T) {
	m := namedModel(120, "svc")

	got, cmd := m.handleNormalKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	updated := got.(Model)

	if cmd != nil {
		t.Errorf("handleNormalKey(i) cmd = %v, want nil", cmd)
	}
	if updated.mode != ModeInfo {
		t.Errorf("mode = %v, want ModeInfo", updated.mode)
	}
}

func TestHandleNormalKey_INoopOnMCPTab(t *testing.T) {
	m := namedModel(120, "svc")
	m.selectedLog = len(m.services) // fora do intervalo de servicos (aba MCP)

	got, _ := m.handleNormalKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	updated := got.(Model)

	if updated.mode != ModeNormal {
		t.Errorf("mode = %v, want ModeNormal (no service selected)", updated.mode)
	}
}

func TestHandleInfoKey_AnyKeyReturnsToNormal(t *testing.T) {
	m := namedModel(120, "svc")
	m.mode = ModeInfo

	got, cmd := m.handleInfoKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	updated := got.(Model)

	if cmd != nil {
		t.Errorf("handleInfoKey(x) cmd = %v, want nil", cmd)
	}
	if updated.mode != ModeNormal {
		t.Errorf("mode = %v, want ModeNormal", updated.mode)
	}
}

// withTempWorkdir chdirs into a fresh t.TempDir() for the duration of the
// test, restoring the original working directory on cleanup. Used to test
// saveCurrentLog/writeLogFile without polluting the repo with a "logs" dir.
func withTempWorkdir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir(%s): %v", dir, err)
	}
	t.Cleanup(func() { os.Chdir(orig) })
	return dir
}

func TestSaveCurrentLog_WritesFileAndSetsSuccessNotice(t *testing.T) {
	withTempWorkdir(t)
	m := logSearchModel(t)
	m.selectedLog = 0

	updated := m.saveCurrentLog()

	if updated.noticeErr {
		t.Fatalf("noticeErr = true, notice = %q, want success", updated.notice)
	}
	if !strings.Contains(updated.notice, logSaveDir) {
		t.Errorf("notice = %q, want it to mention the %q dir", updated.notice, logSaveDir)
	}

	entries, err := os.ReadDir(logSaveDir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", logSaveDir, err)
	}
	if len(entries) != 1 {
		t.Fatalf("%s dir has %d entries, want 1", logSaveDir, len(entries))
	}
	if !strings.HasPrefix(entries[0].Name(), "svc_") {
		t.Errorf("file name = %q, want it prefixed with the service name", entries[0].Name())
	}

	content, err := os.ReadFile(filepath.Join(logSaveDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(content), "find-me-again") {
		t.Errorf("saved log file missing expected content:\n%s", content)
	}
}

func TestSaveCurrentLog_EmptyLogSetsErrorNotice(t *testing.T) {
	withTempWorkdir(t)
	m := namedModel(120, "svc") // servico nunca iniciado, log vazio

	updated := m.saveCurrentLog()

	if !updated.noticeErr {
		t.Errorf("noticeErr = false, want true (empty log)")
	}
	if _, err := os.Stat(logSaveDir); !os.IsNotExist(err) {
		t.Errorf("logs dir should not be created when there is nothing to save (err=%v)", err)
	}
}

func TestSanitizeFileName_ReplacesInvalidWindowsChars(t *testing.T) {
	got := sanitizeFileName(`My Service: a/b\c*d?"e<f>g|h`)
	if strings.ContainsAny(got, ` :/\*?"<>|`) {
		t.Errorf("sanitizeFileName() = %q, still contains invalid characters", got)
	}
}

func TestServiceRowAt_MapsClickedRowToServiceIndex(t *testing.T) {
	m := namedModel(120, "svcA", "svcB", "svcC")

	tests := []struct {
		y       int
		wantIdx int
		wantOK  bool
	}{
		{y: 0, wantOK: false}, // header
		{y: 1, wantOK: false}, // linha em branco
		{y: 2, wantOK: false}, // labels da tabela
		{y: 3, wantOK: false}, // borda da tabela
		{y: 4, wantIdx: 0, wantOK: true},
		{y: 5, wantIdx: 1, wantOK: true},
		{y: 6, wantIdx: 2, wantOK: true},
		{y: 7, wantOK: false}, // abaixo da ultima linha de servico
	}

	for _, tt := range tests {
		idx, ok := m.serviceRowAt(tt.y)
		if ok != tt.wantOK {
			t.Errorf("serviceRowAt(%d) ok = %v, want %v", tt.y, ok, tt.wantOK)
			continue
		}
		if ok && idx != tt.wantIdx {
			t.Errorf("serviceRowAt(%d) idx = %d, want %d", tt.y, idx, tt.wantIdx)
		}
	}
}

func TestHandleMouse_LeftClickOnRowSelectsService(t *testing.T) {
	m := namedModel(120, "svcA", "svcB", "svcC")
	m.selectedLog = 0
	m.logScroll = 7

	got, cmd := m.handleMouse(tea.MouseMsg{X: 3, Y: 5, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	updated := got.(Model)

	if cmd != nil {
		t.Errorf("handleMouse(click) cmd = %v, want nil", cmd)
	}
	if updated.selectedLog != 1 {
		t.Errorf("selectedLog = %d, want 1 (row at Y=5)", updated.selectedLog)
	}
	if updated.logScroll != 0 {
		t.Errorf("logScroll = %d, want reset to 0 on tab switch", updated.logScroll)
	}
}

func TestHandleMouse_ClickOutsideTableIsNoop(t *testing.T) {
	m := namedModel(120, "svcA", "svcB")
	m.selectedLog = 0

	got, _ := m.handleMouse(tea.MouseMsg{X: 3, Y: 0, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	updated := got.(Model)

	if updated.selectedLog != 0 {
		t.Errorf("selectedLog = %d, want unchanged 0 (click on the header, not a row)", updated.selectedLog)
	}
}

func TestHandleMouse_WheelScrollsLog(t *testing.T) {
	m := namedModel(120, "svc")
	m.logScroll = 2

	got, _ := m.handleMouse(tea.MouseMsg{Button: tea.MouseButtonWheelUp})
	updated := got.(Model)
	if updated.logScroll != 3 {
		t.Errorf("logScroll after wheel up = %d, want 3", updated.logScroll)
	}

	got, _ = updated.handleMouse(tea.MouseMsg{Button: tea.MouseButtonWheelDown})
	updated = got.(Model)
	if updated.logScroll != 2 {
		t.Errorf("logScroll after wheel down = %d, want 2", updated.logScroll)
	}
}

func TestHandleMouse_WheelDownNeverGoesNegative(t *testing.T) {
	m := namedModel(120, "svc")
	m.logScroll = 0

	got, _ := m.handleMouse(tea.MouseMsg{Button: tea.MouseButtonWheelDown})
	updated := got.(Model)

	if updated.logScroll != 0 {
		t.Errorf("logScroll = %d, want 0 (must not go negative)", updated.logScroll)
	}
}

func serviceIndex(m Model, name string) int {
	for i, s := range m.services {
		if s.Name() == name {
			return i
		}
	}
	return -1
}
