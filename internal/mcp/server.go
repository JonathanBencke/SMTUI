package mcp

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/JonathanBencke/ServiceManagerTUI/internal/config"
	"github.com/JonathanBencke/ServiceManagerTUI/internal/logbuffer"
	"github.com/JonathanBencke/ServiceManagerTUI/internal/service"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Endpoint paths served on the MCP port. SSE (/sse + /message) is kept for
// backward compatibility with existing clients (e.g. Claude Code); Streamable
// HTTP (/mcp) is the transport expected by kiro-cli.
const (
	ssePath        = "/sse"
	messagePath    = "/message"
	streamablePath = "/mcp"
)

const DefaultPort = 9423

// serverInstructions is advertised on initialize so an agent knows how to drive
// the tool surface without trial and error.
const serverInstructions = `Controls the local development services managed by ServiceManagerTUI.

Every tool answers with a human-readable text rendering AND a JSON payload in
structuredContent - prefer the JSON.

Typical flow: list_services to discover names -> start_service (or
start_service_at for a git worktree) -> wait_until_ready to block until the
service answers -> search_logs when something fails -> get_stats to inspect
resource usage. Live state is also published as subscribable resources under
smtui://.`

type MCPServer struct {
	manager    *service.Manager
	srv        *server.MCPServer
	sseSrv     *server.SSEServer
	httpSrv    *server.StreamableHTTPServer
	httpServer *http.Server
	logs       *logbuffer.RingBuffer
	onLog      func(line string)
	notifier   *notifier

	mu      sync.Mutex
	running bool
	port    int
	cfgPath string
}

func NewServer(cfgPath string) (*MCPServer, error) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, err
	}

	mcps := newMCPServer(service.NewManager(cfg))
	mcps.cfgPath = cfgPath
	return mcps, nil
}

func NewServerFromManager(manager *service.Manager) *MCPServer {
	return newMCPServer(manager)
}

func newMCPServer(manager *service.Manager) *MCPServer {
	srv := server.NewMCPServer(
		"service-manager-tui",
		"1.0.0",
		server.WithToolCapabilities(true),
		// subscribe + listChanged: clients can follow a service's logs and
		// status without polling list_services in a loop.
		server.WithResourceCapabilities(true, true),
		server.WithInstructions(serverInstructions),
	)

	mcps := &MCPServer{
		manager: manager,
		srv:     srv,
		logs:    logbuffer.New(500),
		port:    DefaultPort,
	}
	mcps.notifier = newNotifier(mcps)

	mcps.registerTools()
	mcps.registerResources()
	return mcps
}

// SetConfigPath tells the server where the configuration file lives, enabling
// the smtui://config and smtui://presets resources. NewServerFromManager (used
// by the TUI) does not know the path on its own.
func (m *MCPServer) SetConfigPath(path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfgPath = path
}

func (m *MCPServer) configPath() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cfgPath
}

func (m *MCPServer) SetLogCallback(onLog func(line string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onLog = onLog
}

func (m *MCPServer) emitLog(line string) {
	line = fmt.Sprintf("[%s] %s", time.Now().Format("2006-01-02 15:04:05"), line)
	m.logs.Write(line)
	m.mu.Lock()
	cb := m.onLog
	m.mu.Unlock()
	if cb != nil {
		cb(line)
	}
}

func (m *MCPServer) Logs() []string {
	return m.logs.Lines()
}

func (m *MCPServer) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}

func (m *MCPServer) Port() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.port
}

func (m *MCPServer) Start() error {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return fmt.Errorf("MCP server already running")
	}
	m.mu.Unlock()

	addr := fmt.Sprintf("127.0.0.1:%d", m.port)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("port %d: %w", m.port, err)
	}
	ln.Close()

	baseURL := fmt.Sprintf("http://%s", addr)
	sseSrv := server.NewSSEServer(m.srv, server.WithBaseURL(baseURL))
	httpSrv := server.NewStreamableHTTPServer(m.srv, server.WithEndpointPath(streamablePath))

	// Um unico http.Server serve ambos os transportes: SSE (/sse + /message)
	// para compatibilidade com clientes existentes (Claude Code) e Streamable
	// HTTP (/mcp) para o kiro-cli.
	mux := http.NewServeMux()
	mux.Handle(ssePath, sseSrv)
	mux.Handle(messagePath, sseSrv)
	mux.Handle(streamablePath, httpSrv)
	httpServer := &http.Server{Addr: addr, Handler: mux}

	m.mu.Lock()
	m.sseSrv = sseSrv
	m.httpSrv = httpSrv
	m.httpServer = httpServer
	m.running = true
	m.mu.Unlock()

	m.notifier.start()

	m.emitLog(fmt.Sprintf("MCP server started on %s (SSE: %s, Streamable HTTP: %s)", baseURL, ssePath, streamablePath))

	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			m.emitLog(fmt.Sprintf("MCP server error: %v", err))
		}
	}()

	return nil
}

func (m *MCPServer) Stop() error {
	m.mu.Lock()
	httpServer := m.httpServer
	sseSrv := m.sseSrv
	m.running = false
	m.httpServer = nil
	m.sseSrv = nil
	m.httpSrv = nil
	m.mu.Unlock()

	m.notifier.stop()

	if httpServer == nil {
		return fmt.Errorf("MCP server not running")
	}

	m.emitLog("MCP server stopping...")

	// Fecha as sessoes SSE de vida longa para o Shutdown nao bloquear.
	if sseSrv != nil {
		sseSrv.CloseSessions()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := httpServer.Shutdown(ctx)
	// Garante que a porta seja liberada mesmo se restarem conexoes streaming.
	httpServer.Close()
	return err
}

func (m *MCPServer) Serve() error {
	return server.ServeStdio(m.srv)
}

func (m *MCPServer) Manager() *service.Manager {
	return m.manager
}

// NotifyServiceLog reports that a service emitted a log line, so the matching
// resources are invalidated for subscribers. It is called from the process
// output goroutines and must stay cheap - the notifier debounces internally.
func (m *MCPServer) NotifyServiceLog(name, line string) {
	m.notifier.serviceLog(name)
}

// NotifyServiceStatus reports a service status transition to subscribers.
func (m *MCPServer) NotifyServiceStatus(name string, status service.Status, pid int) {
	m.notifier.serviceStatus(name)
}

// lookupService resolves the service targeted by a tool call, returning the
// error result to send back when the name is missing or unknown. The error
// lists the valid names so the agent can recover without another round-trip.
func (m *MCPServer) lookupService(name string) (*service.Service, *mcp.CallToolResult) {
	if name == "" {
		return nil, mcp.NewToolResultError("missing 'name' parameter. " + m.knownServicesHint())
	}
	s := m.manager.ServiceByName(name)
	if s == nil {
		return nil, mcp.NewToolResultError(fmt.Sprintf("service '%s' not found. %s", name, m.knownServicesHint()))
	}
	return s, nil
}

// knownServicesHint lists the configured service names for use in error
// messages.
func (m *MCPServer) knownServicesHint() string {
	services := m.manager.Services()
	if len(services) == 0 {
		return "No services are configured yet; check services.toml."
	}
	names := make([]string, 0, len(services))
	for _, s := range services {
		names = append(names, s.Name())
	}
	return fmt.Sprintf("Known services: %v", names)
}
