package service

import (
	"os"
	"testing"
)

func TestCollectTreeStats_Self(t *testing.T) {
	pid := uint32(os.Getpid())
	mem, cpu := collectTreeStats(pid)
	if mem <= 0 {
		t.Errorf("expected mem > 0 for self pid %d, got %d", pid, mem)
	}
	if cpu < 0 {
		t.Errorf("cpu seconds negative: %f", cpu)
	}
	t.Logf("self PID %d: mem=%d bytes, cpu=%.4fs", pid, mem, cpu)
}

func BenchmarkGetStats_Win32(b *testing.B) {
	pid := uint32(os.Getpid())
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		collectTreeStats(pid)
	}
}
