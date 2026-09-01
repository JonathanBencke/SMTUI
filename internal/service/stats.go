package service

import (
	"context"
	"fmt"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type ProcStats struct {
	MemBytes   int64
	CPUPercent float64
}

// processMemoryCountersEx equivale a PROCESS_MEMORY_COUNTERS_EX do psapi.
type processMemoryCountersEx struct {
	cb                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
	PrivateUsage               uintptr
}

var (
	modPsapi                 = windows.NewLazySystemDLL("psapi.dll")
	procGetProcessMemoryInfo = modPsapi.NewProc("GetProcessMemoryInfo")
)

func getProcessMemoryInfo(handle windows.Handle, mem *processMemoryCountersEx) error {
	r1, _, err := procGetProcessMemoryInfo.Call(
		uintptr(handle),
		uintptr(unsafe.Pointer(mem)),
		unsafe.Sizeof(*mem),
	)
	if r1 == 0 {
		return err
	}
	return nil
}

// GetStats coleta uso de memoria (WorkingSet) e CPU (delta de tempo de processador)
// do processo do servico e de toda a sua arvore de subprocessos, usando Win32 API
// direta (sem spawn de PowerShell). Bem mais rapido e leve que a versao anterior.
func (s *Service) GetStats() ProcStats {
	s.mu.Lock()
	pid := s.pid
	prevCPU := s.prevCPU
	prevTime := s.prevStatsTime
	s.mu.Unlock()

	if pid <= 0 {
		return ProcStats{}
	}

	memBytes, cpuSeconds := collectTreeStats(uint32(pid))

	now := time.Now()
	var cpuPercent float64
	if !prevTime.IsZero() {
		elapsed := now.Sub(prevTime).Seconds()
		if elapsed > 0 {
			cpuPercent = (cpuSeconds - prevCPU) / elapsed / float64(runtime.NumCPU()) * 100
			if cpuPercent < 0 {
				cpuPercent = 0
			}
			if cpuPercent > 100 {
				cpuPercent = 100
			}
		}
	}

	s.mu.Lock()
	s.prevCPU = cpuSeconds
	s.prevStatsTime = now
	s.mu.Unlock()

	return ProcStats{MemBytes: memBytes, CPUPercent: cpuPercent}
}

// SampleStats measures memory and CPU over an explicit window, without touching
// the delta state (prevCPU/prevStatsTime) that GetStats shares with the TUI's
// periodic sampling. A one-shot caller (the MCP get_stats tool) would otherwise
// either read 0% CPU or corrupt the TUI's next reading.
//
// It returns as soon as ctx is done, reporting whatever window elapsed.
func (s *Service) SampleStats(ctx context.Context, window time.Duration) (stats ProcStats, sampled time.Duration) {
	s.mu.Lock()
	pid := s.pid
	s.mu.Unlock()

	if pid <= 0 {
		return ProcStats{}, 0
	}
	if window <= 0 {
		window = 700 * time.Millisecond
	}

	_, cpuStart := collectTreeStats(uint32(pid))
	start := time.Now()

	timer := time.NewTimer(window)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
	}

	memBytes, cpuEnd := collectTreeStats(uint32(pid))
	elapsed := time.Since(start)

	var cpuPercent float64
	if elapsed > 0 {
		cpuPercent = (cpuEnd - cpuStart) / elapsed.Seconds() / float64(runtime.NumCPU()) * 100
	}
	if cpuPercent < 0 {
		cpuPercent = 0
	}
	if cpuPercent > 100 {
		cpuPercent = 100
	}

	return ProcStats{MemBytes: memBytes, CPUPercent: cpuPercent}, elapsed
}

// collectTreeStats soma WorkingSet e tempo de CPU (kernel+user) do PID raiz e de
// todos os seus descendentes (BFS pela arvore de processos).
func collectTreeStats(rootPID uint32) (memBytes int64, cpuSeconds float64) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0, 0
	}
	defer windows.CloseHandle(snapshot)

	children := make(map[uint32][]uint32)
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return 0, 0
	}
	for {
		parent := entry.ParentProcessID
		children[parent] = append(children[parent], entry.ProcessID)
		entry.Size = uint32(unsafe.Sizeof(entry))
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			break
		}
	}

	visited := make(map[uint32]bool)
	queue := []uint32{rootPID}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		if visited[p] {
			continue
		}
		visited[p] = true

		m, c := readProcCounters(p)
		memBytes += m
		cpuSeconds += c

		for _, child := range children[p] {
			if !visited[child] {
				queue = append(queue, child)
			}
		}
	}
	return memBytes, cpuSeconds
}

// readProcCounters le WorkingSet (memoria) e tempo de CPU (kernel+user) de um PID.
func readProcCounters(pid uint32) (memBytes int64, cpuSeconds float64) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return 0, 0
	}
	defer windows.CloseHandle(h)

	var mem processMemoryCountersEx
	if err := getProcessMemoryInfo(h, &mem); err == nil {
		memBytes = int64(mem.WorkingSetSize)
	}

	var create, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &create, &exit, &kernel, &user); err == nil {
		cpuSeconds = filetimeToSeconds(kernel) + filetimeToSeconds(user)
	}
	return memBytes, cpuSeconds
}

// filetimeToSeconds converte um FILETIME (intervalos de 100ns) para segundos.
func filetimeToSeconds(ft windows.Filetime) float64 {
	n := uint64(ft.HighDateTime)<<32 | uint64(ft.LowDateTime)
	return float64(n) / 1e7
}

func FormatMem(bytes int64) string {
	if bytes <= 0 {
		return ""
	}
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)
	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.1fGB", float64(bytes)/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%dMB", bytes/MB)
	case bytes >= KB:
		return fmt.Sprintf("%dKB", bytes/KB)
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}
