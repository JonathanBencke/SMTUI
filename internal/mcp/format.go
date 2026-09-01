package mcp

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/JonathanBencke/ServiceManagerTUI/internal/config"
	"github.com/JonathanBencke/ServiceManagerTUI/internal/service"
)

// secretKeyRe matches environment variable names whose value must never be
// echoed back, even when the caller explicitly asks for env values.
var secretKeyRe = regexp.MustCompile(`(?i)(pass|secret|token|key|credential|auth)`)

const redactedValue = "***redacted***"

// --- DTO builders -----------------------------------------------------------

// newServiceDTO snapshots the runtime state of a service.
func newServiceDTO(s *service.Service) ServiceDTO {
	uptime := s.Uptime()
	status := string(s.Status())

	profiles := s.Profiles()
	if profiles == nil {
		profiles = []string{}
	}

	dto := ServiceDTO{
		Name:              s.Name(),
		Status:            status,
		PID:               s.PID(),
		Profiles:          profiles,
		Workdir:           s.Workdir(),
		ConfiguredWorkdir: s.ConfiguredWorkdir(),
		WorkdirOverride:   s.WorkdirOverride(),
		GitBranch:         s.GitBranch(),
		Runner:            s.Runner(),
		HealthPort:        s.HealthPort(),
		Running:           status == string(service.StatusRunning),
	}
	if uptime > 0 {
		dto.UptimeSeconds = int64(uptime.Seconds())
		dto.UptimeHuman = formatUptime(uptime)
	}
	return dto
}

func newServiceListDTO(services []*service.Service) ServiceListDTO {
	dto := ServiceListDTO{Services: make([]ServiceDTO, 0, len(services)), Total: len(services)}
	for _, s := range services {
		item := newServiceDTO(s)
		if item.Running {
			dto.Running++
		}
		dto.Services = append(dto.Services, item)
	}
	return dto
}

// newServiceConfigDTO renders the effective configuration of a service.
// Environment values are redacted unless includeEnv is set, and secret-looking
// keys are masked either way.
func newServiceConfigDTO(s *service.Service, includeEnv bool) ServiceConfigDTO {
	cfg := s.Config()

	dto := ServiceConfigDTO{
		Name:              s.Name(),
		Runner:            cfg.Runner,
		ConfiguredWorkdir: s.ConfiguredWorkdir(),
		EffectiveWorkdir:  s.Workdir(),
		WorkdirOverride:   s.WorkdirOverride(),
		GitBranch:         s.GitBranch(),
		Profiles:          cfg.Profiles,
		Modules:           cfg.Modules,
		MainClass:         cfg.MainClass,
		JavaHome:          cfg.JavaHome,
		HealthPort:        cfg.HealthPort,
		Vars:              cfg.Vars,
		EnvRedacted:       !includeEnv,
	}

	if build, err := s.ResolvedCommand("build"); err != nil {
		dto.BuildCommandError = err.Error()
	} else {
		dto.BuildCommand = build
	}
	if run, err := s.ResolvedCommand("run"); err != nil {
		dto.RunCommandError = err.Error()
	} else {
		dto.RunCommand = run
	}

	env := s.ConfiguredEnv()
	dto.EnvKeys = sortedKeys(env)
	if includeEnv {
		dto.Env = redactEnv(env)
	}
	return dto
}

// redactEnv masks the values of secret-looking variables.
func redactEnv(env map[string]string) map[string]string {
	out := make(map[string]string, len(env))
	for k, v := range env {
		if secretKeyRe.MatchString(k) {
			out[k] = redactedValue
			continue
		}
		out[k] = v
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// newPresetListDTO describes the reusable build/run recipes from the config.
func newPresetListDTO(cfg *config.Config) PresetListDTO {
	names := make([]string, 0, len(cfg.Presets))
	for name := range cfg.Presets {
		names = append(names, name)
	}
	sort.Strings(names)

	dto := PresetListDTO{Presets: make([]PresetDTO, 0, len(names)), Total: len(names)}
	for _, name := range names {
		p := cfg.Presets[name]
		dto.Presets = append(dto.Presets, PresetDTO{
			Name:               name,
			Build:              p.Build,
			Run:                p.Run,
			SdlGenerateCommand: p.SdlGenerateCommand,
			EnvKeys:            sortedKeys(p.Env),
			TemplateVars:       p.TemplateVars(),
		})
	}
	return dto
}

// --- Text renderers ---------------------------------------------------------

// renderServiceList reproduces the ASCII table historically returned by
// list_services, so clients that only read the text content keep working.
func renderServiceList(dto ServiceListDTO) string {
	lines := []string{
		fmt.Sprintf("| %-14s | %-10s | %-8s | %-10s | %-8s |", "Name", "Status", "Profile", "PID", "Uptime"),
		"|----------------+------------+----------+------------+----------|",
	}

	for _, s := range dto.Services {
		profile := strings.Join(s.Profiles, ",")
		if profile == "" {
			profile = "none"
		}
		pid := "-"
		if s.PID > 0 {
			pid = fmt.Sprintf("%d", s.PID)
		}
		uptime := "-"
		if s.UptimeHuman != "" {
			uptime = s.UptimeHuman
		}
		lines = append(lines, fmt.Sprintf("| %-14s | %-10s | %-8s | %-10s | %-8s |",
			s.Name, s.Status, profile, pid, uptime))
	}

	return strings.Join(append(lines, workdirOverrideLines(dto)...), "\n")
}

// workdirOverrideLines renders the footnote listing services currently running
// from a custom working directory (set via start_service_at), so the caller can
// tell which worktree is in use and how to restore the configured directory.
func workdirOverrideLines(dto ServiceListDTO) []string {
	var overrides []string
	for _, s := range dto.Services {
		if s.WorkdirOverride != "" {
			overrides = append(overrides, fmt.Sprintf("  %s → %s (configured: %s)", s.Name, s.WorkdirOverride, s.ConfiguredWorkdir))
		}
	}
	if len(overrides) == 0 {
		return nil
	}
	return append([]string{"", "Custom working directories (set via start_service_at):"}, overrides...)
}

func renderStats(dto StatsListDTO) string {
	lines := []string{
		fmt.Sprintf("| %-14s | %-10s | %-8s | %-8s |", "Name", "Status", "CPU", "Memory"),
		"|----------------+------------+----------+----------|",
	}
	for _, s := range dto.Services {
		cpu, mem := "-", "-"
		if s.PID > 0 {
			cpu = fmt.Sprintf("%.1f%%", s.CPUPercent)
			mem = s.MemHuman
		}
		lines = append(lines, fmt.Sprintf("| %-14s | %-10s | %-8s | %-8s |", s.Service, s.Status, cpu, mem))
	}
	if dto.TotalMemBytes > 0 {
		lines = append(lines, "", fmt.Sprintf("Total memory: %s", dto.TotalMemHuman))
	}
	return strings.Join(lines, "\n")
}

func renderSearchLogs(dto SearchLogsDTO) string {
	if dto.MatchCount == 0 {
		return fmt.Sprintf("No match for %q in the %d buffered lines of '%s'. The log buffer is bounded, so an older occurrence may already have been dropped.",
			dto.Pattern, dto.BufferedLines, dto.Service)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%d match(es) for %q in '%s' (%d lines scanned)", dto.MatchCount, dto.Pattern, dto.Service, dto.ScannedLines)
	if dto.Truncated {
		sb.WriteString(" — truncated, refine the pattern or raise max_results")
	}
	for _, m := range dto.Matches {
		sb.WriteString("\n")
		for _, b := range m.Before {
			fmt.Fprintf(&sb, "\n     %s", b)
		}
		fmt.Fprintf(&sb, "\n%4d> %s", m.LineIndex, m.Text)
		for _, a := range m.After {
			fmt.Fprintf(&sb, "\n     %s", a)
		}
	}
	return sb.String()
}

func renderServiceConfig(dto ServiceConfigDTO) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Service: %s", dto.Name)
	if dto.Runner != "" {
		fmt.Fprintf(&sb, "\nPreset: %s", dto.Runner)
	}
	fmt.Fprintf(&sb, "\nConfigured workdir: %s", dto.ConfiguredWorkdir)
	if dto.WorkdirOverride != "" {
		fmt.Fprintf(&sb, "\nEffective workdir: %s (override set via start_service_at)", dto.EffectiveWorkdir)
	}
	if dto.GitBranch != "" {
		fmt.Fprintf(&sb, "\nGit branch: %s", dto.GitBranch)
	}
	writeCommand(&sb, "Build", dto.BuildCommand, dto.BuildCommandError)
	writeCommand(&sb, "Run", dto.RunCommand, dto.RunCommandError)
	if len(dto.Profiles) > 0 {
		fmt.Fprintf(&sb, "\nProfiles: %s", strings.Join(dto.Profiles, ","))
	}
	if dto.HealthPort > 0 {
		fmt.Fprintf(&sb, "\nHealth port: %d", dto.HealthPort)
	}
	if len(dto.EnvKeys) > 0 {
		fmt.Fprintf(&sb, "\nEnv variables (%d): %s", len(dto.EnvKeys), strings.Join(dto.EnvKeys, ", "))
		if dto.EnvRedacted {
			sb.WriteString("\n(values hidden — call again with include_env=true)")
		}
	}
	return sb.String()
}

func writeCommand(sb *strings.Builder, label, cmd, cmdErr string) {
	if cmdErr != "" {
		fmt.Fprintf(sb, "\n%s: unavailable (%s)", label, cmdErr)
		return
	}
	if cmd != "" {
		fmt.Fprintf(sb, "\n%s: %s", label, cmd)
	}
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
