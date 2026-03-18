package synthesis

import (
	"errors"
	"testing"
)

// fakeStats is a test implementation of SystemStatsProvider.
type fakeStats struct {
	cpuIdlePct float64
	cpuErr     error
	memGB      float64
	memErr     error
}

func (f fakeStats) CPUIdlePercent() (float64, error) {
	if f.cpuErr != nil {
		return 0, f.cpuErr
	}
	return f.cpuIdlePct, nil
}

func (f fakeStats) AvailableMemoryGB() (float64, error) {
	if f.memErr != nil {
		return 0, f.memErr
	}
	return f.memGB, nil
}

func TestIsIdle_BelowThreshold(t *testing.T) {
	// CPU usage = 100 - 95 = 5%, below limit of 10. Memory = 8GB, above limit of 4GB.
	stats := fakeStats{cpuIdlePct: 95.0, memGB: 8.0}
	detector := newIdleDetectorWithProvider(10, 4, stats)

	if !detector.IsIdle() {
		t.Error("IsIdle() = false, want true (CPU and memory within thresholds)")
	}
}

func TestIsIdle_AboveThreshold(t *testing.T) {
	// CPU usage = 100 - 80 = 20%, above limit of 10.
	stats := fakeStats{cpuIdlePct: 80.0, memGB: 8.0}
	detector := newIdleDetectorWithProvider(10, 4, stats)

	if detector.IsIdle() {
		t.Error("IsIdle() = true, want false (CPU usage above threshold)")
	}
}

func TestIsIdle_MemoryConstrained(t *testing.T) {
	// CPU fine, but memory = 2GB, below limit of 4GB.
	stats := fakeStats{cpuIdlePct: 98.0, memGB: 2.0}
	detector := newIdleDetectorWithProvider(10, 4, stats)

	if detector.IsIdle() {
		t.Error("IsIdle() = true, want false (available memory below threshold)")
	}
}

func TestIsIdle_FailOpenOnCPUError(t *testing.T) {
	// CPU stat unavailable → fail open (treat as idle if memory is OK).
	stats := fakeStats{cpuErr: errors.New("no cpu stat"), memGB: 8.0}
	detector := newIdleDetectorWithProvider(10, 4, stats)

	if !detector.IsIdle() {
		t.Error("IsIdle() = false, want true (fail open on CPU error with adequate memory)")
	}
}

func TestIsIdle_FailOpenOnMemError(t *testing.T) {
	// Memory stat unavailable, CPU is fine → fail open.
	stats := fakeStats{cpuIdlePct: 95.0, memErr: errors.New("no mem stat")}
	detector := newIdleDetectorWithProvider(10, 4, stats)

	if !detector.IsIdle() {
		t.Error("IsIdle() = false, want true (fail open on memory error with low CPU)")
	}
}

func TestIsIdle_BothErrorsFailOpen(t *testing.T) {
	// Both stats unavailable → fail open.
	stats := fakeStats{
		cpuErr: errors.New("no cpu"),
		memErr: errors.New("no mem"),
	}
	detector := newIdleDetectorWithProvider(10, 4, stats)

	if !detector.IsIdle() {
		t.Error("IsIdle() = false, want true (fail open when both stats unavailable)")
	}
}
