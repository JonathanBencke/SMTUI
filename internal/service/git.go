package service

import (
	"os/exec"
	"strings"
	"syscall"
)

// gitBranch returns the current branch name of the git repository at workdir,
// or "" when workdir is not inside a git repository (or git is unavailable).
// A detached HEAD returns the short commit hash instead of the literal
// string "HEAD", which is more useful at a glance.
func gitBranch(workdir string) string {
	if workdir == "" {
		return ""
	}

	out, err := runGit(workdir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return ""
	}

	branch := strings.TrimSpace(out)
	if branch != "HEAD" {
		return branch
	}

	// Detached HEAD: fall back to the short commit hash.
	short, err := runGit(workdir, "rev-parse", "--short", "HEAD")
	if err != nil {
		return branch
	}
	return strings.TrimSpace(short)
}

// runGit runs a git subcommand in dir and returns its trimmed stdout.
func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}
