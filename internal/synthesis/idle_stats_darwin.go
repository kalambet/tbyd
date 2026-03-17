//go:build darwin

package synthesis

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// statsTimeout is the maximum time allowed for each OS stats command.
const statsTimeout = 10 * time.Second

// defaultStatsProvider is the macOS implementation of SystemStatsProvider.
type defaultStatsProvider struct{}

// CPUIdlePercent parses idle CPU percentage from `top -l 1 -n 0 -stats cpu`.
// Returns the idle percentage (0–100). Errors are propagated to the caller who
// treats them as "idle" (fail open).
func (defaultStatsProvider) CPUIdlePercent() (float64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), statsTimeout)
	defer cancel()

	// -l 1: log mode (single sample), -n 0: show no processes, -stats cpu: CPU only
	out, err := exec.CommandContext(ctx, "/usr/bin/top", "-l", "1", "-n", "0", "-stats", "cpu").Output()
	if err != nil {
		return 0, fmt.Errorf("running top: %w", err)
	}

	// Look for a line like: "CPU usage: 12.5% user, 8.2% sys, 79.3% idle"
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "CPU usage:") {
			continue
		}
		parts := strings.Split(line, ",")
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if strings.HasSuffix(part, "% idle") {
				numStr := strings.TrimSuffix(part, "% idle")
				numStr = strings.TrimSpace(numStr)
				v, err := strconv.ParseFloat(numStr, 64)
				if err != nil {
					return 0, fmt.Errorf("parsing idle percent %q: %w", numStr, err)
				}
				return v, nil
			}
		}
	}

	return 0, fmt.Errorf("CPU idle percentage not found in top output")
}

// AvailableMemoryGB returns free + inactive memory in GB using vm_stat.
// Returns an error if parsing fails; callers treat errors as "memory OK" (fail open).
func (defaultStatsProvider) AvailableMemoryGB() (float64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), statsTimeout)
	defer cancel()

	// Get page size via sysctl.
	pageSizeOut, err := exec.CommandContext(ctx, "sysctl", "-n", "hw.pagesize").Output()
	if err != nil {
		return 0, fmt.Errorf("running sysctl hw.pagesize: %w", err)
	}
	pageSize, err := strconv.ParseInt(strings.TrimSpace(string(pageSizeOut)), 10, 64)
	if err != nil || pageSize <= 0 {
		return 0, fmt.Errorf("parsing page size %q: %w", string(pageSizeOut), err)
	}

	vmOut, err := exec.CommandContext(ctx, "vm_stat").Output()
	if err != nil {
		return 0, fmt.Errorf("running vm_stat: %w", err)
	}

	var freePages, inactivePages int64
	for _, line := range bytes.Split(vmOut, []byte("\n")) {
		s := strings.TrimSpace(string(line))
		switch {
		case strings.HasPrefix(s, "Pages free:"):
			freePages = parseVMStatPages(s)
		case strings.HasPrefix(s, "Pages inactive:"):
			inactivePages = parseVMStatPages(s)
		}
	}

	availableBytes := (freePages + inactivePages) * pageSize
	availableGB := float64(availableBytes) / (1024 * 1024 * 1024)
	return availableGB, nil
}

// parseVMStatPages extracts the trailing page count from a vm_stat line.
// e.g. "Pages free:          12345." → 12345
func parseVMStatPages(line string) int64 {
	idx := strings.LastIndex(line, ":")
	if idx < 0 {
		return 0
	}
	raw := strings.TrimSpace(line[idx+1:])
	raw = strings.TrimSuffix(raw, ".")
	v, _ := strconv.ParseInt(raw, 10, 64)
	return v
}
