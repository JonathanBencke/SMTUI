package mcp

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/JonathanBencke/ServiceManagerTUI/internal/service"
	"github.com/mark3labs/mcp-go/mcp"
)

const (
	defaultLogLines    = 50
	maxSearchResults   = 500
	defaultSearchLimit = 50
	maxContextLines    = 10
	defaultWaitTimeout = 180 * time.Second
	maxWaitTimeout     = 600 * time.Second
	waitPollInterval   = time.Second
	// failureLogTail is how many log lines accompany a failed wait, so the
	// agent can diagnose without a second call.
	failureLogTail = 30
	// statsWindow is the interval over which CPU usage is measured. CPU is a
	// delta between two samples, so a one-shot call needs a real window.
	statsWindow = 700 * time.Millisecond
)

// levelPatterns maps the 'level' filter of search_logs to the substrings that
// usually mark that severity in Java/Node/Go logs.
var levelPatterns = map[string]*regexp.Regexp{
	"error": regexp.MustCompile(`(?i)(\berror\b|\bsevere\b|\bfatal\b|exception|caused by)`),
	"warn":  regexp.MustCompile(`(?i)(\bwarn(ing)?\b)`),
}

func (m *MCPServer) registerObservabilityTools() {
	m.srv.AddTool(
		mcp.NewTool("get_logs",
			mcp.WithDescription("Get recent logs for a specific service. Pass 'since_index' with the 'last_line_index' of a previous call to read only what is new - the cheapest way to follow a long build. The buffer is bounded (500 lines per service), so older output is dropped."),
			mcp.WithTitleAnnotation("Get service logs"),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithOpenWorldHintAnnotation(false),
			mcp.WithString("name", mcp.Required(), mcp.Description("Service name")),
			mcp.WithNumber("lines", mcp.Description("Number of recent log lines to return (default 50). Ignored when since_index is given")),
			mcp.WithNumber("since_index", mcp.Description("Return only lines with a global index >= this value. Use the 'last_line_index' returned by the previous call")),
			mcp.WithOutputSchema[LogsDTO](),
		),
		m.handleGetLogs,
	)

	m.srv.AddTool(
		mcp.NewTool("search_logs",
			mcp.WithDescription("Search a service's buffered logs with a regular expression (RE2 syntax), optionally filtered by severity and with surrounding context lines. Use this instead of pulling the whole log when diagnosing a failure. Only the retained buffer is searched, so 'no match' does not prove the message never appeared."),
			mcp.WithTitleAnnotation("Search service logs"),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithOpenWorldHintAnnotation(false),
			mcp.WithString("name", mcp.Required(), mcp.Description("Service name")),
			mcp.WithString("pattern", mcp.Required(), mcp.Description("Regular expression (RE2), e.g. 'Caused by|BindException'")),
			mcp.WithBoolean("ignore_case", mcp.Description("Case-insensitive match (default true)")),
			mcp.WithNumber("max_results", mcp.Description("Maximum number of matches to return (default 50, max 500)")),
			mcp.WithNumber("context_lines", mcp.Description("Lines of context to include before and after each match (default 0, max 10)")),
			mcp.WithNumber("tail_scan", mcp.Description("Search only the last N buffered lines (default: the whole buffer)")),
			mcp.WithString("level", mcp.Description("Extra severity filter: 'error', 'warn' or 'any' (default 'any')")),
			mcp.WithOutputSchema[SearchLogsDTO](),
		),
		m.handleSearchLogs,
	)

	m.srv.AddTool(
		mcp.NewTool("get_stats",
			mcp.WithDescription("Get CPU and memory usage of a service and its whole process tree, or of every service when 'name' is omitted. CPU is measured over a short sampling window, so the call takes about a second."),
			mcp.WithTitleAnnotation("Get resource usage"),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithOpenWorldHintAnnotation(false),
			mcp.WithString("name", mcp.Description("Service name. Omit to get every service")),
			mcp.WithOutputSchema[StatsListDTO](),
		),
		m.handleGetStats,
	)

	m.srv.AddTool(
		mcp.NewTool("wait_until_ready",
			mcp.WithDescription("Block until a service finished booting: status 'running' and, when a health_port is configured, that port accepting connections. Call it right after start_service instead of polling list_services. Fails fast (returning the log tail) if the service crashes or stops while waiting."),
			mcp.WithTitleAnnotation("Wait for a service to become ready"),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithOpenWorldHintAnnotation(false),
			mcp.WithString("name", mcp.Required(), mcp.Description("Service name")),
			mcp.WithNumber("timeout_seconds", mcp.Description("How long to wait before giving up (default 180, max 600)")),
			mcp.WithBoolean("require_health_port", mcp.Description("Also require the configured health_port to accept connections (default true). Ignored when the service declares no health_port")),
			mcp.WithOutputSchema[WaitResultDTO](),
		),
		m.handleWaitUntilReady,
	)

	m.srv.AddTool(
		mcp.NewTool("get_service_config",
			mcp.WithDescription("Get the effective, read-only configuration of a service: the preset it uses, the fully expanded build and run command lines, working directories, profiles and the names of the environment variables it injects. Use it to understand why a service behaves the way it does. This tool never modifies services.toml."),
			mcp.WithTitleAnnotation("Get service configuration"),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithOpenWorldHintAnnotation(false),
			mcp.WithString("name", mcp.Required(), mcp.Description("Service name")),
			mcp.WithBoolean("include_env", mcp.Description("Include environment variable values, not just their names (default false). Secret-looking keys stay masked either way")),
			mcp.WithOutputSchema[ServiceConfigDTO](),
		),
		m.handleGetServiceConfig,
	)
}

func (m *MCPServer) handleGetLogs(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name := req.GetString("name", "")
	lineCount := req.GetInt("lines", defaultLogLines)
	// -1 marks "not provided": 0 is a legitimate cursor meaning "from the
	// beginning of the buffer".
	sinceIndex := req.GetInt("since_index", -1)
	m.emitLog(fmt.Sprintf("[MCP] get_logs('%s', lines=%d, since_index=%d) called", name, lineCount, sinceIndex))

	s, errResult := m.lookupService(name)
	if errResult != nil {
		return errResult, nil
	}

	if lineCount <= 0 {
		lineCount = defaultLogLines
	}

	dto := buildLogsDTO(s, lineCount, sinceIndex)

	text := strings.Join(dto.Lines, "\n")
	if dto.ReturnedLines == 0 {
		if sinceIndex >= 0 {
			text = fmt.Sprintf("No new log lines for '%s' since index %d", name, sinceIndex)
		} else {
			text = fmt.Sprintf("No logs available for '%s'", name)
		}
	}

	m.emitLog(fmt.Sprintf("[MCP] get_logs('%s') → %d lines", name, dto.ReturnedLines))
	return mcp.NewToolResultStructured(dto, text), nil
}

// buildLogsDTO renders either the tail of the buffer or everything after a
// cursor, keeping the global indexes consistent in both cases.
func buildLogsDTO(s *service.Service, lineCount, sinceIndex int) LogsDTO {
	all := s.Logs()
	cursor := s.LogCursor()
	oldest := cursor - len(all)

	dto := LogsDTO{
		Service:       s.Name(),
		BufferedLines: len(all),
		LastLineIndex: cursor,
	}

	var lines []string
	var first int

	if sinceIndex >= 0 {
		start := sinceIndex
		if start < oldest {
			start = oldest
			// Lines the caller asked for were already evicted from the buffer.
			dto.Truncated = true
		}
		if start < cursor {
			lines = all[start-oldest:]
		}
		first = start
	} else {
		lines = all
		if len(all) > lineCount {
			lines = all[len(all)-lineCount:]
			dto.Truncated = true
		}
		first = cursor - len(lines)
	}

	dto.Lines = lines
	dto.ReturnedLines = len(lines)
	dto.FirstLineIndex = first
	return dto
}

func (m *MCPServer) handleSearchLogs(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name := req.GetString("name", "")
	pattern := req.GetString("pattern", "")
	ignoreCase := req.GetBool("ignore_case", true)
	maxResults := req.GetInt("max_results", defaultSearchLimit)
	contextLines := req.GetInt("context_lines", 0)
	tailScan := req.GetInt("tail_scan", 0)
	level := strings.ToLower(strings.TrimSpace(req.GetString("level", "")))
	m.emitLog(fmt.Sprintf("[MCP] search_logs('%s', %q) called", name, pattern))

	s, errResult := m.lookupService(name)
	if errResult != nil {
		return errResult, nil
	}

	if strings.TrimSpace(pattern) == "" {
		return mcp.NewToolResultError("missing 'pattern' parameter: provide an RE2 regular expression, e.g. 'Caused by'"), nil
	}

	expr := pattern
	if ignoreCase {
		expr = "(?i)" + expr
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid 'pattern' regular expression: %v", err)), nil
	}

	var levelRe *regexp.Regexp
	if level != "" && level != "any" {
		lr, ok := levelPatterns[level]
		if !ok {
			return mcp.NewToolResultError(fmt.Sprintf("invalid 'level' %q: use 'error', 'warn' or 'any'", level)), nil
		}
		levelRe = lr
	}

	if maxResults <= 0 {
		maxResults = defaultSearchLimit
	}
	if maxResults > maxSearchResults {
		maxResults = maxSearchResults
	}
	if contextLines < 0 {
		contextLines = 0
	}
	if contextLines > maxContextLines {
		contextLines = maxContextLines
	}

	dto := searchLogs(s, re, levelRe, pattern, maxResults, contextLines, tailScan)

	m.emitLog(fmt.Sprintf("[MCP] search_logs('%s') → %d matches", name, dto.MatchCount))
	return mcp.NewToolResultStructured(dto, renderSearchLogs(dto)), nil
}

// searchLogs scans the retained buffer, collecting up to maxResults matches
// with the requested amount of context around each one.
func searchLogs(s *service.Service, re, levelRe *regexp.Regexp, pattern string, maxResults, contextLines, tailScan int) SearchLogsDTO {
	all := s.Logs()
	cursor := s.LogCursor()
	oldest := cursor - len(all)

	scanned := all
	offset := 0
	if tailScan > 0 && tailScan < len(all) {
		offset = len(all) - tailScan
		scanned = all[offset:]
	}

	dto := SearchLogsDTO{
		Service:       s.Name(),
		Pattern:       pattern,
		Matches:       []LogMatchDTO{},
		BufferedLines: len(all),
		ScannedLines:  len(scanned),
	}

	for i, line := range scanned {
		if !re.MatchString(line) {
			continue
		}
		if levelRe != nil && !levelRe.MatchString(line) {
			continue
		}
		if len(dto.Matches) >= maxResults {
			dto.Truncated = true
			break
		}

		abs := offset + i
		match := LogMatchDTO{LineIndex: oldest + abs, Text: line}
		if contextLines > 0 {
			match.Before = all[maxInt(0, abs-contextLines):abs]
			match.After = all[minInt(len(all), abs+1):minInt(len(all), abs+1+contextLines)]
		}
		dto.Matches = append(dto.Matches, match)
	}

	dto.MatchCount = len(dto.Matches)
	return dto
}

func (m *MCPServer) handleGetStats(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name := req.GetString("name", "")
	m.emitLog(fmt.Sprintf("[MCP] get_stats('%s') called", name))

	targets := m.manager.Services()
	if name != "" {
		s, errResult := m.lookupService(name)
		if errResult != nil {
			return errResult, nil
		}
		targets = []*service.Service{s}
	}

	dto := sampleAll(ctx, targets)

	m.emitLog(fmt.Sprintf("[MCP] get_stats → %d services, %s total", len(dto.Services), dto.TotalMemHuman))
	return mcp.NewToolResultStructured(dto, renderStats(dto)), nil
}

// sampleAll measures every service in parallel, so the sampling window is paid
// once instead of once per service.
func sampleAll(ctx context.Context, targets []*service.Service) StatsListDTO {
	results := make([]StatsDTO, len(targets))
	var wg sync.WaitGroup

	for i, s := range targets {
		results[i] = StatsDTO{Service: s.Name(), Status: string(s.Status()), PID: s.PID()}
		if s.PID() <= 0 {
			continue
		}
		wg.Add(1)
		go func(i int, s *service.Service) {
			defer wg.Done()
			stats, elapsed := s.SampleStats(ctx, statsWindow)
			results[i].MemBytes = stats.MemBytes
			results[i].MemHuman = service.FormatMem(stats.MemBytes)
			results[i].CPUPercent = stats.CPUPercent
			results[i].SampledMs = elapsed.Milliseconds()
		}(i, s)
	}
	wg.Wait()

	dto := StatsListDTO{Services: results}
	for _, r := range results {
		dto.TotalMemBytes += r.MemBytes
	}
	dto.TotalMemHuman = service.FormatMem(dto.TotalMemBytes)
	return dto
}

func (m *MCPServer) handleWaitUntilReady(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name := req.GetString("name", "")
	timeout := clampDuration(req.GetInt("timeout_seconds", 0), defaultWaitTimeout, maxWaitTimeout)
	requirePort := req.GetBool("require_health_port", true)
	m.emitLog(fmt.Sprintf("[MCP] wait_until_ready('%s', timeout=%s) called", name, timeout))

	s, errResult := m.lookupService(name)
	if errResult != nil {
		return errResult, nil
	}

	dto := waitUntilReady(ctx, s, timeout, requirePort)

	m.emitLog(fmt.Sprintf("[MCP] wait_until_ready('%s') → ready=%v status=%s", name, dto.Ready, dto.Status))
	return mcp.NewToolResultStructured(dto, dto.Message), nil
}

// waitUntilReady polls the service until it is running (and, when required,
// answering on its health port), it dies, or the deadline passes.
func waitUntilReady(ctx context.Context, s *service.Service, timeout time.Duration, requirePort bool) WaitResultDTO {
	start := time.Now()
	port := s.HealthPort()
	checkPort := requirePort && port > 0

	dto := WaitResultDTO{Service: s.Name(), HealthPort: port}

	for {
		status := s.Status()
		dto.Status = string(status)

		switch {
		case status == service.StatusRunning:
			if !checkPort {
				dto.Ready = true
				dto.WaitedMs = time.Since(start).Milliseconds()
				dto.Message = fmt.Sprintf("Service '%s' is running (no health_port configured, readiness is based on process status only)", s.Name())
				return dto
			}
			if service.CheckPort(port) {
				dto.Ready = true
				dto.PortOpen = true
				dto.WaitedMs = time.Since(start).Milliseconds()
				dto.Message = fmt.Sprintf("Service '%s' is ready: running and accepting connections on port %d", s.Name(), port)
				return dto
			}

		case status == service.StatusCrashed, status == service.StatusStopped, status == service.StatusIdle:
			dto.WaitedMs = time.Since(start).Milliseconds()
			dto.RecentLogs = tailLines(s.Logs(), failureLogTail)
			dto.Message = fmt.Sprintf(
				"Service '%s' is %s and will never become ready. Start it with start_service, or inspect the log tail below / search_logs for the failure.",
				s.Name(), status)
			return dto
		}

		if time.Since(start) >= timeout {
			dto.TimedOut = true
			dto.WaitedMs = time.Since(start).Milliseconds()
			dto.RecentLogs = tailLines(s.Logs(), failureLogTail)
			dto.Message = fmt.Sprintf(
				"Timed out after %s waiting for '%s' (status: %s). A long Maven build may simply need more time - retry with a larger timeout_seconds, or follow get_logs with since_index.",
				timeout, s.Name(), dto.Status)
			return dto
		}

		select {
		case <-ctx.Done():
			dto.WaitedMs = time.Since(start).Milliseconds()
			dto.Message = fmt.Sprintf("Wait for '%s' cancelled by the client after %s (status: %s)", s.Name(), time.Since(start).Round(time.Millisecond), dto.Status)
			return dto
		case <-time.After(waitPollInterval):
		}
	}
}

func (m *MCPServer) handleGetServiceConfig(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name := req.GetString("name", "")
	includeEnv := req.GetBool("include_env", false)
	m.emitLog(fmt.Sprintf("[MCP] get_service_config('%s', include_env=%v) called", name, includeEnv))

	s, errResult := m.lookupService(name)
	if errResult != nil {
		return errResult, nil
	}

	dto := newServiceConfigDTO(s, includeEnv)
	return mcp.NewToolResultStructured(dto, renderServiceConfig(dto)), nil
}

func tailLines(lines []string, n int) []string {
	if len(lines) <= n {
		return lines
	}
	return lines[len(lines)-n:]
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
