package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JonathanBencke/ServiceManagerTUI/internal/config"
	"github.com/JonathanBencke/ServiceManagerTUI/internal/service"
	"github.com/mark3labs/mcp-go/mcp"
)

// decodeStructured re-decodes the structuredContent of a tool result into the
// caller's DTO, mirroring what an MCP client does.
func decodeStructured[T any](t *testing.T, res *mcp.CallToolResult) T {
	t.Helper()
	if res.StructuredContent == nil {
		t.Fatalf("result has no structuredContent: %s", resultText(res))
	}
	data, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshalling structuredContent: %v", err)
	}
	var out T
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("decoding structuredContent %s: %v", data, err)
	}
	return out
}

// loggingServer returns a server whose service "svc" prints marker lines and
// exits, so the log buffer has deterministic content. The echoes live in a
// batch file on purpose: the service logs the command line it runs, so inlining
// them would make every marker appear twice.
func loggingServer(t *testing.T) (*MCPServer, *service.Service) {
	t.Helper()
	workdir := t.TempDir()
	script := "@echo off\r\necho hello-world\r\necho ERROR boom failed\r\necho tail-line\r\n"
	if err := os.WriteFile(filepath.Join(workdir, "run.bat"), []byte(script), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Presets:  map[string]config.Preset{"stub": {Run: `cmd /c .\run.bat`}},
		Services: []config.ServiceConfig{{Name: "svc", Runner: "stub", Workdir: workdir}},
	}
	srv := NewServerFromManager(service.NewManager(cfg))
	svc := srv.Manager().ServiceByName("svc")

	if _, err := srv.handleStartService(context.Background(),
		callToolRequest("start_service", map[string]any{"name": "svc"})); err != nil {
		t.Fatalf("handleStartService() error = %v", err)
	}
	waitForStatus(t, svc, service.StatusStopped, 15*time.Second)
	return srv, svc
}

// longRunningServer returns a server whose service "svc" stays alive, with the
// given health port declared (0 for none). The service is stopped on cleanup.
func longRunningServer(t *testing.T, healthPort int) (*MCPServer, *service.Service) {
	t.Helper()
	workdir := t.TempDir()
	cfg := &config.Config{
		Presets: map[string]config.Preset{"stub": {Run: "cmd /c ping -n 30 127.0.0.1"}},
		Services: []config.ServiceConfig{{
			Name: "svc", Runner: "stub", Workdir: workdir, HealthPort: healthPort,
		}},
	}
	srv := NewServerFromManager(service.NewManager(cfg))
	svc := srv.Manager().ServiceByName("svc")

	if err := svc.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	waitForStatus(t, svc, service.StatusRunning, 15*time.Second)

	t.Cleanup(func() {
		if isStoppable(svc.Status()) {
			svc.Stop()
		}
		waitForStatus(t, svc, service.StatusStopped, 15*time.Second)
	})
	return srv, svc
}

func TestHandleListServices_StructuredPayload(t *testing.T) {
	srv, configured := startableServer(t)

	res, err := srv.handleListServices(context.Background(), callToolRequest("list_services", nil))
	if err != nil {
		t.Fatalf("handleListServices() error = %v", err)
	}

	dto := decodeStructured[ServiceListDTO](t, res)
	if dto.Total != 1 {
		t.Fatalf("Total = %d, want 1", dto.Total)
	}
	if dto.Services[0].Name != "svc" {
		t.Errorf("Services[0].Name = %q, want %q", dto.Services[0].Name, "svc")
	}
	if dto.Services[0].ConfiguredWorkdir != configured {
		t.Errorf("ConfiguredWorkdir = %q, want %q", dto.Services[0].ConfiguredWorkdir, configured)
	}
	// The ASCII table is kept for clients that only read the text content.
	if text := resultText(res); !strings.Contains(text, "| Name") {
		t.Errorf("text rendering lost the table header:\n%s", text)
	}
}

func TestHandleGetLogs_SinceIndexReturnsOnlyNewLines(t *testing.T) {
	srv, svc := loggingServer(t)

	first, err := srv.handleGetLogs(context.Background(), callToolRequest("get_logs", map[string]any{"name": "svc"}))
	if err != nil {
		t.Fatalf("handleGetLogs() error = %v", err)
	}
	firstDTO := decodeStructured[LogsDTO](t, first)
	if firstDTO.ReturnedLines == 0 {
		t.Fatal("first call returned no lines")
	}

	second, err := srv.handleGetLogs(context.Background(),
		callToolRequest("get_logs", map[string]any{"name": "svc", "since_index": firstDTO.LastLineIndex}))
	if err != nil {
		t.Fatalf("handleGetLogs() error = %v", err)
	}
	secondDTO := decodeStructured[LogsDTO](t, second)
	if secondDTO.ReturnedLines != 0 {
		t.Errorf("ReturnedLines = %d, want 0 (nothing new since the cursor): %v", secondDTO.ReturnedLines, secondDTO.Lines)
	}
	if secondDTO.LastLineIndex != firstDTO.LastLineIndex {
		t.Errorf("LastLineIndex = %d, want it unchanged at %d", secondDTO.LastLineIndex, firstDTO.LastLineIndex)
	}

	svc.Logs() // keep svc referenced; the buffer is the subject under test
}

func TestHandleGetLogs_TailIsTruncated(t *testing.T) {
	srv, _ := loggingServer(t)

	res, err := srv.handleGetLogs(context.Background(),
		callToolRequest("get_logs", map[string]any{"name": "svc", "lines": 1}))
	if err != nil {
		t.Fatalf("handleGetLogs() error = %v", err)
	}

	dto := decodeStructured[LogsDTO](t, res)
	if dto.ReturnedLines != 1 {
		t.Errorf("ReturnedLines = %d, want 1", dto.ReturnedLines)
	}
	if !dto.Truncated {
		t.Error("Truncated = false, want true when older lines were cut")
	}
	if dto.FirstLineIndex != dto.LastLineIndex-1 {
		t.Errorf("FirstLineIndex = %d, want %d", dto.FirstLineIndex, dto.LastLineIndex-1)
	}
}

func TestHandleSearchLogs_FindsMatchWithContext(t *testing.T) {
	srv, _ := loggingServer(t)

	res, err := srv.handleSearchLogs(context.Background(), callToolRequest("search_logs", map[string]any{
		"name": "svc", "pattern": "boom", "context_lines": 1,
	}))
	if err != nil {
		t.Fatalf("handleSearchLogs() error = %v", err)
	}

	dto := decodeStructured[SearchLogsDTO](t, res)
	if dto.MatchCount != 1 {
		t.Fatalf("MatchCount = %d, want 1: %+v", dto.MatchCount, dto.Matches)
	}
	if !strings.Contains(dto.Matches[0].Text, "boom") {
		t.Errorf("match text = %q, want it to contain %q", dto.Matches[0].Text, "boom")
	}
	if len(dto.Matches[0].Before) == 0 && len(dto.Matches[0].After) == 0 {
		t.Error("context_lines=1 produced no surrounding lines")
	}
}

func TestHandleSearchLogs_LevelFilter(t *testing.T) {
	srv, _ := loggingServer(t)

	res, err := srv.handleSearchLogs(context.Background(), callToolRequest("search_logs", map[string]any{
		"name": "svc", "pattern": "hello-world", "level": "error",
	}))
	if err != nil {
		t.Fatalf("handleSearchLogs() error = %v", err)
	}

	dto := decodeStructured[SearchLogsDTO](t, res)
	if dto.MatchCount != 0 {
		t.Errorf("MatchCount = %d, want 0: an info line must not pass the 'error' filter", dto.MatchCount)
	}
	if dto.ScannedLines == 0 {
		t.Error("ScannedLines = 0, want the buffered lines to be reported")
	}
}

func TestHandleSearchLogs_InvalidPattern(t *testing.T) {
	srv, _ := loggingServer(t)

	res, err := srv.handleSearchLogs(context.Background(), callToolRequest("search_logs", map[string]any{
		"name": "svc", "pattern": "([unclosed",
	}))
	if err != nil {
		t.Fatalf("handleSearchLogs() error = %v", err)
	}
	if !res.IsError {
		t.Fatal("IsError = false, want true for an invalid regular expression")
	}
	if text := resultText(res); !strings.Contains(text, "invalid 'pattern'") {
		t.Errorf("error = %q, want it to explain the bad pattern", text)
	}
}

func TestHandleSearchLogs_RespectsMaxResults(t *testing.T) {
	srv, _ := loggingServer(t)

	res, err := srv.handleSearchLogs(context.Background(), callToolRequest("search_logs", map[string]any{
		"name": "svc", "pattern": ".", "max_results": 1,
	}))
	if err != nil {
		t.Fatalf("handleSearchLogs() error = %v", err)
	}

	dto := decodeStructured[SearchLogsDTO](t, res)
	if dto.MatchCount != 1 {
		t.Errorf("MatchCount = %d, want 1 (capped)", dto.MatchCount)
	}
	if !dto.Truncated {
		t.Error("Truncated = false, want true when the cap cut the results")
	}
}

func TestHandleGetStats_ReportsStoppedService(t *testing.T) {
	srv, _ := loggingServer(t)

	res, err := srv.handleGetStats(context.Background(), callToolRequest("get_stats", nil))
	if err != nil {
		t.Fatalf("handleGetStats() error = %v", err)
	}

	dto := decodeStructured[StatsListDTO](t, res)
	if len(dto.Services) != 1 {
		t.Fatalf("Services = %d, want 1", len(dto.Services))
	}
	if dto.Services[0].SampledMs != 0 {
		t.Errorf("SampledMs = %d, want 0 for a service with no process", dto.Services[0].SampledMs)
	}
}

func TestHandleGetStats_SamplesRunningService(t *testing.T) {
	srv, _ := longRunningServer(t, 0)

	res, err := srv.handleGetStats(context.Background(), callToolRequest("get_stats", map[string]any{"name": "svc"}))
	if err != nil {
		t.Fatalf("handleGetStats() error = %v", err)
	}

	dto := decodeStructured[StatsListDTO](t, res)
	if len(dto.Services) != 1 {
		t.Fatalf("Services = %d, want 1", len(dto.Services))
	}
	if dto.Services[0].SampledMs <= 0 {
		t.Errorf("SampledMs = %d, want a real sampling window", dto.Services[0].SampledMs)
	}
	if dto.Services[0].MemBytes <= 0 {
		t.Errorf("MemBytes = %d, want the running process tree to use memory", dto.Services[0].MemBytes)
	}
}

func TestHandleWaitUntilReady_RunningWithoutHealthPort(t *testing.T) {
	srv, _ := longRunningServer(t, 0)

	res, err := srv.handleWaitUntilReady(context.Background(),
		callToolRequest("wait_until_ready", map[string]any{"name": "svc", "timeout_seconds": 10}))
	if err != nil {
		t.Fatalf("handleWaitUntilReady() error = %v", err)
	}

	dto := decodeStructured[WaitResultDTO](t, res)
	if !dto.Ready {
		t.Errorf("Ready = false, want true: %s", dto.Message)
	}
	if dto.Status != string(service.StatusRunning) {
		t.Errorf("Status = %q, want %q", dto.Status, service.StatusRunning)
	}
}

func TestHandleWaitUntilReady_FailsFastWhenNotStarted(t *testing.T) {
	srv, _ := startableServer(t)

	start := time.Now()
	res, err := srv.handleWaitUntilReady(context.Background(),
		callToolRequest("wait_until_ready", map[string]any{"name": "svc", "timeout_seconds": 30}))
	if err != nil {
		t.Fatalf("handleWaitUntilReady() error = %v", err)
	}

	dto := decodeStructured[WaitResultDTO](t, res)
	if dto.Ready {
		t.Error("Ready = true, want false for a service that was never started")
	}
	if dto.TimedOut {
		t.Error("TimedOut = true, want false: an idle service must fail fast, not burn the timeout")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("took %s, want an immediate answer", elapsed)
	}
}

func TestHandleWaitUntilReady_TimesOutOnClosedHealthPort(t *testing.T) {
	srv, _ := longRunningServer(t, freePort(t))

	res, err := srv.handleWaitUntilReady(context.Background(),
		callToolRequest("wait_until_ready", map[string]any{"name": "svc", "timeout_seconds": 1}))
	if err != nil {
		t.Fatalf("handleWaitUntilReady() error = %v", err)
	}

	dto := decodeStructured[WaitResultDTO](t, res)
	if dto.Ready {
		t.Error("Ready = true, want false while the health port refuses connections")
	}
	if !dto.TimedOut {
		t.Errorf("TimedOut = false, want true: %s", dto.Message)
	}
	if len(dto.RecentLogs) == 0 {
		t.Error("RecentLogs is empty, want the log tail to help diagnose the timeout")
	}
}

func TestWaitUntilReady_HonoursContextCancellation(t *testing.T) {
	_, svc := longRunningServer(t, freePort(t))

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	dto := waitUntilReady(ctx, svc, 5*time.Minute, true)

	if dto.Ready {
		t.Error("Ready = true, want false")
	}
	if dto.TimedOut {
		t.Error("TimedOut = true, want false: the client cancelled, the wait did not expire")
	}
	if elapsed := time.Since(start); elapsed > 30*time.Second {
		t.Errorf("took %s, want the cancellation to be honoured promptly", elapsed)
	}
}

func TestHandleRestartService_PreservesWorkdirOverride(t *testing.T) {
	srv, configured := startableServer(t)
	worktree := t.TempDir()
	svc := srv.Manager().ServiceByName("svc")

	if _, err := srv.handleStartServiceAt(context.Background(),
		callToolRequest("start_service_at", map[string]any{"name": "svc", "workdir": worktree})); err != nil {
		t.Fatalf("handleStartServiceAt() error = %v", err)
	}
	waitForStatus(t, svc, service.StatusStopped, 15*time.Second)

	res, err := srv.handleRestartService(context.Background(),
		callToolRequest("restart_service", map[string]any{"name": "svc", "timeout_seconds": 15}))
	if err != nil {
		t.Fatalf("handleRestartService() error = %v", err)
	}
	if res.IsError {
		t.Fatalf("IsError = true: %s", resultText(res))
	}

	dto := decodeStructured[ActionResultDTO](t, res)
	if dto.Workdir != worktree {
		t.Errorf("Workdir = %q, want the override %q to survive the restart", dto.Workdir, worktree)
	}
	if dto.Workdir == configured {
		t.Errorf("restart fell back to the configured workdir %q", configured)
	}
	waitForStatus(t, svc, service.StatusStopped, 15*time.Second)
}

func TestHandleRestartService_ServiceNotFound(t *testing.T) {
	srv, _ := startableServer(t)

	res, err := srv.handleRestartService(context.Background(),
		callToolRequest("restart_service", map[string]any{"name": "nope"}))
	if err != nil {
		t.Fatalf("handleRestartService() error = %v", err)
	}
	if !res.IsError {
		t.Fatal("IsError = false, want true for an unknown service")
	}
	if text := resultText(res); !strings.Contains(text, "Known services") {
		t.Errorf("error = %q, want it to list the valid service names", text)
	}
}

func TestHandleGetServiceConfig_RedactsEnvByDefault(t *testing.T) {
	workdir := t.TempDir()
	cfg := &config.Config{
		Presets: map[string]config.Preset{"stub": {Run: "cmd /c echo running"}},
		Services: []config.ServiceConfig{{
			Name: "svc", Runner: "stub", Workdir: workdir,
			Env: map[string]string{"DB_PASSWORD": "hunter2", "LOG_LEVEL": "debug"},
		}},
	}
	srv := NewServerFromManager(service.NewManager(cfg))

	res, err := srv.handleGetServiceConfig(context.Background(),
		callToolRequest("get_service_config", map[string]any{"name": "svc"}))
	if err != nil {
		t.Fatalf("handleGetServiceConfig() error = %v", err)
	}

	dto := decodeStructured[ServiceConfigDTO](t, res)
	if !dto.EnvRedacted {
		t.Error("EnvRedacted = false, want true by default")
	}
	if len(dto.Env) != 0 {
		t.Errorf("Env = %v, want no values without include_env", dto.Env)
	}
	if len(dto.EnvKeys) != 2 {
		t.Errorf("EnvKeys = %v, want both declared variables listed", dto.EnvKeys)
	}
	if dto.RunCommand != "cmd /c echo running" {
		t.Errorf("RunCommand = %q, want the expanded preset command", dto.RunCommand)
	}
	if dto.BuildCommandError == "" {
		t.Error("BuildCommandError is empty, want an explanation for the missing build command")
	}
	if strings.Contains(resultText(res), "hunter2") {
		t.Error("the text rendering leaked a secret value")
	}
}

func TestHandleGetServiceConfig_MasksSecretsEvenWhenAsked(t *testing.T) {
	workdir := t.TempDir()
	cfg := &config.Config{
		Presets: map[string]config.Preset{"stub": {Run: "cmd /c echo running"}},
		Services: []config.ServiceConfig{{
			Name: "svc", Runner: "stub", Workdir: workdir,
			Env: map[string]string{"DB_PASSWORD": "hunter2", "LOG_LEVEL": "debug"},
		}},
	}
	srv := NewServerFromManager(service.NewManager(cfg))

	res, err := srv.handleGetServiceConfig(context.Background(),
		callToolRequest("get_service_config", map[string]any{"name": "svc", "include_env": true}))
	if err != nil {
		t.Fatalf("handleGetServiceConfig() error = %v", err)
	}

	dto := decodeStructured[ServiceConfigDTO](t, res)
	if dto.Env["LOG_LEVEL"] != "debug" {
		t.Errorf("Env[LOG_LEVEL] = %q, want the plain value", dto.Env["LOG_LEVEL"])
	}
	if dto.Env["DB_PASSWORD"] != redactedValue {
		t.Errorf("Env[DB_PASSWORD] = %q, want it masked", dto.Env["DB_PASSWORD"])
	}
}

func TestResources_ServicesAndTemplates(t *testing.T) {
	srv, _ := startableServer(t)
	ctx := context.Background()

	contents, err := srv.readServicesResource(ctx, readResourceRequest(uriServices))
	if err != nil {
		t.Fatalf("readServicesResource() error = %v", err)
	}
	if !strings.Contains(resourceText(t, contents), `"name": "svc"`) {
		t.Errorf("services resource does not describe the service:\n%s", resourceText(t, contents))
	}

	contents, err = srv.readServiceResource(ctx, readResourceRequest(serviceURI("svc")))
	if err != nil {
		t.Fatalf("readServiceResource() error = %v", err)
	}
	if !strings.Contains(resourceText(t, contents), `"status"`) {
		t.Error("service resource does not expose the status")
	}

	if _, err := srv.readServiceLogsResource(ctx, readResourceRequest(serviceURI("svc")+"/logs")); err != nil {
		t.Fatalf("readServiceLogsResource() error = %v", err)
	}
	if _, err := srv.readServiceConfigResource(ctx, readResourceRequest(serviceURI("svc")+"/config")); err != nil {
		t.Fatalf("readServiceConfigResource() error = %v", err)
	}
}

func TestResources_UnknownServiceName(t *testing.T) {
	srv, _ := startableServer(t)

	_, err := srv.readServiceResource(context.Background(), readResourceRequest(serviceURI("nope")))
	if err == nil {
		t.Fatal("error = nil, want a failure for an unknown service")
	}
	if !strings.Contains(err.Error(), "Known services") {
		t.Errorf("error = %q, want it to list the valid service names", err)
	}
}

func TestResources_ConfigAndPresetsNeedConfigPath(t *testing.T) {
	srv, _ := startableServer(t)

	if _, err := srv.readConfigResource(context.Background(), readResourceRequest(uriConfig)); err == nil {
		t.Error("error = nil, want a failure when no config path is set")
	}

	path := filepath.Join(t.TempDir(), "services.toml")
	if err := config.SaveConfig(path, &config.Config{
		Presets: map[string]config.Preset{"java": {Build: "mvn -pl {{.Modules}} install", Run: "java {{.MainClass}}"}},
	}); err != nil {
		t.Fatal(err)
	}
	srv.SetConfigPath(path)

	contents, err := srv.readConfigResource(context.Background(), readResourceRequest(uriConfig))
	if err != nil {
		t.Fatalf("readConfigResource() error = %v", err)
	}
	if !strings.Contains(resourceText(t, contents), "java") {
		t.Error("config resource does not contain the saved preset")
	}

	contents, err = srv.readPresetsResource(context.Background(), readResourceRequest(uriPresets))
	if err != nil {
		t.Fatalf("readPresetsResource() error = %v", err)
	}
	presetsText := resourceText(t, contents)
	for _, want := range []string{`"name": "java"`, "Modules", "MainClass"} {
		if !strings.Contains(presetsText, want) {
			t.Errorf("presets resource missing %q:\n%s", want, presetsText)
		}
	}
}

// TestNotifier_DebouncesBursts asserts that a burst of log lines produces a
// single scheduled notification, which is what keeps a verbose Maven build from
// flooding subscribers.
func TestNotifier_DebouncesBursts(t *testing.T) {
	srv, _ := startableServer(t)
	n := srv.notifier
	n.debounce = time.Hour // keep the timer pending for the whole test

	for i := 0; i < 100; i++ {
		srv.NotifyServiceLog("svc", "line")
	}

	n.mu.Lock()
	pending := len(n.pending)
	n.mu.Unlock()

	if pending != 1 {
		t.Errorf("pending notifications = %d, want 1 for a burst on a single resource", pending)
	}

	srv.NotifyServiceStatus("svc", service.StatusRunning, 123)

	n.mu.Lock()
	pending = len(n.pending)
	_, hasList := n.pending[uriServices]
	n.mu.Unlock()

	if pending != 3 {
		t.Errorf("pending notifications = %d, want 3 (logs, service, service list)", pending)
	}
	if !hasList {
		t.Errorf("a status change must also invalidate %s", uriServices)
	}

	n.stop()
	n.mu.Lock()
	pending = len(n.pending)
	n.mu.Unlock()
	if pending != 0 {
		t.Errorf("pending notifications after stop = %d, want 0", pending)
	}
}

func readResourceRequest(uri string) mcp.ReadResourceRequest {
	return mcp.ReadResourceRequest{Params: mcp.ReadResourceParams{URI: uri}}
}

func resourceText(t *testing.T, contents []mcp.ResourceContents) string {
	t.Helper()
	var sb strings.Builder
	for _, c := range contents {
		trc, ok := c.(mcp.TextResourceContents)
		if !ok {
			t.Fatalf("resource content is %T, want TextResourceContents", c)
		}
		sb.WriteString(trc.Text)
	}
	return sb.String()
}
