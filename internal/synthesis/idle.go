package synthesis

// SystemStatsProvider abstracts OS-level CPU and memory queries.
// The default implementation uses macOS command-line tools.
// Tests may inject a fake implementation.
type SystemStatsProvider interface {
	// CPUIdlePercent returns the percentage of CPU time that is idle (0–100).
	// An error means "unknown"; callers treat unknown as idle (fail open).
	CPUIdlePercent() (float64, error)

	// AvailableMemoryGB returns free + inactive memory in gigabytes.
	// An error means "unknown"; callers treat unknown as idle (fail open).
	AvailableMemoryGB() (float64, error)
}

// IdleDetector checks whether the system is idle enough for deep enrichment.
type IdleDetector struct {
	cpuMaxPercent int
	memMinGB      int
	stats         SystemStatsProvider
}

// NewIdleDetector creates an IdleDetector using the default macOS stats provider.
func NewIdleDetector(cpuMaxPercent, memMinGB int) *IdleDetector {
	return &IdleDetector{
		cpuMaxPercent: cpuMaxPercent,
		memMinGB:      memMinGB,
		stats:         defaultStatsProvider{},
	}
}

// newIdleDetectorWithProvider creates an IdleDetector with a custom stats provider.
// Used in tests.
func newIdleDetectorWithProvider(cpuMaxPercent, memMinGB int, p SystemStatsProvider) *IdleDetector {
	return &IdleDetector{
		cpuMaxPercent: cpuMaxPercent,
		memMinGB:      memMinGB,
		stats:         p,
	}
}

// IsIdle returns true if CPU usage is below cpuMaxPercent AND available memory
// is above memMinGB. Fails open: if either stat is unavailable the system is
// treated as idle so the deep model eventually runs rather than never running.
func (d *IdleDetector) IsIdle() bool {
	cpuIdle, err := d.stats.CPUIdlePercent()
	if err == nil {
		cpuUsed := 100.0 - cpuIdle
		if int(cpuUsed) >= d.cpuMaxPercent {
			return false
		}
	}
	// err != nil → treat as idle (fail open)

	memGB, err := d.stats.AvailableMemoryGB()
	if err == nil {
		if memGB < float64(d.memMinGB) {
			return false
		}
	}
	// err != nil → treat as idle (fail open)

	return true
}
