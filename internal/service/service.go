package service

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"text/template"
	"time"

	"github.com/JonathanBencke/ServiceManagerTUI/internal/config"
	"github.com/JonathanBencke/ServiceManagerTUI/internal/logbuffer"
)

type Status string

const (
	StatusIdle       Status = "idle"
	StatusBuilding   Status = "building"
	StatusGenerating Status = "generating"
	StatusRunning    Status = "running"
	StatusStopping   Status = "stopping"
	StatusStopped    Status = "stopped"
	StatusCrashed    Status = "crashed"
)

// isActive reports whether a status means the service currently owns a running
// command (its process, a build or a standalone generate-sources), which makes
// it ineligible for a concurrent start.
func isActive(s Status) bool {
	return s == StatusRunning || s == StatusBuilding || s == StatusStopping || s == StatusGenerating
}

// hasProcess reports whether a status implies a launched process that must be
// stopped before another command runs in the same workdir.
func hasProcess(s Status) bool {
	return s == StatusRunning || s == StatusBuilding || s == StatusStopping
}

const createNoWindow = 0x08000000

// defaultSdlGenerateCommand is used when a preset does not override
// SdlGenerateCommand.
const defaultSdlGenerateCommand = "mvn clean generate-sources"

// sdlMarkerGlob matches the SDL contract file at the root of a Senior
// SDL/PDL/EDL project. It is usually named main.sdl, but can also take the
// project's domain name (e.g. career.sdl), so any *.sdl file qualifies.
const sdlMarkerGlob = "*.sdl"

// maxSdlRootSearchDepth bounds how many parent directories findSdlRoot walks
// up from a service's workdir before giving up.
const maxSdlRootSearchDepth = 5

// noSdlRootMessage is logged when an explicit generate-sources request is
// skipped because the service's workdir does not sit inside an SDL/PDL/EDL
// project.
const noSdlRootMessage = "No main.sdl found, skipping generate-sources"

type Service struct {
	cfg      config.ServiceConfig
	defaults config.Defaults
	preset   config.Preset
	rootDir  string

	mu sync.Mutex
	// workdirOverride, when set, replaces cfg.Workdir for every command run by
	// this service (build, run and generate-sources). It is populated by
	// StartAt to support running the service from a git worktree.
	workdirOverride string
	status          Status
	cmd             *exec.Cmd
	pid             int
	pipe            io.ReadCloser
	cancel          context.CancelFunc
	prevCPU         float64
	prevStatsTime   time.Time
	startedAt       time.Time
	gitBranch       string
	gitBranchDir    string
	logs            *logbuffer.RingBuffer
	onLog           func(name, line string)
	onStatus        func(name string, status Status, pid int)
}

func New(cfg config.ServiceConfig, defaults config.Defaults, preset config.Preset, rootDir string) *Service {
	return &Service{
		cfg:      cfg,
		defaults: defaults,
		preset:   preset,
		rootDir:  rootDir,
		status:   StatusIdle,
		logs:     logbuffer.New(500),
	}
}

func (s *Service) Name() string    { return s.cfg.Name }
func (s *Service) Status() Status  { s.mu.Lock(); defer s.mu.Unlock(); return s.status }
func (s *Service) PID() int        { s.mu.Lock(); defer s.mu.Unlock(); return s.pid }
func (s *Service) Logs() []string  { return s.logs.Lines() }
func (s *Service) HealthPort() int { return s.cfg.HealthPort }

// Runner returns the name of the preset the service references, or an empty
// string when it declares its own build/run commands.
func (s *Service) Runner() string { return s.cfg.Runner }

// LogsSince returns the buffered log lines whose global index is >= since,
// together with the cursor to use on the next call. It lets a caller follow a
// long build by reading only what is new.
func (s *Service) LogsSince(since int) (lines []string, next int) {
	return s.logs.LinesSince(since)
}

// LogCursor returns the index the next emitted log line will take.
func (s *Service) LogCursor() int { return s.logs.Written() }

// Config returns a deep copy of the service configuration, so callers can
// inspect (and safely mutate) it without racing the running service.
func (s *Service) Config() config.ServiceConfig {
	s.mu.Lock()
	c := s.cfg
	c.Profiles = append([]string(nil), s.cfg.Profiles...)
	s.mu.Unlock()
	c.Env = copyStringMap(s.cfg.Env)
	c.Vars = copyStringMap(s.cfg.Vars)
	c.Modules = append([]string(nil), s.cfg.Modules...)
	return c
}

// ResolvedCommand expands the "build" or "run" command template with the
// service's template data, returning the exact command line that would run.
func (s *Service) ResolvedCommand(kind string) (string, error) {
	return s.resolveCommand(kind)
}

// ConfiguredEnv returns the environment variables declared by the
// configuration (defaults, then preset, then service — last wins), rendered
// through the template engine. The ambient OS environment is deliberately
// excluded: callers want the service's own contribution, not the whole
// process environment.
func (s *Service) ConfiguredEnv() map[string]string {
	data := s.templateData()
	env := map[string]string{}
	merge := func(m map[string]string) {
		for k, v := range m {
			rendered, err := renderTemplate("env", v, data)
			if err != nil {
				rendered = v
			}
			env[k] = rendered
		}
	}
	merge(s.defaults.Env)
	merge(s.preset.Env)
	merge(s.cfg.Env)
	return env
}

func copyStringMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// ConfiguredWorkdir returns the working directory declared in the
// configuration file, ignoring any runtime override.
func (s *Service) ConfiguredWorkdir() string { return s.cfg.Workdir }

// Workdir returns the effective working directory used by build, run and
// generate-sources: the override set by StartAt when present, otherwise the
// configured one.
func (s *Service) Workdir() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.workdirLocked()
}

// workdirLocked is Workdir without locking; callers must hold s.mu.
func (s *Service) workdirLocked() string {
	if s.workdirOverride != "" {
		return s.workdirOverride
	}
	return s.cfg.Workdir
}

// WorkdirOverride returns the custom working directory in effect, or an empty
// string when the service uses the configured one.
func (s *Service) WorkdirOverride() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.workdirOverride
}

func (s *Service) Uptime() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.startedAt.IsZero() {
		return 0
	}
	return time.Since(s.startedAt)
}

// RefreshGitBranch recalculates and caches the git branch of the service's
// effective workdir. It spawns a "git" process, so it must only be called
// from discrete events (start, workdir change, config reload) — never from
// the periodic stats tick, which would spawn git every few seconds for every
// service regardless of whether the branch could have changed.
func (s *Service) RefreshGitBranch() {
	dir := s.Workdir()
	branch := gitBranch(dir)
	s.mu.Lock()
	s.gitBranch = branch
	s.gitBranchDir = dir
	s.mu.Unlock()
}

// GitBranch returns the cached git branch name for the service's effective
// workdir, or "" when the workdir is not inside a git repository. If the
// cache is stale (workdir changed since the last refresh, or no refresh ever
// ran) it computes and caches the branch synchronously before returning it.
func (s *Service) GitBranch() string {
	dir := s.Workdir()

	s.mu.Lock()
	if s.gitBranchDir == dir {
		branch := s.gitBranch
		s.mu.Unlock()
		return branch
	}
	s.mu.Unlock()

	branch := gitBranch(dir)
	s.mu.Lock()
	s.gitBranch = branch
	s.gitBranchDir = dir
	s.mu.Unlock()
	return branch
}

func (s *Service) SetCallbacks(onLog func(name, line string), onStatus func(name string, status Status, pid int)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onLog = onLog
	s.onStatus = onStatus
}

func (s *Service) emitLog(line string) {
	s.logs.Write(line)
	if s.onLog != nil {
		s.onLog(s.cfg.Name, line)
	}
}

func (s *Service) setStatus(status Status, pid int) {
	s.mu.Lock()
	s.status = status
	s.pid = pid
	cb := s.onStatus
	s.mu.Unlock()
	if cb != nil {
		cb(s.cfg.Name, status, pid)
	}
}

// templateData exposes the variables available to build/run/env templates.
// It is a map (rather than a struct) so presets can reference custom variables
// declared under [service.vars] in addition to the built-in ones.
func (s *Service) templateData() map[string]string {
	data := map[string]string{
		"Name":      s.cfg.Name,
		"Workdir":   s.Workdir(),
		"JavaHome":  s.cfg.JavaHome,
		"Modules":   strings.Join(s.cfg.Modules, ","),
		"MainClass": s.cfg.MainClass,
		"Profiles":  strings.Join(s.cfg.Profiles, ","),
		"Path":      envPath(),
	}
	// Custom preset variables (e.g. {{.IntegrationProperties}}) overlay the
	// built-ins; they never overwrite the reserved keys above in practice.
	for k, v := range s.cfg.Vars {
		data[k] = v
	}
	return data
}

func renderTemplate(name, tmpl string, data interface{}) (string, error) {
	// missingkey=zero renders unknown {{.X}} references as an empty string
	// instead of "<no value>", so an optional preset variable left unset does
	// not corrupt the command.
	t, err := template.New(name).Option("missingkey=zero").Parse(tmpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// resolveCommand expands the build/run template for the given kind, using the
// service override first, then the preset, then an error.
func (s *Service) resolveCommand(kind string) (string, error) {
	var raw string
	switch kind {
	case "build":
		switch {
		case s.cfg.BuildCommand != "":
			raw = s.cfg.BuildCommand
		case s.preset.Build != "":
			raw = s.preset.Build
		default:
			return "", fmt.Errorf("no build command: set 'build_command' on service or define preset %q with 'build'", s.cfg.Runner)
		}
	case "run":
		switch {
		case s.cfg.RunCommand != "":
			raw = s.cfg.RunCommand
		case s.preset.Run != "":
			raw = s.preset.Run
		default:
			return "", fmt.Errorf("no run command: set 'run_command' on service or define preset %q with 'run'", s.cfg.Runner)
		}
	default:
		return "", fmt.Errorf("unknown command kind: %s", kind)
	}
	return renderTemplate(kind, raw, s.templateData())
}

// env builds the process environment: OS env, then defaults.Env, preset.Env
// and service.Env (last wins). Values support templates.
func (s *Service) env() []string {
	data := s.templateData()
	env := append([]string{}, defaultEnv()...)

	merge := func(m map[string]string) {
		for k, v := range m {
			rendered, err := renderTemplate("env", v, data)
			if err != nil {
				rendered = v
			}
			env = append(env, fmt.Sprintf("%s=%s", k, rendered))
		}
	}

	merge(s.defaults.Env)
	merge(s.preset.Env)
	merge(s.cfg.Env)

	return dedupEnv(env)
}

// dedupEnv keeps the last occurrence of each variable name (case-insensitive),
// so preset/service overrides win over the ambient environment.
func dedupEnv(env []string) []string {
	seen := make(map[string]int)
	var result []string
	for _, e := range env {
		key := strings.ToUpper(strings.SplitN(e, "=", 2)[0])
		if idx, ok := seen[key]; ok {
			result[idx] = e
		} else {
			seen[key] = len(result)
			result = append(result, e)
		}
	}
	return result
}

// shellSplit splits a command string honoring single and double quotes.
func shellSplit(s string) ([]string, error) {
	var args []string
	var current strings.Builder
	inSingle, inDouble, inToken := false, false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\'' && !inDouble:
			inSingle = !inSingle
			inToken = true
		case c == '"' && !inSingle:
			inDouble = !inDouble
			inToken = true
		case (c == ' ' || c == '\t') && !inSingle && !inDouble:
			if inToken {
				args = append(args, current.String())
				current.Reset()
				inToken = false
			}
		default:
			current.WriteByte(c)
			inToken = true
		}
	}
	if inSingle || inDouble {
		return nil, fmt.Errorf("unterminated quote in command: %s", s)
	}
	if inToken {
		args = append(args, current.String())
	}
	if len(args) == 0 {
		return nil, fmt.Errorf("empty command")
	}
	return args, nil
}

func (s *Service) streamReader(r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line != "" {
			s.emitLog(line)
		}
	}
}

// streamCombined streams stdout and stderr concurrently and returns a channel
// closed when both readers reach EOF.
func (s *Service) streamCombined(stdout, stderr io.Reader) chan struct{} {
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); s.streamReader(stdout) }()
	go func() { defer wg.Done(); s.streamReader(stderr) }()
	go func() { wg.Wait(); close(done) }()
	return done
}

// hasBuildCommand reports whether the service has a build step defined either
// inline (build_command) or via its preset.
func (s *Service) hasBuildCommand() bool {
	return s.cfg.BuildCommand != "" || s.preset.Build != ""
}

// findSdlRoot walks up from workdir through up to maxSdlRootSearchDepth
// parent directories looking for a *.sdl file (typically main.sdl, but
// sometimes named after the project's domain, e.g. career.sdl), which marks
// the root of a Senior SDL/PDL/EDL project (workdir is typically
// <root>/java/impl). It returns the directory containing the .sdl file and
// true on success, or "" and false if none is found within the search depth.
func findSdlRoot(workdir string) (string, bool) {
	dir := workdir
	for i := 0; i <= maxSdlRootSearchDepth; i++ {
		matches, err := filepath.Glob(filepath.Join(dir, sdlMarkerGlob))
		if err == nil && len(matches) > 0 {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", false
}

// sdlGenerateCommand returns the command used to generate sources for an SDL
// project: the preset's SdlGenerateCommand override if set, otherwise the
// default "mvn clean generate-sources".
func (s *Service) sdlGenerateCommand() string {
	if s.preset.SdlGenerateCommand != "" {
		return s.preset.SdlGenerateCommand
	}
	return defaultSdlGenerateCommand
}

// runSyncStep runs cmdStr synchronously in dir, streaming its combined
// stdout/stderr to the service log, and blocks until it exits. It is shared
// by the generate-sources and build steps, which both need to run to
// completion before the next step starts.
func (s *Service) runSyncStep(cmdStr, dir string) error {
	parts, err := shellSplit(cmdStr)
	if err != nil {
		return fmt.Errorf("parse command: %w", err)
	}

	cmd := exec.Command(parts[0], parts[1:]...)
	cmd.Dir = dir
	cmd.Env = s.env()
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("create pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("create pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start: %w", err)
	}

	done := s.streamCombined(stdout, stderr)
	<-done

	if err := cmd.Wait(); err != nil {
		return err
	}
	return nil
}

// startStep describes one synchronous step (currently the build) run before
// the service's process is started.
type startStep struct {
	label      string
	cmd        string
	dir        string
	resolveErr error
}

// buildStartSteps assembles the ordered list of synchronous steps to run
// before starting the service. Currently only the build step (if configured):
// source generation is never triggered by a start, it is an explicit action
// (see GenerateSources).
func (s *Service) buildStartSteps() []startStep {
	var steps []startStep

	if s.hasBuildCommand() {
		buildCmdStr, err := s.resolveCommand("build")
		steps = append(steps, startStep{
			label:      "Building",
			cmd:        buildCmdStr,
			dir:        s.Workdir(),
			resolveErr: err,
		})
	}

	return steps
}

// resolveWorkdir validates a custom working directory (typically a git
// worktree) and returns it in absolute form. Relative paths are resolved
// against the process' current directory.
func resolveWorkdir(dir string) (string, error) {
	trimmed := strings.TrimSpace(dir)
	if trimmed == "" {
		return "", fmt.Errorf("workdir is empty")
	}
	abs, err := filepath.Abs(trimmed)
	if err != nil {
		return "", fmt.Errorf("resolve workdir %q: %w", trimmed, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("workdir %q: %w", abs, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("workdir %q is not a directory", abs)
	}
	return abs, nil
}

// setWorkdirOverride records a validated custom working directory. Passing the
// configured workdir clears the override, so the caller can restore the
// original directory without a dedicated API. It is refused while the service
// owns a running command, since that command would keep the previous
// directory. Returns the effective workdir in place after the change.
func (s *Service) setWorkdirOverride(abs string) (string, error) {
	s.mu.Lock()
	if isActive(s.status) {
		st := s.status
		s.mu.Unlock()
		return "", fmt.Errorf("%s is %s: stop it before changing the working directory", s.cfg.Name, st)
	}
	if strings.EqualFold(abs, s.cfg.Workdir) {
		s.workdirOverride = ""
	} else {
		s.workdirOverride = abs
	}
	effective := s.workdirLocked()
	s.mu.Unlock()
	return effective, nil
}

// clearWorkdirOverride drops the custom workdir, reporting whether one was in
// effect and which it was. It is a no-op while the service owns a running
// command, since that command's directory cannot change anymore.
func (s *Service) clearWorkdirOverride() (bool, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := s.workdirOverride
	if previous == "" || isActive(s.status) {
		return false, ""
	}
	s.workdirOverride = ""
	return true, previous
}

// StartConfigured starts the service from the workdir declared in the
// configuration file, dropping any custom directory previously set by StartAt.
// It backs every start triggered from the terminal UI, which must always obey
// the configured workdir.
func (s *Service) StartConfigured() error {
	if cleared, previous := s.clearWorkdirOverride(); cleared {
		s.emitLog(fmt.Sprintf("Back to the configured workdir: %s (was %s)", s.cfg.Workdir, previous))
	}
	return s.Start()
}

// StartAt starts the service from a custom working directory instead of the
// one declared in the configuration file — typically a git worktree created
// for a branch. The override is sticky for later Start and GenerateSources
// calls, so an agent can rebuild, restart and regenerate sources against the
// same checkout. It is dropped by StartConfigured (any start triggered from
// the terminal UI) or by passing the configured workdir back.
func (s *Service) StartAt(workdir string) error {
	abs, err := resolveWorkdir(workdir)
	if err != nil {
		return err
	}

	effective, err := s.setWorkdirOverride(abs)
	if err != nil {
		return err
	}

	if effective == s.cfg.Workdir {
		s.emitLog(fmt.Sprintf("Using configured workdir: %s", effective))
	} else {
		s.emitLog(fmt.Sprintf("Using custom workdir: %s (configured: %s)", effective, s.cfg.Workdir))
	}

	return s.Start()
}

func (s *Service) Start() error {
	s.mu.Lock()
	if isActive(s.status) {
		st := s.status
		s.mu.Unlock()
		if st == StatusGenerating {
			return fmt.Errorf("%s is generating sources", s.cfg.Name)
		}
		return fmt.Errorf("%s already running", s.cfg.Name)
	}
	s.mu.Unlock()

	// Warms the git branch cache off the hot path: GitBranch() would
	// otherwise pay for this exec.Command synchronously on the next render.
	go s.RefreshGitBranch()

	s.setStatus(StatusBuilding, 0)

	steps := s.buildStartSteps()
	total := len(steps) + 1 // + the run step, always present

	for i, step := range steps {
		if step.resolveErr != nil {
			s.emitLog(fmt.Sprintf("Cannot resolve build command: %v", step.resolveErr))
			s.setStatus(StatusCrashed, 0)
			return step.resolveErr
		}

		s.emitLog(fmt.Sprintf("[%d/%d] %s (%s)...", i+1, total, step.label, step.cmd))

		if err := s.runSyncStep(step.cmd, step.dir); err != nil {
			s.emitLog(fmt.Sprintf("%s failed: %v", step.label, err))
			s.setStatus(StatusCrashed, 0)
			return err
		}
	}

	runCmdStr, err := s.resolveCommand("run")
	if err != nil {
		s.emitLog(fmt.Sprintf("Cannot resolve run command: %v", err))
		s.setStatus(StatusCrashed, 0)
		return err
	}

	s.emitLog(fmt.Sprintf("[%d/%d] Starting (%s)...", total, total, runCmdStr))

	runParts, err := shellSplit(runCmdStr)
	if err != nil {
		s.emitLog(fmt.Sprintf("Parse run command: %v", err))
		s.setStatus(StatusCrashed, 0)
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	runCmd := exec.CommandContext(ctx, runParts[0], runParts[1:]...)
	runCmd.Dir = s.Workdir()
	runCmd.Env = s.env()
	runCmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}

	runStdout, err := runCmd.StdoutPipe()
	if err != nil {
		s.emitLog(fmt.Sprintf("Failed to create pipe: %v", err))
		s.setStatus(StatusCrashed, 0)
		cancel()
		return err
	}
	runStderr, err := runCmd.StderrPipe()
	if err != nil {
		s.emitLog(fmt.Sprintf("Failed to create pipe: %v", err))
		s.setStatus(StatusCrashed, 0)
		cancel()
		return err
	}

	if err := runCmd.Start(); err != nil {
		s.emitLog(fmt.Sprintf("Failed to start: %v", err))
		s.setStatus(StatusCrashed, 0)
		cancel()
		return err
	}

	s.mu.Lock()
	s.cmd = runCmd
	s.pipe = runStdout
	s.cancel = cancel
	s.startedAt = time.Now()
	s.mu.Unlock()

	s.setStatus(StatusRunning, runCmd.Process.Pid)

	go func() {
		runDone := s.streamCombined(runStdout, runStderr)

		waitCh := make(chan error, 1)
		go func() {
			waitCh <- runCmd.Wait()
		}()

		select {
		case <-runDone:
			err := <-waitCh
			s.mu.Lock()
			s.cmd = nil
			s.pipe = nil
			s.cancel = nil
			s.mu.Unlock()
			if err != nil {
				s.emitLog(fmt.Sprintf("Process exited with error: %v", err))
				s.setStatus(StatusCrashed, 0)
			} else {
				s.setStatus(StatusStopped, 0)
			}
		case err := <-waitCh:
			<-runDone
			s.mu.Lock()
			s.cmd = nil
			s.pipe = nil
			s.cancel = nil
			s.mu.Unlock()
			if err != nil {
				s.emitLog(fmt.Sprintf("Process exited with error: %v", err))
				s.setStatus(StatusCrashed, 0)
			} else {
				s.setStatus(StatusStopped, 0)
			}
		}
	}()

	return nil
}

// beginGenerating atomically transitions the service into StatusGenerating,
// refusing if a generation is already in progress. It is the single guard
// against two concurrent generate-sources runs on the same service, since the
// TUI shortcut and the MCP tool can trigger it independently.
func (s *Service) beginGenerating() error {
	s.mu.Lock()
	if s.status == StatusGenerating {
		s.mu.Unlock()
		return fmt.Errorf("%s is already generating sources", s.cfg.Name)
	}
	s.status = StatusGenerating
	s.pid = 0
	cb := s.onStatus
	s.mu.Unlock()
	if cb != nil {
		cb(s.cfg.Name, StatusGenerating, 0)
	}
	return nil
}

// idleStatus returns the resting status to fall back to after a standalone
// step: stopped if the service already ran at least once, idle otherwise.
func (s *Service) idleStatus() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.startedAt.IsZero() {
		return StatusIdle
	}
	return StatusStopped
}

// GenerateSources runs only the SDL/PDL/EDL source generation step for this
// service, without building or starting it. A running service is stopped
// first (it is not restarted afterwards), since the generation rewrites the
// same generated sources the process is using.
//
// If the workdir does not sit inside an SDL project, the step is skipped unless
// the preset explicitly enables generation in the service workdir.
func (s *Service) GenerateSources() error {
	s.mu.Lock()
	current := s.status
	s.mu.Unlock()

	if current == StatusGenerating {
		return fmt.Errorf("%s is already generating sources", s.cfg.Name)
	}

	if hasProcess(current) {
		s.emitLog("Stopping service before generate-sources...")
		if err := s.Stop(); err != nil {
			s.emitLog(fmt.Sprintf("Stop before generate-sources: %v", err))
		}
	}

	generateDir := s.Workdir()
	if sdlRoot, ok := findSdlRoot(s.Workdir()); ok {
		generateDir = sdlRoot
	} else if !s.preset.GenerateInWorkdir {
		s.emitLog(noSdlRootMessage)
		return nil
	}

	if err := s.beginGenerating(); err != nil {
		return err
	}

	cmdStr, err := renderTemplate("generate-sources", s.sdlGenerateCommand(), s.templateData())
	if err != nil {
		s.emitLog(fmt.Sprintf("Generating sources failed: %v", err))
		s.setStatus(StatusCrashed, 0)
		return err
	}
	s.emitLog(fmt.Sprintf("Generating sources (%s)...", cmdStr))

	if err := s.runSyncStep(cmdStr, generateDir); err != nil {
		s.emitLog(fmt.Sprintf("Generating sources failed: %v", err))
		s.setStatus(StatusCrashed, 0)
		return err
	}

	s.emitLog("Sources generated successfully")
	s.setStatus(s.idleStatus(), 0)
	return nil
}

func (s *Service) Stop() error {
	s.mu.Lock()
	if s.status == StatusStopping || s.status == StatusStopped || s.status == StatusGenerating {
		st := s.status
		s.mu.Unlock()
		if st == StatusGenerating {
			return fmt.Errorf("%s is generating sources", s.cfg.Name)
		}
		return fmt.Errorf("%s is not running", s.cfg.Name)
	}
	cmd := s.cmd
	pipe := s.pipe
	cancel := s.cancel
	s.status = StatusStopping
	s.mu.Unlock()

	if cmd == nil || cmd.Process == nil {
		s.setStatus(StatusStopped, 0)
		return fmt.Errorf("%s is not running", s.cfg.Name)
	}

	pid := cmd.Process.Pid
	s.emitLog(fmt.Sprintf("Force stopping (PID %d)...", pid))

	kill := exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprintf("%d", pid))
	kill.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	kill.CombinedOutput()

	if cancel != nil {
		cancel()
	}

	cmd.Process.Kill()

	if pipe != nil {
		pipe.Close()
	}

	done := make(chan struct{})
	go func() {
		childKill := exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprintf("%d", pid))
		childKill.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
		childKill.CombinedOutput()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
	}

	s.mu.Lock()
	s.cmd = nil
	s.pipe = nil
	s.cancel = nil
	s.mu.Unlock()
	s.setStatus(StatusStopped, 0)
	return nil
}

// defaultRestartTimeout bounds how long Restart waits for the previous
// process to actually die before giving up, mirroring the MCP
// restart_service tool's default.
const defaultRestartTimeout = 60 * time.Second

// Restart stops the service (force kill of the whole process tree) if it
// owns one, waits for it to actually die and starts it again. Any custom
// working directory set by StartAt is preserved: Stop never touches
// workdirOverride, so the follow-up Start transparently reuses it — unlike
// StartConfigured, which always resets to the configured workdir.
//
// It fails without starting when the service is currently generating
// sources (the caller must wait for that to finish first) or when it does
// not stop within timeout (falls back to defaultRestartTimeout when <= 0).
func (s *Service) Restart(timeout time.Duration) error {
	if timeout <= 0 {
		timeout = defaultRestartTimeout
	}

	if s.Status() == StatusGenerating {
		return fmt.Errorf("%s is generating sources", s.cfg.Name)
	}

	if hasProcess(s.Status()) {
		if err := s.Stop(); err != nil {
			return err
		}
	}

	deadline := time.Now().Add(timeout)
	for hasProcess(s.Status()) {
		if time.Now().After(deadline) {
			return fmt.Errorf("%s did not stop within %s (status: %s); it was not restarted", s.cfg.Name, timeout, s.Status())
		}
		time.Sleep(100 * time.Millisecond)
	}

	return s.Start()
}

func (s *Service) SetProfiles(profiles []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.Profiles = profiles
}

func (s *Service) Profiles() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg.Profiles
}
