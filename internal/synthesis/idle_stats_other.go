//go:build !darwin

package synthesis

// defaultStatsProvider is a stub for non-macOS platforms.
// It reports the system as NOT idle (high CPU, low memory) so the deep
// enrichment worker only fires at the scheduled hour, never on idle triggers.
// This prevents continuous LLM load on platforms where we cannot measure
// actual system utilization.
type defaultStatsProvider struct{}

func (defaultStatsProvider) CPUIdlePercent() (float64, error) {
	// Report 100% CPU usage so IsIdle() returns false.
	return 0, nil
}

func (defaultStatsProvider) AvailableMemoryGB() (float64, error) {
	// Report 0 GB available so IsIdle() returns false.
	return 0, nil
}
