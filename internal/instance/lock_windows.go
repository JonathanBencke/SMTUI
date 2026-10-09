// Package instance guarantees a single smtui process per configuration file.
//
// Each smtui process (TUI or standalone -mcp) owns its own service manager.
// Two of them pointed at the same services.toml cannot see each other's
// processes, so both can start the same service and leave duplicates running.
package instance

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// ErrAlreadyRunning is returned by Acquire when another smtui process already
// manages the same configuration file.
var ErrAlreadyRunning = errors.New("another smtui instance is already managing this configuration")

// mutexNamePrefix scopes the lock to the current Windows session.
const mutexNamePrefix = `Local\SMTUI-`

// mutexHashLength is how many hex characters of the config path hash go into
// the mutex name; enough to make collisions irrelevant.
const mutexHashLength = 16

// Lock is a held single-instance lock. The OS releases it automatically if
// the process dies, so a crash never leaves a stale lock behind.
type Lock struct {
	handle windows.Handle
}

// Acquire takes the single-instance lock for cfgPath. It returns
// ErrAlreadyRunning if another process holds it.
func Acquire(cfgPath string) (*Lock, error) {
	name, err := windows.UTF16PtrFromString(MutexName(cfgPath))
	if err != nil {
		return nil, fmt.Errorf("instance lock name: %w", err)
	}
	handle, err := windows.CreateMutex(nil, false, name)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		windows.CloseHandle(handle)
		return nil, ErrAlreadyRunning
	}
	if err != nil {
		return nil, fmt.Errorf("instance lock: %w", err)
	}
	return &Lock{handle: handle}, nil
}

// Release frees the lock. It is idempotent and safe on a nil receiver.
func (l *Lock) Release() {
	if l == nil || l.handle == 0 {
		return
	}
	windows.CloseHandle(l.handle)
	l.handle = 0
}

// MutexName derives the lock name from the absolute, case-insensitive config
// path, so "services.toml" and "C:\...\SERVICES.TOML" map to the same lock.
func MutexName(cfgPath string) string {
	abs, err := filepath.Abs(cfgPath)
	if err != nil {
		abs = cfgPath
	}
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(abs))))
	return mutexNamePrefix + hex.EncodeToString(sum[:])[:mutexHashLength]
}
