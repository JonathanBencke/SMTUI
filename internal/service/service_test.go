package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JonathanBencke/ServiceManagerTUI/internal/config"
)

func javaMavenPreset() config.Preset {
	return config.Preset{
		Build: "mvn -pl {{.Modules}} -am install -DskipTests",
		Run:   "mvn -pl impl compile org.codehaus.mojo:exec-maven-plugin:3.2.0:java -Dexec.mainClass={{.MainClass}} -Dexec.classpathScope=runtime{{if .Profiles}} -Dexec.args=--spring.profiles.active={{.Profiles}}{{end}}",
	}
}

func TestResolveCommands_BuildAndRunWithProfiles(t *testing.T) {
	cfg := config.ServiceConfig{
		Name:      "Database",
		JavaHome:  `C:\JDK11`,
		Modules:   []string{"client", "server", "schemanameprovider", "migration"},
		MainClass: "org.example.DatabaseServer",
		Profiles:  []string{"dev"},
	}
	s := New(cfg, config.Defaults{}, javaMavenPreset(), "")

	build, err := s.resolveCommand("build")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	wantBuild := "mvn -pl client,server,schemanameprovider,migration -am install -DskipTests"
	if build != wantBuild {
		t.Errorf("build = %q\n want %q", build, wantBuild)
	}

	run, err := s.resolveCommand("run")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	wantRun := "mvn -pl impl compile org.codehaus.mojo:exec-maven-plugin:3.2.0:java -Dexec.mainClass=org.example.DatabaseServer -Dexec.classpathScope=runtime -Dexec.args=--spring.profiles.active=dev"
	if run != wantRun {
		t.Errorf("run = %q\n want %q", run, wantRun)
	}

	parts, err := shellSplit(run)
	if err != nil {
		t.Fatalf("split run: %v", err)
	}
	if parts[0] != "mvn" {
		t.Errorf("parts[0] = %q, want mvn", parts[0])
	}
	if got, want := parts[len(parts)-1], "-Dexec.args=--spring.profiles.active=dev"; got != want {
		t.Errorf("last part = %q, want %q", got, want)
	}
}

func TestResolveCommands_RunWithoutProfiles(t *testing.T) {
	cfg := config.ServiceConfig{
		Name:      "Benefits",
		Modules:   []string{"client", "server"},
		MainClass: "org.example.BenefitsServer",
		Profiles:  nil,
	}
	s := New(cfg, config.Defaults{}, javaMavenPreset(), "")

	run, err := s.resolveCommand("run")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	wantRun := "mvn -pl impl compile org.codehaus.mojo:exec-maven-plugin:3.2.0:java -Dexec.mainClass=org.example.BenefitsServer -Dexec.classpathScope=runtime"
	if run != wantRun {
		t.Errorf("run = %q\n want %q", run, wantRun)
	}
}

func TestResolveCommands_ServiceOverridesPreset(t *testing.T) {
	cfg := config.ServiceConfig{
		Name:       "web",
		RunCommand: "node {{.MainClass}}",
		MainClass:  "server.js",
	}
	s := New(cfg, config.Defaults{}, javaMavenPreset(), "")

	run, err := s.resolveCommand("run")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if run != "node server.js" {
		t.Errorf("run = %q, want %q", run, "node server.js")
	}
}

func TestResolveCommands_CustomVarFromServiceVars(t *testing.T) {
	preset := config.Preset{Build: `cmd /c "copy /Y {{.IntegrationProperties}} integration.properties"`}
	cfg := config.ServiceConfig{
		Name: "hcm-integration",
		Vars: map[string]string{"IntegrationProperties": `C:\cfg\integration.properties`},
	}
	s := New(cfg, config.Defaults{}, preset, "")

	build, err := s.resolveCommand("build")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	want := `cmd /c "copy /Y C:\cfg\integration.properties integration.properties"`
	if build != want {
		t.Errorf("build = %q\n want %q", build, want)
	}
}

func TestResolveCommands_NoCommandError(t *testing.T) {
	cfg := config.ServiceConfig{Name: "x"}
	s := New(cfg, config.Defaults{}, config.Preset{}, "")

	if _, err := s.resolveCommand("build"); err == nil {
		t.Error("expected error when no build command is defined")
	}
	if _, err := s.resolveCommand("run"); err == nil {
		t.Error("expected error when no run command is defined")
	}
}

func TestShellSplit(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"mvn -pl a,b -am install", []string{"mvn", "-pl", "a,b", "-am", "install"}},
		{`echo "hello world"`, []string{"echo", "hello world"}},
		{`echo 'a b' c`, []string{"echo", "a b", "c"}},
		{"single", []string{"single"}},
	}
	for _, c := range cases {
		got, err := shellSplit(c.in)
		if err != nil {
			t.Errorf("shellSplit(%q) error: %v", c.in, err)
			continue
		}
		if len(got) != len(c.want) {
			t.Errorf("shellSplit(%q) = %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("shellSplit(%q)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
			}
		}
	}

	if _, err := shellSplit(`echo "unterminated`); err == nil {
		t.Error("expected error for unterminated quote")
	}
	if _, err := shellSplit(""); err == nil {
		t.Error("expected error for empty command")
	}
}

func TestDedupEnv_LastWins(t *testing.T) {
	env := []string{
		"PATH=C:\\windows",
		"FOO=1",
		"PATH=D:\\tools",
		"Bar=2",
		"bar=3",
	}
	got := dedupEnv(env)
	m := map[string]string{}
	for _, e := range got {
		k, v, _ := splitEq(e)
		m[k] = v
	}
	if m["PATH"] != "D:\\tools" {
		t.Errorf("PATH = %q, want D:\\tools", m["PATH"])
	}
	if m["FOO"] != "1" {
		t.Errorf("FOO = %q, want 1", m["FOO"])
	}
	barVal := ""
	for k, v := range m {
		if strings.EqualFold(k, "bar") {
			barVal = v
		}
	}
	if barVal != "3" {
		t.Errorf("BAR (case-insensitive) = %q, want 3 (last wins)", barVal)
	}
}

func TestFindSdlRoot_FoundInParentDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.sdl"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	workdir := filepath.Join(root, "java", "impl")
	if err := os.MkdirAll(workdir, 0755); err != nil {
		t.Fatal(err)
	}

	got, ok := findSdlRoot(workdir)

	if !ok {
		t.Fatal("findSdlRoot() ok = false, want true")
	}
	if got != root {
		t.Errorf("findSdlRoot() = %q, want %q", got, root)
	}
}

func TestFindSdlRoot_FoundWithDomainNamedSdlFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "career.sdl"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	workdir := filepath.Join(root, "java", "impl")
	if err := os.MkdirAll(workdir, 0755); err != nil {
		t.Fatal(err)
	}

	got, ok := findSdlRoot(workdir)

	if !ok {
		t.Fatal("findSdlRoot() ok = false, want true (career.sdl should match *.sdl)")
	}
	if got != root {
		t.Errorf("findSdlRoot() = %q, want %q", got, root)
	}
}

func TestFindSdlRoot_FoundInWorkdirItself(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.sdl"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	got, ok := findSdlRoot(root)

	if !ok {
		t.Fatal("findSdlRoot() ok = false, want true")
	}
	if got != root {
		t.Errorf("findSdlRoot() = %q, want %q", got, root)
	}
}

func TestFindSdlRoot_NotFoundWithinDepth(t *testing.T) {
	root := t.TempDir()
	workdir := filepath.Join(root, "a", "b", "c", "d", "e", "f", "g")
	if err := os.MkdirAll(workdir, 0755); err != nil {
		t.Fatal(err)
	}

	_, ok := findSdlRoot(workdir)

	if ok {
		t.Error("findSdlRoot() ok = true, want false (no main.sdl anywhere)")
	}
}

func TestSdlGenerateCommand_DefaultWhenPresetEmpty(t *testing.T) {
	s := New(config.ServiceConfig{Name: "x"}, config.Defaults{}, config.Preset{}, "")

	if got := s.sdlGenerateCommand(); got != defaultSdlGenerateCommand {
		t.Errorf("sdlGenerateCommand() = %q, want %q", got, defaultSdlGenerateCommand)
	}
}

func TestSdlGenerateCommand_CustomFromPreset(t *testing.T) {
	preset := config.Preset{SdlGenerateCommand: "mvn -pl impl clean generate-sources"}
	s := New(config.ServiceConfig{Name: "x"}, config.Defaults{}, preset, "")

	if got := s.sdlGenerateCommand(); got != preset.SdlGenerateCommand {
		t.Errorf("sdlGenerateCommand() = %q, want %q", got, preset.SdlGenerateCommand)
	}
}

func waitForStatus(t *testing.T, s *Service, want Status, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if s.Status() == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for status %q, last status = %q, logs:\n%s", want, s.Status(), strings.Join(s.Logs(), "\n"))
}

func TestStart_BuildAndRunOnly(t *testing.T) {
	workdir := t.TempDir()
	preset := config.Preset{
		Build: "cmd /c echo building",
		Run:   "cmd /c echo running",
	}
	s := New(config.ServiceConfig{Name: "svc", Workdir: workdir}, config.Defaults{}, preset, "")

	if err := s.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	waitForStatus(t, s, StatusStopped, 5*time.Second)

	logs := strings.Join(s.Logs(), "\n")
	if strings.Contains(logs, "Generating sources") || strings.Contains(logs, noSdlRootMessage) {
		t.Errorf("start must not deal with generate-sources at all:\n%s", logs)
	}
	if !strings.Contains(logs, "[1/2] Building") {
		t.Errorf("logs should contain [1/2] Building step:\n%s", logs)
	}
	if !strings.Contains(logs, "[2/2] Starting") {
		t.Errorf("logs should contain [2/2] Starting step:\n%s", logs)
	}
}

func TestStart_SdlRootDetected_DoesNotGenerateSources(t *testing.T) {
	_, workdir := sdlProject(t)
	preset := config.Preset{
		Build:              "cmd /c echo building",
		Run:                "cmd /c echo running",
		SdlGenerateCommand: "cmd /c echo should-not-generate",
	}
	s := New(config.ServiceConfig{Name: "svc", Workdir: workdir}, config.Defaults{}, preset, "")

	if err := s.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	waitForStatus(t, s, StatusStopped, 5*time.Second)

	logs := strings.Join(s.Logs(), "\n")
	if strings.Contains(logs, "should-not-generate") || strings.Contains(logs, "Generating sources") {
		t.Errorf("generate-sources must not run on start, even inside an SDL project:\n%s", logs)
	}
	if !strings.Contains(logs, "[1/2] Building") {
		t.Errorf("logs should contain [1/2] Building step:\n%s", logs)
	}
	if !strings.Contains(logs, "[2/2] Starting") {
		t.Errorf("logs should contain [2/2] Starting step:\n%s", logs)
	}
}

func TestStart_RunOnly_SingleStep(t *testing.T) {
	_, workdir := sdlProject(t)
	preset := config.Preset{
		Run:                "cmd /c echo running",
		SdlGenerateCommand: "cmd /c echo should-not-generate",
	}
	s := New(config.ServiceConfig{Name: "svc", Workdir: workdir}, config.Defaults{}, preset, "")

	if err := s.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	waitForStatus(t, s, StatusStopped, 5*time.Second)

	logs := strings.Join(s.Logs(), "\n")
	if !strings.Contains(logs, "[1/1] Starting") {
		t.Errorf("logs should contain [1/1] Starting step:\n%s", logs)
	}
	if strings.Contains(logs, "Building") || strings.Contains(logs, "should-not-generate") {
		t.Errorf("run-only service should have no other step:\n%s", logs)
	}
}

func TestStart_BuildFailure_AbortsBeforeRun(t *testing.T) {
	workdir := t.TempDir()
	preset := config.Preset{
		Build: "cmd /c exit 1",
		Run:   "cmd /c echo should-not-run-run",
	}
	s := New(config.ServiceConfig{Name: "svc", Workdir: workdir}, config.Defaults{}, preset, "")

	err := s.Start()

	if err == nil {
		t.Fatal("Start() error = nil, want error from failed build step")
	}
	if got := s.Status(); got != StatusCrashed {
		t.Errorf("Status() = %q, want %q", got, StatusCrashed)
	}
	logs := strings.Join(s.Logs(), "\n")
	if strings.Contains(logs, "should-not-run-run") {
		t.Errorf("run should not have executed after a build failure:\n%s", logs)
	}
	if !strings.Contains(logs, "Building failed") {
		t.Errorf("logs should mention the build failure:\n%s", logs)
	}
}

func sdlProject(t *testing.T) (root, workdir string) {
	t.Helper()
	root = t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.sdl"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	workdir = filepath.Join(root, "java", "impl")
	if err := os.MkdirAll(workdir, 0755); err != nil {
		t.Fatal(err)
	}
	return root, workdir
}

func TestGenerateSources_SdlRootDetected_RunsOnlyGenerateStep(t *testing.T) {
	_, workdir := sdlProject(t)
	preset := config.Preset{
		Build:              "cmd /c echo should-not-run-build",
		Run:                "cmd /c echo should-not-run-run",
		SdlGenerateCommand: "cmd /c echo generating-standalone",
	}
	s := New(config.ServiceConfig{Name: "svc", Workdir: workdir}, config.Defaults{}, preset, "")

	var seen []Status
	s.SetCallbacks(nil, func(name string, status Status, pid int) {
		seen = append(seen, status)
	})

	if err := s.GenerateSources(); err != nil {
		t.Fatalf("GenerateSources() error = %v", err)
	}

	if got := s.Status(); got != StatusIdle {
		t.Errorf("Status() = %q, want %q (service never ran)", got, StatusIdle)
	}
	if len(seen) == 0 || seen[0] != StatusGenerating {
		t.Errorf("status transitions = %v, want %q first", seen, StatusGenerating)
	}
	logs := strings.Join(s.Logs(), "\n")
	if !strings.Contains(logs, "generating-standalone") {
		t.Errorf("logs should contain the generate-sources output:\n%s", logs)
	}
	if !strings.Contains(logs, "Sources generated successfully") {
		t.Errorf("logs should confirm success:\n%s", logs)
	}
	if strings.Contains(logs, "should-not-run-build") || strings.Contains(logs, "should-not-run-run") {
		t.Errorf("build/run must not execute on GenerateSources():\n%s", logs)
	}
}

func TestGenerateSources_NoSdlRoot_SkipsAndKeepsStatus(t *testing.T) {
	workdir := t.TempDir()
	preset := config.Preset{SdlGenerateCommand: "cmd /c echo should-not-run-generate"}
	s := New(config.ServiceConfig{Name: "svc", Workdir: workdir}, config.Defaults{}, preset, "")

	if err := s.GenerateSources(); err != nil {
		t.Fatalf("GenerateSources() error = %v, want nil (skip is not an error)", err)
	}

	if got := s.Status(); got != StatusIdle {
		t.Errorf("Status() = %q, want %q (status must be preserved)", got, StatusIdle)
	}
	logs := strings.Join(s.Logs(), "\n")
	if !strings.Contains(logs, noSdlRootMessage) {
		t.Errorf("logs should mention the skip:\n%s", logs)
	}
	if strings.Contains(logs, "should-not-run-generate") {
		t.Errorf("generate command must not run without an .sdl file:\n%s", logs)
	}
}

func TestGenerateSources_ExplicitWorkdir_RunsWithoutSdlRoot(t *testing.T) {
	workdir := t.TempDir()
	preset := config.Preset{
		SdlGenerateCommand: "cmd /c echo generating-in-workdir",
		GenerateInWorkdir:  true,
	}
	s := New(config.ServiceConfig{Name: "svc", Workdir: workdir}, config.Defaults{}, preset, "")

	if err := s.GenerateSources(); err != nil {
		t.Fatalf("GenerateSources() error = %v", err)
	}

	logs := strings.Join(s.Logs(), "\n")
	if !strings.Contains(logs, "generating-in-workdir") {
		t.Errorf("logs should contain the explicit generation output:\n%s", logs)
	}
	if strings.Contains(logs, noSdlRootMessage) {
		t.Errorf("logs should not skip an explicit generation command:\n%s", logs)
	}
}

func TestGenerateSources_ExpandsCommandTemplate(t *testing.T) {
	workdir := t.TempDir()
	preset := config.Preset{
		SdlGenerateCommand: "cmd /c echo {{.Marker}}",
		GenerateInWorkdir:  true,
	}
	s := New(config.ServiceConfig{
		Name:    "svc",
		Workdir: workdir,
		Vars:    map[string]string{"Marker": "template-expanded"},
	}, config.Defaults{}, preset, "")

	if err := s.GenerateSources(); err != nil {
		t.Fatalf("GenerateSources() error = %v", err)
	}

	if logs := strings.Join(s.Logs(), "\n"); !strings.Contains(logs, "template-expanded") {
		t.Errorf("logs should contain the expanded template value:\n%s", logs)
	}
}

func TestGenerateSources_CommandFailure_SetsCrashed(t *testing.T) {
	_, workdir := sdlProject(t)
	preset := config.Preset{SdlGenerateCommand: "cmd /c exit 1"}
	s := New(config.ServiceConfig{Name: "svc", Workdir: workdir}, config.Defaults{}, preset, "")

	if err := s.GenerateSources(); err == nil {
		t.Fatal("GenerateSources() error = nil, want error from failed command")
	}

	if got := s.Status(); got != StatusCrashed {
		t.Errorf("Status() = %q, want %q", got, StatusCrashed)
	}
	if logs := strings.Join(s.Logs(), "\n"); !strings.Contains(logs, "Generating sources failed") {
		t.Errorf("logs should mention the failure:\n%s", logs)
	}
}

func TestGenerateSources_AlreadyGenerating_IsRefused(t *testing.T) {
	_, workdir := sdlProject(t)
	s := New(config.ServiceConfig{Name: "svc", Workdir: workdir}, config.Defaults{}, config.Preset{}, "")
	s.setStatus(StatusGenerating, 0)

	err := s.GenerateSources()

	if err == nil {
		t.Fatal("GenerateSources() error = nil, want error while another generation is in progress")
	}
	if got := s.Status(); got != StatusGenerating {
		t.Errorf("Status() = %q, want %q", got, StatusGenerating)
	}
}

func TestGenerateSources_StopsRunningServiceFirst(t *testing.T) {
	_, workdir := sdlProject(t)
	preset := config.Preset{
		Run:                "cmd /c ping -n 30 127.0.0.1",
		SdlGenerateCommand: "cmd /c echo generating-standalone",
	}
	s := New(config.ServiceConfig{Name: "svc", Workdir: workdir}, config.Defaults{}, preset, "")

	if err := s.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	waitForStatus(t, s, StatusRunning, 10*time.Second)

	if err := s.GenerateSources(); err != nil {
		t.Fatalf("GenerateSources() error = %v", err)
	}

	if got := s.Status(); got != StatusStopped {
		t.Errorf("Status() = %q, want %q (service already ran, not restarted)", got, StatusStopped)
	}
	logs := strings.Join(s.Logs(), "\n")
	if !strings.Contains(logs, "Stopping service before generate-sources") {
		t.Errorf("logs should mention the service was stopped first:\n%s", logs)
	}
	if !strings.Contains(logs, "generating-standalone") {
		t.Errorf("logs should contain the generate-sources output:\n%s", logs)
	}
}

func TestRestart_StopsAndStartsAgainWithNewPID(t *testing.T) {
	workdir := t.TempDir()
	preset := config.Preset{Run: "cmd /c ping -n 30 127.0.0.1"}
	s := New(config.ServiceConfig{Name: "svc", Workdir: workdir}, config.Defaults{}, preset, "")

	if err := s.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	waitForStatus(t, s, StatusRunning, 10*time.Second)
	firstPID := s.PID()

	if err := s.Restart(5 * time.Second); err != nil {
		t.Fatalf("Restart() error = %v", err)
	}
	defer s.Stop() // libera o handle do workdir (t.TempDir()) antes do cleanup do teste

	waitForStatus(t, s, StatusRunning, 10*time.Second)
	if got := s.PID(); got == firstPID || got <= 0 {
		t.Errorf("PID() after restart = %d, want a new PID different from %d", got, firstPID)
	}
}

func TestRestart_PreservesWorkdirOverride(t *testing.T) {
	configured := t.TempDir()
	override := t.TempDir()
	preset := config.Preset{Run: "cmd /c ping -n 30 127.0.0.1"}
	s := New(config.ServiceConfig{Name: "svc", Workdir: configured}, config.Defaults{}, preset, "")

	if err := s.StartAt(override); err != nil {
		t.Fatalf("StartAt() error = %v", err)
	}
	waitForStatus(t, s, StatusRunning, 10*time.Second)

	if err := s.Restart(5 * time.Second); err != nil {
		t.Fatalf("Restart() error = %v", err)
	}
	defer s.Stop() // libera o handle do workdir (t.TempDir()) antes do cleanup do teste
	waitForStatus(t, s, StatusRunning, 10*time.Second)

	if got := s.Workdir(); !strings.EqualFold(got, override) {
		t.Errorf("Workdir() after restart = %q, want the preserved override %q", got, override)
	}
}

func TestRestart_RefusedWhileGenerating(t *testing.T) {
	_, workdir := sdlProject(t)
	preset := config.Preset{Run: "cmd /c echo running"}
	s := New(config.ServiceConfig{Name: "svc", Workdir: workdir}, config.Defaults{}, preset, "")
	s.setStatus(StatusGenerating, 0)

	err := s.Restart(2 * time.Second)

	if err == nil {
		t.Fatal("Restart() error = nil, want error while generating sources")
	}
	if got := s.Status(); got != StatusGenerating {
		t.Errorf("Status() = %q, want unchanged %q", got, StatusGenerating)
	}
}

func TestRestart_IdleServiceJustStarts(t *testing.T) {
	workdir := t.TempDir()
	preset := config.Preset{Run: "cmd /c echo running"}
	s := New(config.ServiceConfig{Name: "svc", Workdir: workdir}, config.Defaults{}, preset, "")

	if err := s.Restart(5 * time.Second); err != nil {
		t.Fatalf("Restart() on an idle service error = %v", err)
	}

	waitForStatus(t, s, StatusStopped, 5*time.Second)
}

func TestStart_RefusedWhileGenerating(t *testing.T) {
	_, workdir := sdlProject(t)
	preset := config.Preset{Run: "cmd /c echo running"}
	s := New(config.ServiceConfig{Name: "svc", Workdir: workdir}, config.Defaults{}, preset, "")
	s.setStatus(StatusGenerating, 0)

	err := s.Start()

	if err == nil {
		t.Fatal("Start() error = nil, want error while generating sources")
	}
	if got := s.Status(); got != StatusGenerating {
		t.Errorf("Status() = %q, want %q (Start must not change it)", got, StatusGenerating)
	}
}

// markerDir creates a directory containing a uniquely named file, so a command
// running there can be identified by listing the directory (`cmd /c dir /b`).
// Comparing marker names is more reliable than comparing printed paths, which
// Windows may render in short (8.3) form.
func markerDir(t *testing.T, dir, marker string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, marker), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

const listDirCommand = "cmd /c dir /b"

func TestStartAt_UsesCustomWorkdir(t *testing.T) {
	configured := markerDir(t, t.TempDir(), "configured-marker.txt")
	worktree := markerDir(t, t.TempDir(), "worktree-marker.txt")
	preset := config.Preset{
		Build: listDirCommand,
		Run:   listDirCommand,
	}
	s := New(config.ServiceConfig{Name: "svc", Workdir: configured}, config.Defaults{}, preset, "")

	if err := s.StartAt(worktree); err != nil {
		t.Fatalf("StartAt() error = %v", err)
	}
	waitForStatus(t, s, StatusStopped, 5*time.Second)

	if got := s.Workdir(); got != worktree {
		t.Errorf("Workdir() = %q, want %q", got, worktree)
	}
	if got := s.WorkdirOverride(); got != worktree {
		t.Errorf("WorkdirOverride() = %q, want %q", got, worktree)
	}
	if got := s.ConfiguredWorkdir(); got != configured {
		t.Errorf("ConfiguredWorkdir() = %q, want %q", got, configured)
	}
	logs := strings.Join(s.Logs(), "\n")
	if !strings.Contains(logs, "worktree-marker.txt") {
		t.Errorf("build/run should have executed in the worktree:\n%s", logs)
	}
	if strings.Contains(logs, "configured-marker.txt") {
		t.Errorf("no command should have run in the configured workdir:\n%s", logs)
	}
}

func TestStartAt_OverrideStaysForLaterStart(t *testing.T) {
	configured := markerDir(t, t.TempDir(), "configured-marker.txt")
	worktree := markerDir(t, t.TempDir(), "worktree-marker.txt")
	preset := config.Preset{Run: listDirCommand}
	s := New(config.ServiceConfig{Name: "svc", Workdir: configured}, config.Defaults{}, preset, "")

	if err := s.StartAt(worktree); err != nil {
		t.Fatalf("StartAt() error = %v", err)
	}
	waitForStatus(t, s, StatusStopped, 5*time.Second)

	if err := s.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	waitForStatus(t, s, StatusStopped, 5*time.Second)

	if got := s.Workdir(); got != worktree {
		t.Errorf("Workdir() = %q, want the sticky override %q", got, worktree)
	}
	if logs := strings.Join(s.Logs(), "\n"); strings.Contains(logs, "configured-marker.txt") {
		t.Errorf("the plain Start() should have reused the override:\n%s", logs)
	}
}

func TestStartAt_ConfiguredWorkdirClearsOverride(t *testing.T) {
	configured := markerDir(t, t.TempDir(), "configured-marker.txt")
	worktree := markerDir(t, t.TempDir(), "worktree-marker.txt")
	preset := config.Preset{Run: listDirCommand}
	s := New(config.ServiceConfig{Name: "svc", Workdir: configured}, config.Defaults{}, preset, "")

	if err := s.StartAt(worktree); err != nil {
		t.Fatalf("StartAt(worktree) error = %v", err)
	}
	waitForStatus(t, s, StatusStopped, 5*time.Second)

	if err := s.StartAt(configured); err != nil {
		t.Fatalf("StartAt(configured) error = %v", err)
	}
	waitForStatus(t, s, StatusStopped, 5*time.Second)

	if got := s.WorkdirOverride(); got != "" {
		t.Errorf("WorkdirOverride() = %q, want empty after restoring the configured workdir", got)
	}
	if got := s.Workdir(); got != configured {
		t.Errorf("Workdir() = %q, want %q", got, configured)
	}
	if logs := strings.Join(s.Logs(), "\n"); !strings.Contains(logs, "configured-marker.txt") {
		t.Errorf("the last start should have run in the configured workdir:\n%s", logs)
	}
}

func TestStartAt_GenerateSourcesUsesOverriddenWorktree(t *testing.T) {
	configuredRoot, configured := sdlProject(t)
	markerDir(t, configuredRoot, "configured-root-marker.txt")
	worktreeRoot, worktree := sdlProject(t)
	markerDir(t, worktreeRoot, "worktree-root-marker.txt")
	preset := config.Preset{
		Run:                "cmd /c echo running",
		SdlGenerateCommand: listDirCommand,
	}
	s := New(config.ServiceConfig{Name: "svc", Workdir: configured}, config.Defaults{}, preset, "")

	if err := s.StartAt(worktree); err != nil {
		t.Fatalf("StartAt() error = %v", err)
	}
	waitForStatus(t, s, StatusStopped, 5*time.Second)

	if err := s.GenerateSources(); err != nil {
		t.Fatalf("GenerateSources() error = %v", err)
	}

	logs := strings.Join(s.Logs(), "\n")
	if !strings.Contains(logs, "worktree-root-marker.txt") {
		t.Errorf("generate-sources should run in the worktree SDL root:\n%s", logs)
	}
	if strings.Contains(logs, "configured-root-marker.txt") {
		t.Errorf("generate-sources must not run in the configured SDL root:\n%s", logs)
	}
}

func TestStartAt_InvalidWorkdirIsRejected(t *testing.T) {
	configured := t.TempDir()
	file := filepath.Join(configured, "not-a-dir.txt")
	if err := os.WriteFile(file, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	preset := config.Preset{Run: "cmd /c echo should-not-run"}

	cases := map[string]string{
		"empty":     "   ",
		"missing":   filepath.Join(configured, "nope"),
		"not-a-dir": file,
	}
	for label, dir := range cases {
		s := New(config.ServiceConfig{Name: "svc", Workdir: configured}, config.Defaults{}, preset, "")

		err := s.StartAt(dir)

		if err == nil {
			t.Errorf("StartAt(%s) error = nil, want error", label)
		}
		if got := s.Status(); got != StatusIdle {
			t.Errorf("StartAt(%s): Status() = %q, want %q (service must not start)", label, got, StatusIdle)
		}
		if got := s.WorkdirOverride(); got != "" {
			t.Errorf("StartAt(%s): WorkdirOverride() = %q, want empty", label, got)
		}
	}
}

func TestStartAt_RefusedWhileRunning(t *testing.T) {
	configured := t.TempDir()
	worktree := t.TempDir()
	preset := config.Preset{Run: "cmd /c ping -n 30 127.0.0.1"}
	s := New(config.ServiceConfig{Name: "svc", Workdir: configured}, config.Defaults{}, preset, "")

	if err := s.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	waitForStatus(t, s, StatusRunning, 10*time.Second)
	defer s.Stop()

	err := s.StartAt(worktree)

	if err == nil {
		t.Fatal("StartAt() error = nil, want error while the service is running")
	}
	if got := s.Workdir(); got != configured {
		t.Errorf("Workdir() = %q, want %q (override must not apply to a running command)", got, configured)
	}
}

func TestStartConfigured_DropsOverrideFromStartAt(t *testing.T) {
	configured := markerDir(t, t.TempDir(), "configured-marker.txt")
	worktree := markerDir(t, t.TempDir(), "worktree-marker.txt")
	preset := config.Preset{Run: listDirCommand}
	s := New(config.ServiceConfig{Name: "svc", Workdir: configured}, config.Defaults{}, preset, "")

	if err := s.StartAt(worktree); err != nil {
		t.Fatalf("StartAt() error = %v", err)
	}
	waitForStatus(t, s, StatusStopped, 5*time.Second)

	if err := s.StartConfigured(); err != nil {
		t.Fatalf("StartConfigured() error = %v", err)
	}
	waitForStatus(t, s, StatusStopped, 5*time.Second)

	if got := s.WorkdirOverride(); got != "" {
		t.Errorf("WorkdirOverride() = %q, want empty (terminal start must drop it)", got)
	}
	if got := s.Workdir(); got != configured {
		t.Errorf("Workdir() = %q, want %q", got, configured)
	}
	logs := strings.Join(s.Logs(), "\n")
	if !strings.Contains(logs, "configured-marker.txt") {
		t.Errorf("StartConfigured() should have run in the configured workdir:\n%s", logs)
	}
	if !strings.Contains(logs, "Back to the configured workdir") {
		t.Errorf("dropping the override should be logged:\n%s", logs)
	}
}

func TestStartConfigured_NoOverride_IsQuiet(t *testing.T) {
	configured := markerDir(t, t.TempDir(), "configured-marker.txt")
	preset := config.Preset{Run: listDirCommand}
	s := New(config.ServiceConfig{Name: "svc", Workdir: configured}, config.Defaults{}, preset, "")

	if err := s.StartConfigured(); err != nil {
		t.Fatalf("StartConfigured() error = %v", err)
	}
	waitForStatus(t, s, StatusStopped, 5*time.Second)

	logs := strings.Join(s.Logs(), "\n")
	if strings.Contains(logs, "Back to the configured workdir") {
		t.Errorf("nothing to drop, the log should stay quiet:\n%s", logs)
	}
	if !strings.Contains(logs, "configured-marker.txt") {
		t.Errorf("service should have run in the configured workdir:\n%s", logs)
	}
}

func TestIsActive(t *testing.T) {
	active := []Status{StatusRunning, StatusBuilding, StatusGenerating, StatusStopping}
	for _, st := range active {
		if !isActive(st) {
			t.Errorf("isActive(%q) = false, want true", st)
		}
	}
	for _, st := range []Status{StatusIdle, StatusStopped, StatusCrashed} {
		if isActive(st) {
			t.Errorf("isActive(%q) = true, want false", st)
		}
	}
}

func splitEq(e string) (string, string, error) {
	for i := 0; i < len(e); i++ {
		if e[i] == '=' {
			return e[:i], e[i+1:], nil
		}
	}
	return e, "", nil
}
