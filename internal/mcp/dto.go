package mcp

// This file defines the structured payloads returned by every MCP tool.
//
// Each tool answers with both a human-readable text rendering (kept for
// backward compatibility with clients that only read content[0].text) and the
// corresponding struct below in structuredContent. The struct is the source of
// truth: format.go derives the text from it, never the other way around.
//
// Field names are part of the tool contract — keep them stable.

// ServiceDTO is the full runtime view of a single managed service.
type ServiceDTO struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	// PID is 0 when no process is attached.
	PID           int      `json:"pid"`
	UptimeSeconds int64    `json:"uptime_seconds"`
	UptimeHuman   string   `json:"uptime_human"`
	Profiles      []string `json:"profiles"`
	// Workdir is the directory commands actually run in: the override when one
	// is set, otherwise ConfiguredWorkdir.
	Workdir           string `json:"workdir"`
	ConfiguredWorkdir string `json:"configured_workdir"`
	// WorkdirOverride is empty unless start_service_at pinned a custom
	// directory (e.g. a git worktree).
	WorkdirOverride string `json:"workdir_override,omitempty"`
	// GitBranch is the current git branch of the effective workdir, or ""
	// when it is not inside a git repository.
	GitBranch  string `json:"git_branch,omitempty"`
	Runner     string `json:"runner,omitempty"`
	HealthPort int    `json:"health_port,omitempty"`
	// Running reports whether the service currently owns a live process.
	Running bool `json:"running"`
}

// ServiceListDTO is the payload of list_services.
type ServiceListDTO struct {
	Services []ServiceDTO `json:"services"`
	Total    int          `json:"total"`
	Running  int          `json:"running"`
}

// ActionResultDTO is the payload of every state-changing tool (start, stop,
// restart, profiles, generate_sources).
type ActionResultDTO struct {
	OK      bool   `json:"ok"`
	Action  string `json:"action"`
	Service string `json:"service,omitempty"`
	Status  string `json:"status,omitempty"`
	PID     int    `json:"pid,omitempty"`
	Workdir string `json:"workdir,omitempty"`
	Message string `json:"message"`
	// NextStep tells the agent what to do next when the action is
	// asynchronous, e.g. "call wait_until_ready('Benefits')".
	NextStep string `json:"next_step,omitempty"`
}

// BulkActionResultDTO is the payload of start_all / stop_all.
type BulkActionResultDTO struct {
	OK       bool     `json:"ok"`
	Action   string   `json:"action"`
	Services []string `json:"services"`
	Message  string   `json:"message"`
	NextStep string   `json:"next_step,omitempty"`
}

// LogsDTO is the payload of get_logs.
type LogsDTO struct {
	Service string   `json:"service"`
	Lines   []string `json:"lines"`
	// FirstLineIndex/LastLineIndex are global, monotonic indexes. Pass
	// LastLineIndex back as since_index to read only what is new.
	FirstLineIndex int `json:"first_line_index"`
	LastLineIndex  int `json:"last_line_index"`
	ReturnedLines  int `json:"returned_lines"`
	BufferedLines  int `json:"buffered_lines"`
	// Truncated reports that older lines were dropped from the answer (either
	// evicted from the ring buffer or cut by the requested line count).
	Truncated bool `json:"truncated"`
}

// LogMatchDTO is a single hit of search_logs.
type LogMatchDTO struct {
	LineIndex int      `json:"line_index"`
	Text      string   `json:"text"`
	Before    []string `json:"before,omitempty"`
	After     []string `json:"after,omitempty"`
}

// SearchLogsDTO is the payload of search_logs.
type SearchLogsDTO struct {
	Service    string        `json:"service"`
	Pattern    string        `json:"pattern"`
	Matches    []LogMatchDTO `json:"matches"`
	MatchCount int           `json:"match_count"`
	// ScannedLines is how many buffered lines were searched. The buffer is
	// bounded, so "no match" only means "not in the retained window".
	ScannedLines  int  `json:"scanned_lines"`
	BufferedLines int  `json:"buffered_lines"`
	Truncated     bool `json:"truncated"`
}

// StatsDTO is the resource usage of one service.
type StatsDTO struct {
	Service    string  `json:"service"`
	Status     string  `json:"status"`
	PID        int     `json:"pid,omitempty"`
	MemBytes   int64   `json:"mem_bytes"`
	MemHuman   string  `json:"mem_human"`
	CPUPercent float64 `json:"cpu_percent"`
	// SampledMs is the window over which CPU was measured. CPU usage is a
	// delta, so a zero window means the value is not meaningful.
	SampledMs int64 `json:"sampled_ms"`
}

// StatsListDTO is the payload of get_stats.
type StatsListDTO struct {
	Services      []StatsDTO `json:"services"`
	TotalMemBytes int64      `json:"total_mem_bytes"`
	TotalMemHuman string     `json:"total_mem_human"`
}

// WaitResultDTO is the payload of wait_until_ready.
type WaitResultDTO struct {
	Service    string `json:"service"`
	Ready      bool   `json:"ready"`
	Status     string `json:"status"`
	WaitedMs   int64  `json:"waited_ms"`
	TimedOut   bool   `json:"timed_out"`
	PortOpen   bool   `json:"port_open"`
	HealthPort int    `json:"health_port,omitempty"`
	Message    string `json:"message"`
	// RecentLogs carries the tail of the log when the wait failed, so the
	// agent can diagnose without a second round-trip.
	RecentLogs []string `json:"recent_logs,omitempty"`
}

// ServiceConfigDTO is the read-only, effective configuration of a service.
type ServiceConfigDTO struct {
	Name              string `json:"name"`
	Runner            string `json:"runner,omitempty"`
	ConfiguredWorkdir string `json:"configured_workdir"`
	EffectiveWorkdir  string `json:"effective_workdir"`
	WorkdirOverride   string `json:"workdir_override,omitempty"`
	// GitBranch is the current git branch of the effective workdir, or ""
	// when it is not inside a git repository.
	GitBranch    string `json:"git_branch,omitempty"`
	BuildCommand string `json:"build_command,omitempty"`
	RunCommand   string `json:"run_command,omitempty"`
	// BuildCommandError/RunCommandError explain why a command could not be
	// resolved (missing preset, bad template) instead of failing the call.
	BuildCommandError string            `json:"build_command_error,omitempty"`
	RunCommandError   string            `json:"run_command_error,omitempty"`
	Profiles          []string          `json:"profiles,omitempty"`
	Modules           []string          `json:"modules,omitempty"`
	MainClass         string            `json:"main_class,omitempty"`
	JavaHome          string            `json:"java_home,omitempty"`
	HealthPort        int               `json:"health_port,omitempty"`
	Vars              map[string]string `json:"vars,omitempty"`
	// EnvKeys always lists the declared variables; Env only carries values when
	// the caller asked for them, and secret-looking keys stay masked.
	EnvKeys     []string          `json:"env_keys,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	EnvRedacted bool              `json:"env_redacted"`
}

// PresetDTO is a reusable build/run recipe.
type PresetDTO struct {
	Name               string   `json:"name"`
	Build              string   `json:"build,omitempty"`
	Run                string   `json:"run,omitempty"`
	SdlGenerateCommand string   `json:"sdl_generate_command,omitempty"`
	EnvKeys            []string `json:"env_keys,omitempty"`
	TemplateVars       []string `json:"template_vars,omitempty"`
}

// PresetListDTO is the payload of the smtui://presets resource.
type PresetListDTO struct {
	Presets []PresetDTO `json:"presets"`
	Total   int         `json:"total"`
}
