package service

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JonathanBencke/ServiceManagerTUI/internal/config"
)

// initTestRepo creates a throwaway git repository at dir, with a single
// commit on the given branch name. It skips the test when git is not
// available in PATH, since these tests exercise the real git binary.
func initTestRepo(t *testing.T, dir, branch string) {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	run("init", "--quiet", "-b", branch)
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", "file.txt")
	run("commit", "--quiet", "-m", "initial")
}

func TestGitBranch_ReturnsCurrentBranch(t *testing.T) {
	dir := t.TempDir()
	initTestRepo(t, dir, "feature/HCMPG-1234-teste")

	got := gitBranch(dir)

	if want := "feature/HCMPG-1234-teste"; got != want {
		t.Errorf("gitBranch() = %q, want %q", got, want)
	}
}

func TestGitBranch_NotAGitRepo_ReturnsEmpty(t *testing.T) {
	dir := t.TempDir()

	if got := gitBranch(dir); got != "" {
		t.Errorf("gitBranch() = %q, want \"\" (not a git repo)", got)
	}
}

func TestGitBranch_EmptyWorkdir_ReturnsEmpty(t *testing.T) {
	if got := gitBranch(""); got != "" {
		t.Errorf("gitBranch(\"\") = %q, want \"\"", got)
	}
}

func TestGitBranch_DetachedHead_ReturnsShortHash(t *testing.T) {
	dir := t.TempDir()
	initTestRepo(t, dir, "main")

	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	fullHash := strings.TrimSpace(string(out))

	checkout := exec.Command("git", "checkout", "--quiet", fullHash)
	checkout.Dir = dir
	if out, err := checkout.CombinedOutput(); err != nil {
		t.Fatalf("git checkout %s: %v\n%s", fullHash, err, out)
	}

	got := gitBranch(dir)

	if got == "" || got == "HEAD" {
		t.Fatalf("gitBranch() = %q, want a short commit hash", got)
	}
	if !strings.HasPrefix(fullHash, got) {
		t.Errorf("gitBranch() = %q, want prefix of full hash %q", got, fullHash)
	}
}

func TestService_GitBranch_CachesAndReflectsWorkdir(t *testing.T) {
	dir := t.TempDir()
	initTestRepo(t, dir, "develop")

	cfg := config.ServiceConfig{Name: "Stub", Workdir: dir}
	s := New(cfg, config.Defaults{}, config.Preset{}, "")

	got := s.GitBranch()
	if want := "develop"; got != want {
		t.Errorf("GitBranch() = %q, want %q", got, want)
	}

	// Segunda chamada deve vir do cache (mesmo resultado, sem exigir git de novo).
	if got2 := s.GitBranch(); got2 != got {
		t.Errorf("GitBranch() segunda chamada = %q, want %q (cache)", got2, got)
	}
}
