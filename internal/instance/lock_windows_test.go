package instance

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestAcquire_SecondAcquireForSameConfigFails(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "services.toml")
	first, err := Acquire(cfg)
	if err != nil {
		t.Fatalf("first Acquire() error = %v", err)
	}
	defer first.Release()

	second, err := Acquire(strings.ToUpper(cfg))

	if !errors.Is(err, ErrAlreadyRunning) {
		second.Release()
		t.Fatalf("second Acquire() error = %v, want ErrAlreadyRunning", err)
	}
}

func TestAcquire_SucceedsAgainAfterRelease(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "services.toml")
	first, err := Acquire(cfg)
	if err != nil {
		t.Fatalf("first Acquire() error = %v", err)
	}
	first.Release()

	second, err := Acquire(cfg)

	if err != nil {
		t.Fatalf("Acquire() after Release() error = %v, want nil", err)
	}
	second.Release()
}

func TestAcquire_DifferentConfigsDoNotConflict(t *testing.T) {
	dir := t.TempDir()
	a, err := Acquire(filepath.Join(dir, "a.toml"))
	if err != nil {
		t.Fatalf("Acquire(a) error = %v", err)
	}
	defer a.Release()

	b, err := Acquire(filepath.Join(dir, "b.toml"))

	if err != nil {
		t.Fatalf("Acquire(b) error = %v, want nil", err)
	}
	b.Release()
}
