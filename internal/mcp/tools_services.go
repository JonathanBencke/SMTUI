package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/JonathanBencke/ServiceManagerTUI/internal/service"
	"github.com/mark3labs/mcp-go/mcp"
)

// registerTools wires the whole tool surface. Every tool declares an output
// schema (so clients know the shape of structuredContent) and behaviour
// annotations (so a client can tell a read-only query from a force kill).
func (m *MCPServer) registerTools() {
	m.registerServiceTools()
	m.registerObservabilityTools()
}

func (m *MCPServer) registerServiceTools() {
	m.srv.AddTool(
		mcp.NewTool("list_services",
			mcp.WithDescription("List all services with their current status, PID, uptime, profiles and working directory. Start here to discover the exact service names the other tools expect."),
			mcp.WithTitleAnnotation("List services"),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithOpenWorldHintAnnotation(false),
			mcp.WithOutputSchema[ServiceListDTO](),
		),
		m.handleListServices,
	)

	m.srv.AddTool(
		mcp.NewTool("start_service",
			mcp.WithDescription("Start a specific service by name from its configured working directory. Returns as soon as the process is launched - the service is not ready yet, so follow up with wait_until_ready."),
			mcp.WithTitleAnnotation("Start service"),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
			mcp.WithString("name", mcp.Required(), mcp.Description("Service name (e.g. Database, Benefits, BenefitsData)")),
			mcp.WithOutputSchema[ActionResultDTO](),
		),
		m.handleStartService,
	)

	m.srv.AddTool(
		mcp.NewTool("start_service_at",
			mcp.WithDescription("Start a service from a custom working directory instead of the workdir declared in services.toml - useful for git worktrees, where the same project is checked out in another directory. The override is sticky for later start_service/generate_sources calls on this service, so the same checkout is reused; it is dropped when the user starts the service from the TUI (which always uses the configured workdir) or when the configured workdir (shown by list_services) is passed back. The service must be stopped."),
			mcp.WithTitleAnnotation("Start service from a custom directory"),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
			mcp.WithString("name", mcp.Required(), mcp.Description("Service name (e.g. Database, Benefits, BenefitsData)")),
			mcp.WithString("workdir", mcp.Required(), mcp.Description("Working directory to run the service from, e.g. C:\\worktrees\\feature-x\\java\\impl. Must be an existing directory; relative paths are resolved against the smtui process directory")),
			mcp.WithOutputSchema[ActionResultDTO](),
		),
		m.handleStartServiceAt,
	)

	m.srv.AddTool(
		mcp.NewTool("restart_service",
			mcp.WithDescription("Stop a service (force kill) and start it again, keeping any custom working directory set by start_service_at. Use after changing configuration or rebuilding. Returns once the new process is launched - follow up with wait_until_ready."),
			mcp.WithTitleAnnotation("Restart service"),
			mcp.WithDestructiveHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
			mcp.WithString("name", mcp.Required(), mcp.Description("Service name")),
			mcp.WithNumber("timeout_seconds", mcp.Description("How long to wait for the service to stop before giving up (default 60, max 300)")),
			mcp.WithOutputSchema[ActionResultDTO](),
		),
		m.handleRestartService,
	)

	m.srv.AddTool(
		mcp.NewTool("stop_service",
			mcp.WithDescription("Stop a specific service by name (force kill of the whole process tree). Unsaved in-memory state of the service is lost."),
			mcp.WithTitleAnnotation("Stop service"),
			mcp.WithDestructiveHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithOpenWorldHintAnnotation(false),
			mcp.WithString("name", mcp.Required(), mcp.Description("Service name")),
			mcp.WithOutputSchema[ActionResultDTO](),
		),
		m.handleStopService,
	)

	m.srv.AddTool(
		mcp.NewTool("generate_sources",
			mcp.WithDescription("Run only the SDL/PDL/EDL source generation (generate-sources) for a service, without building or starting it. Stops the service first if it is running (it is not restarted). Runs asynchronously: poll list_services for the 'generating' status and get_logs for the output."),
			mcp.WithTitleAnnotation("Generate sources"),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
			mcp.WithString("name", mcp.Required(), mcp.Description("Service name")),
			mcp.WithOutputSchema[ActionResultDTO](),
		),
		m.handleGenerateSources,
	)

	m.srv.AddTool(
		mcp.NewTool("start_all",
			mcp.WithDescription("Start every configured service at once, each from its own working directory."),
			mcp.WithTitleAnnotation("Start all services"),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
			mcp.WithOutputSchema[BulkActionResultDTO](),
		),
		m.handleStartAll,
	)

	m.srv.AddTool(
		mcp.NewTool("stop_all",
			mcp.WithDescription("Stop every running service (force kill of each process tree)."),
			mcp.WithTitleAnnotation("Stop all services"),
			mcp.WithDestructiveHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithOpenWorldHintAnnotation(false),
			mcp.WithOutputSchema[BulkActionResultDTO](),
		),
		m.handleStopAll,
	)
}

func (m *MCPServer) handleListServices(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	m.emitLog("[MCP] list_services called")

	dto := newServiceListDTO(m.manager.Services())

	m.emitLog(fmt.Sprintf("[MCP] list_services → %d services", dto.Total))
	return mcp.NewToolResultStructured(dto, renderServiceList(dto)), nil
}

// startAndReport starts s - from workdir when non-empty - and renders the
// result shared by start_service and start_service_at.
func (m *MCPServer) startAndReport(tool string, s *service.Service, workdir string) *mcp.CallToolResult {
	name := s.Name()

	if st := s.Status(); st == service.StatusRunning || st == service.StatusBuilding {
		dto := ActionResultDTO{
			OK:      true,
			Action:  tool,
			Service: name,
			Status:  string(st),
			PID:     s.PID(),
			Workdir: s.Workdir(),
			Message: fmt.Sprintf("Service '%s' is already %s (workdir: %s)", name, st, s.Workdir()),
		}
		return mcp.NewToolResultStructured(dto, dto.Message)
	}

	start := s.Start
	if workdir != "" {
		start = func() error { return s.StartAt(workdir) }
	}

	if err := start(); err != nil {
		m.emitLog(fmt.Sprintf("[MCP] %s('%s') FAILED: %v", tool, name, err))
		return mcp.NewToolResultError(fmt.Sprintf("failed to start '%s': %v", name, err))
	}

	m.emitLog(fmt.Sprintf("[MCP] %s('%s') → started in %s (PID %d)", tool, name, s.Workdir(), s.PID()))

	dto := ActionResultDTO{
		OK:       true,
		Action:   tool,
		Service:  name,
		Status:   string(s.Status()),
		PID:      s.PID(),
		Workdir:  s.Workdir(),
		Message:  fmt.Sprintf("Service '%s' started successfully in '%s' (PID: %d)", name, s.Workdir(), s.PID()),
		NextStep: fmt.Sprintf("The process was launched but may still be booting. Call wait_until_ready('%s') before using it.", name),
	}
	return mcp.NewToolResultStructured(dto, dto.Message)
}

func (m *MCPServer) handleStartService(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name := req.GetString("name", "")
	m.emitLog(fmt.Sprintf("[MCP] start_service('%s') called", name))

	s, errResult := m.lookupService(name)
	if errResult != nil {
		return errResult, nil
	}

	return m.startAndReport("start_service", s, ""), nil
}

// handleStartServiceAt starts a service from a caller-provided working
// directory (e.g. a git worktree), overriding the configured workdir.
func (m *MCPServer) handleStartServiceAt(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name := req.GetString("name", "")
	workdir := req.GetString("workdir", "")
	m.emitLog(fmt.Sprintf("[MCP] start_service_at('%s', '%s') called", name, workdir))

	s, errResult := m.lookupService(name)
	if errResult != nil {
		return errResult, nil
	}

	if strings.TrimSpace(workdir) == "" {
		return mcp.NewToolResultError("missing 'workdir' parameter"), nil
	}

	return m.startAndReport("start_service_at", s, workdir), nil
}

// restartStopTimeout bounds how long handleRestartService waits for the old
// process to die before reporting failure.
const (
	defaultRestartTimeout = 60 * time.Second
	maxRestartTimeout     = 300 * time.Second
)

// handleRestartService stops the service, waits for it to actually die and
// starts it again. The custom working directory set by start_service_at is
// preserved, so restarting a service running from a git worktree does not
// silently fall back to the configured checkout.
func (m *MCPServer) handleRestartService(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name := req.GetString("name", "")
	timeout := clampDuration(req.GetInt("timeout_seconds", 0), defaultRestartTimeout, maxRestartTimeout)
	m.emitLog(fmt.Sprintf("[MCP] restart_service('%s') called", name))

	s, errResult := m.lookupService(name)
	if errResult != nil {
		return errResult, nil
	}

	override := s.WorkdirOverride()

	if st := s.Status(); st == service.StatusGenerating {
		return mcp.NewToolResultError(fmt.Sprintf("service '%s' is generating sources; wait for it to finish before restarting", name)), nil
	}

	if isStoppable(s.Status()) {
		if err := s.Stop(); err != nil {
			m.emitLog(fmt.Sprintf("[MCP] restart_service('%s') FAILED to stop: %v", name, err))
			return mcp.NewToolResultError(fmt.Sprintf("failed to stop '%s': %v", name, err)), nil
		}
	}

	if !waitForStopped(ctx, s, timeout) {
		return mcp.NewToolResultError(fmt.Sprintf(
			"service '%s' did not stop within %s (status: %s); it was not restarted",
			name, timeout, s.Status())), nil
	}

	return m.startAndReport("restart_service", s, override), nil
}

// isStoppable reports whether a status owns a process that Stop can kill.
func isStoppable(st service.Status) bool {
	return st == service.StatusRunning || st == service.StatusBuilding || st == service.StatusStopping
}

// waitForStopped polls until the service no longer owns a process, or the
// timeout/context expires.
func waitForStopped(ctx context.Context, s *service.Service, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if !isStoppable(s.Status()) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// clampDuration converts a caller-provided seconds value into a duration,
// falling back to def when unset and never exceeding max.
func clampDuration(seconds int, def, max time.Duration) time.Duration {
	if seconds <= 0 {
		return def
	}
	d := time.Duration(seconds) * time.Second
	if d > max {
		return max
	}
	return d
}

func (m *MCPServer) handleStopService(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name := req.GetString("name", "")
	m.emitLog(fmt.Sprintf("[MCP] stop_service('%s') called", name))

	s, errResult := m.lookupService(name)
	if errResult != nil {
		return errResult, nil
	}

	if s.Status() != service.StatusRunning && s.Status() != service.StatusBuilding {
		dto := ActionResultDTO{
			OK:      true,
			Action:  "stop_service",
			Service: name,
			Status:  string(s.Status()),
			Message: fmt.Sprintf("Service '%s' is not running (status: %s)", name, s.Status()),
		}
		return mcp.NewToolResultStructured(dto, dto.Message), nil
	}

	if err := s.Stop(); err != nil {
		m.emitLog(fmt.Sprintf("[MCP] stop_service('%s') FAILED: %v", name, err))
		return mcp.NewToolResultError(fmt.Sprintf("failed to stop '%s': %v", name, err)), nil
	}

	m.emitLog(fmt.Sprintf("[MCP] stop_service('%s') → stopped", name))
	dto := ActionResultDTO{
		OK:      true,
		Action:  "stop_service",
		Service: name,
		Status:  string(s.Status()),
		Message: fmt.Sprintf("Service '%s' stopped successfully", name),
	}
	return mcp.NewToolResultStructured(dto, dto.Message), nil
}

// handleGenerateSources triggers the standalone generate-sources step for a
// service. The generation can take minutes, so it runs in the background and
// the caller polls list_services/get_logs for progress.
func (m *MCPServer) handleGenerateSources(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name := req.GetString("name", "")
	m.emitLog(fmt.Sprintf("[MCP] generate_sources('%s') called", name))

	s, errResult := m.lookupService(name)
	if errResult != nil {
		return errResult, nil
	}

	if s.Status() == service.StatusGenerating {
		dto := ActionResultDTO{
			OK:      true,
			Action:  "generate_sources",
			Service: name,
			Status:  string(service.StatusGenerating),
			Message: fmt.Sprintf("Service '%s' is already generating sources", name),
		}
		return mcp.NewToolResultStructured(dto, dto.Message), nil
	}

	go s.GenerateSources()

	m.emitLog(fmt.Sprintf("[MCP] generate_sources('%s') → started", name))
	dto := ActionResultDTO{
		OK:      true,
		Action:  "generate_sources",
		Service: name,
		Status:  string(s.Status()),
		Workdir: s.Workdir(),
		Message: fmt.Sprintf(
			"Generating sources for '%s'... The service is stopped first if it was running and is not restarted. Use get_logs('%s') to follow the output and list_services to check when the status leaves 'generating'.",
			name, name),
		NextStep: fmt.Sprintf("Poll get_logs('%s', since_index=...) and list_services until the status leaves 'generating'.", name),
	}
	return mcp.NewToolResultStructured(dto, dto.Message), nil
}

func (m *MCPServer) handleStartAll(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	m.emitLog("[MCP] start_all called")
	m.manager.StartAll()

	dto := BulkActionResultDTO{
		OK:       true,
		Action:   "start_all",
		Services: m.serviceNames(),
		Message:  "Starting all services...",
		NextStep: "Call wait_until_ready for each service, or list_services to watch the statuses.",
	}
	return mcp.NewToolResultStructured(dto, dto.Message), nil
}

func (m *MCPServer) handleStopAll(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	m.emitLog("[MCP] stop_all called")
	m.manager.StopAll()

	dto := BulkActionResultDTO{
		OK:       true,
		Action:   "stop_all",
		Services: m.serviceNames(),
		Message:  "Stopping all services...",
	}
	return mcp.NewToolResultStructured(dto, dto.Message), nil
}

func (m *MCPServer) serviceNames() []string {
	services := m.manager.Services()
	names := make([]string, 0, len(services))
	for _, s := range services {
		names = append(names, s.Name())
	}
	return names
}
