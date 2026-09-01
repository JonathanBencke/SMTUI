package mcp

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/JonathanBencke/ServiceManagerTUI/internal/config"
	"github.com/JonathanBencke/ServiceManagerTUI/internal/service"
	"github.com/mark3labs/mcp-go/mcp"
)

// freePort returns an available TCP port on the loopback interface.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// newTestServer starts an MCP server on a free port for the duration of a test,
// returning the base URL and a stop function.
func newTestServer(t *testing.T) (string, func()) {
	t.Helper()
	mgr := service.NewManager(&config.Config{})
	srv := NewServerFromManager(mgr)
	srv.port = freePort(t)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	// Give the listener a moment to accept connections.
	time.Sleep(50 * time.Millisecond)
	return fmt.Sprintf("http://127.0.0.1:%d", srv.port), func() { srv.Stop() }
}

// callToolRequest builds a tool call request with the given arguments.
func callToolRequest(name string, args map[string]any) mcp.CallToolRequest {
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: name, Arguments: args},
	}
}

// resultText flattens the textual content of a tool result.
func resultText(res *mcp.CallToolResult) string {
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

// sdlManagerServer returns an MCP server whose single service "svc" lives in an
// SDL project, using genCmd as the generate-sources command.
func sdlManagerServer(t *testing.T, genCmd string) *MCPServer {
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
		Presets:  map[string]config.Preset{"stub": {SdlGenerateCommand: genCmd}},
		Services: []config.ServiceConfig{{Name: "svc", Runner: "stub", Workdir: workdir}},
	}
	return NewServerFromManager(service.NewManager(cfg))
}

// startableServer returns an MCP server with a single run-only service "svc"
// whose configured workdir is returned alongside it.
func startableServer(t *testing.T) (*MCPServer, string) {
	t.Helper()
	workdir := t.TempDir()
	cfg := &config.Config{
		Presets:  map[string]config.Preset{"stub": {Run: "cmd /c echo running"}},
		Services: []config.ServiceConfig{{Name: "svc", Runner: "stub", Workdir: workdir}},
	}
	return NewServerFromManager(service.NewManager(cfg)), workdir
}

func TestHandleStartServiceAt_StartsFromCustomWorkdir(t *testing.T) {
	srv, configured := startableServer(t)
	worktree := t.TempDir()
	svc := srv.Manager().ServiceByName("svc")

	res, err := srv.handleStartServiceAt(context.Background(),
		callToolRequest("start_service_at", map[string]any{"name": "svc", "workdir": worktree}))

	if err != nil {
		t.Fatalf("handleStartServiceAt() error = %v", err)
	}
	if res.IsError {
		t.Fatalf("IsError = true, want false: %s", resultText(res))
	}
	if text := resultText(res); !strings.Contains(text, worktree) {
		t.Errorf("result = %q, want it to mention the custom workdir %q", text, worktree)
	}
	if got := svc.Workdir(); got != worktree {
		t.Errorf("Workdir() = %q, want %q", got, worktree)
	}
	if got := svc.ConfiguredWorkdir(); got != configured {
		t.Errorf("ConfiguredWorkdir() = %q, want %q", got, configured)
	}
	waitForStatus(t, svc, service.StatusStopped, 10*time.Second)
}

func TestHandleStartServiceAt_MissingWorkdir(t *testing.T) {
	srv, _ := startableServer(t)

	res, err := srv.handleStartServiceAt(context.Background(),
		callToolRequest("start_service_at", map[string]any{"name": "svc"}))

	if err != nil {
		t.Fatalf("handleStartServiceAt() error = %v", err)
	}
	if !res.IsError {
		t.Errorf("IsError = false, want true for a missing 'workdir'")
	}
	if got := srv.Manager().ServiceByName("svc").Status(); got != service.StatusIdle {
		t.Errorf("Status() = %q, want %q (service must not start)", got, service.StatusIdle)
	}
}

func TestHandleStartServiceAt_ServiceNotFound(t *testing.T) {
	srv, _ := startableServer(t)

	res, err := srv.handleStartServiceAt(context.Background(),
		callToolRequest("start_service_at", map[string]any{"name": "nope", "workdir": t.TempDir()}))

	if err != nil {
		t.Fatalf("handleStartServiceAt() error = %v", err)
	}
	if !res.IsError {
		t.Errorf("IsError = false, want true for an unknown service")
	}
}

func TestHandleStartServiceAt_NonExistentWorkdir(t *testing.T) {
	srv, _ := startableServer(t)
	missing := filepath.Join(t.TempDir(), "does-not-exist")

	res, err := srv.handleStartServiceAt(context.Background(),
		callToolRequest("start_service_at", map[string]any{"name": "svc", "workdir": missing}))

	if err != nil {
		t.Fatalf("handleStartServiceAt() error = %v", err)
	}
	if !res.IsError {
		t.Errorf("IsError = false, want true for a non-existent workdir")
	}
	if got := srv.Manager().ServiceByName("svc").Status(); got != service.StatusIdle {
		t.Errorf("Status() = %q, want %q (service must not start)", got, service.StatusIdle)
	}
}

func TestHandleListServices_ReportsWorkdirOverride(t *testing.T) {
	srv, configured := startableServer(t)
	worktree := t.TempDir()
	svc := srv.Manager().ServiceByName("svc")

	if _, err := srv.handleStartServiceAt(context.Background(),
		callToolRequest("start_service_at", map[string]any{"name": "svc", "workdir": worktree})); err != nil {
		t.Fatalf("handleStartServiceAt() error = %v", err)
	}
	waitForStatus(t, svc, service.StatusStopped, 10*time.Second)

	res, err := srv.handleListServices(context.Background(), callToolRequest("list_services", nil))
	if err != nil {
		t.Fatalf("handleListServices() error = %v", err)
	}

	text := resultText(res)
	if !strings.Contains(text, worktree) {
		t.Errorf("list_services should report the custom workdir %q:\n%s", worktree, text)
	}
	if !strings.Contains(text, configured) {
		t.Errorf("list_services should report the configured workdir %q so it can be restored:\n%s", configured, text)
	}
}

// TestRegisteredTools asserts the tool surface advertised to MCP clients,
// including the worktree-aware start_service_at and its required arguments.
func TestRegisteredTools(t *testing.T) {
	srv, _ := startableServer(t)

	tools := srv.srv.ListTools()

	want := []string{
		"list_services", "start_service", "start_service_at", "restart_service",
		"stop_service", "get_logs", "search_logs", "get_stats", "wait_until_ready",
		"get_service_config", "generate_sources", "start_all", "stop_all",
	}
	for _, name := range want {
		if _, ok := tools[name]; !ok {
			t.Errorf("tool %q not registered", name)
		}
	}
	if len(tools) != len(want) {
		t.Errorf("registered %d tools, want %d: %v", len(tools), len(want), tools)
	}

	startAt, ok := tools["start_service_at"]
	if !ok {
		t.Fatal("start_service_at not registered")
	}
	required := startAt.Tool.InputSchema.Required
	for _, arg := range []string{"name", "workdir"} {
		if !slices.Contains(required, arg) {
			t.Errorf("start_service_at required = %v, want it to contain %q", required, arg)
		}
	}
}

// TestToolsDeclareOutputSchema guards the "MCP first" contract: every tool must
// advertise the shape of its structuredContent, otherwise a client has to fall
// back to parsing the text rendering.
func TestToolsDeclareOutputSchema(t *testing.T) {
	srv, _ := startableServer(t)

	for name, entry := range srv.srv.ListTools() {
		if entry.Tool.OutputSchema.Type == "" && entry.Tool.RawOutputSchema == nil {
			t.Errorf("tool %q declares no output schema", name)
		}
	}
}

// TestToolAnnotations asserts the behavioural hints a client relies on to tell
// a safe query from a force kill.
func TestToolAnnotations(t *testing.T) {
	srv, _ := startableServer(t)
	tools := srv.srv.ListTools()

	readOnly := []string{"list_services", "get_logs", "search_logs", "get_stats", "wait_until_ready", "get_service_config"}
	for _, name := range readOnly {
		entry, ok := tools[name]
		if !ok {
			t.Fatalf("tool %q not registered", name)
		}
		if hint := entry.Tool.Annotations.ReadOnlyHint; hint == nil || !*hint {
			t.Errorf("tool %q ReadOnlyHint = %v, want true", name, hint)
		}
		// mcp-go defaults DestructiveHint to true, so a read-only tool must
		// say otherwise explicitly or clients will treat a query as dangerous.
		if hint := entry.Tool.Annotations.DestructiveHint; hint == nil || *hint {
			t.Errorf("tool %q DestructiveHint = %v, want false", name, hint)
		}
	}

	destructive := []string{"stop_service", "stop_all", "restart_service"}
	for _, name := range destructive {
		entry, ok := tools[name]
		if !ok {
			t.Fatalf("tool %q not registered", name)
		}
		if hint := entry.Tool.Annotations.DestructiveHint; hint == nil || !*hint {
			t.Errorf("tool %q DestructiveHint = %v, want true", name, hint)
		}
	}
}

func TestHandleGenerateSources_MissingName(t *testing.T) {
	srv := sdlManagerServer(t, "cmd /c echo generating-from-mcp")

	res, err := srv.handleGenerateSources(context.Background(), callToolRequest("generate_sources", map[string]any{}))

	if err != nil {
		t.Fatalf("handleGenerateSources() error = %v", err)
	}
	if !res.IsError {
		t.Errorf("IsError = false, want true for a missing 'name'")
	}
}

func TestHandleGenerateSources_ServiceNotFound(t *testing.T) {
	srv := sdlManagerServer(t, "cmd /c echo generating-from-mcp")

	res, err := srv.handleGenerateSources(context.Background(), callToolRequest("generate_sources", map[string]any{"name": "nope"}))

	if err != nil {
		t.Fatalf("handleGenerateSources() error = %v", err)
	}
	if !res.IsError {
		t.Errorf("IsError = false, want true for an unknown service")
	}
}

func TestHandleGenerateSources_TriggersGenerationAsynchronously(t *testing.T) {
	srv := sdlManagerServer(t, "cmd /c echo generating-from-mcp")
	svc := srv.Manager().ServiceByName("svc")

	res, err := srv.handleGenerateSources(context.Background(), callToolRequest("generate_sources", map[string]any{"name": "svc"}))

	if err != nil {
		t.Fatalf("handleGenerateSources() error = %v", err)
	}
	if res.IsError {
		t.Fatalf("IsError = true, want false: %s", resultText(res))
	}
	if text := resultText(res); !strings.Contains(text, "Generating sources for 'svc'") {
		t.Errorf("result = %q, want the async acknowledgement", text)
	}

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(strings.Join(svc.Logs(), "\n"), "generating-from-mcp") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("generate-sources output never appeared, logs:\n%s", strings.Join(svc.Logs(), "\n"))
}

func TestHandleGenerateSources_AlreadyGenerating(t *testing.T) {
	srv := sdlManagerServer(t, "cmd /c ping -n 4 127.0.0.1")
	svc := srv.Manager().ServiceByName("svc")

	first, err := srv.handleGenerateSources(context.Background(), callToolRequest("generate_sources", map[string]any{"name": "svc"}))
	if err != nil {
		t.Fatalf("handleGenerateSources() error = %v", err)
	}
	if first.IsError {
		t.Fatalf("first call IsError = true: %s", resultText(first))
	}

	waitForStatus(t, svc, service.StatusGenerating, 10*time.Second)

	second, err := srv.handleGenerateSources(context.Background(), callToolRequest("generate_sources", map[string]any{"name": "svc"}))
	if err != nil {
		t.Fatalf("handleGenerateSources() error = %v", err)
	}
	if text := resultText(second); !strings.Contains(text, "already generating") {
		t.Errorf("result = %q, want an 'already generating' message", text)
	}

	// Espera o fim da geração para o TempDir poder ser removido no cleanup.
	waitForStatus(t, svc, service.StatusIdle, 20*time.Second)
}

func waitForStatus(t *testing.T, s *service.Service, want service.Status, timeout time.Duration) {
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

// TestStreamableHTTPInitialize verifies the /mcp (Streamable HTTP) transport
// used by kiro-cli completes an initialize handshake.
func TestStreamableHTTPInitialize(t *testing.T) {
	base, stop := newTestServer(t)
	defer stop()

	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"kiro-test","version":"1"}}}`
	req, err := http.NewRequest(http.MethodPost, base+"/mcp", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /mcp error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	data, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(data), "service-manager-tui") {
		t.Errorf("initialize response missing serverInfo:\n%s", data)
	}
}

// TestSSEStillServed verifies the legacy /sse transport (Claude Code) still
// responds with an event stream — guarding against regression.
func TestSSEStillServed(t *testing.T) {
	base, stop := newTestServer(t)
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/sse", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /sse error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
}
