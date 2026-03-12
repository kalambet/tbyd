package api

import (
	"fmt"
	"io"
	"log/slog"
	"sync"
)

const onboardingMessage = "tbyd can store your interactions locally for improved context retrieval.\n" +
	"This data never leaves your machine. Enable with: tbyd config set storage.save_interactions true\n"

// OnboardingConfig is the minimal interface needed by OnboardingNotifier to
// check and persist the onboarding state.
type OnboardingConfig interface {
	// SaveInteractionsExplicitlySet reports whether storage.save_interactions
	// has been explicitly stored in the config backend.
	SaveInteractionsExplicitlySet() (bool, error)
	// OnboardingShown reports whether the onboarding prompt has already been shown.
	OnboardingShown() bool
	// MarkOnboardingShown persists storage.onboarding_shown = true.
	MarkOnboardingShown() error
}

// OnboardingNotifier prints the interaction storage onboarding message at most
// once: on the first request when save_interactions has never been explicitly
// configured. Thread-safe.
type OnboardingNotifier struct {
	cfg  OnboardingConfig
	once sync.Once
}

// NewOnboardingNotifier creates a notifier backed by cfg. Pass nil to disable
// onboarding entirely (useful in tests that do not exercise this path).
func NewOnboardingNotifier(cfg OnboardingConfig) *OnboardingNotifier {
	return &OnboardingNotifier{cfg: cfg}
}

// Notify prints the onboarding message to w if the conditions are met.
// It is safe to call from multiple goroutines; the check-and-print is
// performed at most once per process lifetime. Errors writing to w or
// persisting the flag are silently ignored so they never block a request.
func (n *OnboardingNotifier) Notify(w io.Writer) {
	if n == nil || n.cfg == nil {
		return
	}
	n.once.Do(func() {
		// Skip if save_interactions was explicitly configured.
		set, err := n.cfg.SaveInteractionsExplicitlySet()
		if err != nil {
			slog.Warn("onboarding: could not check save_interactions config; showing prompt anyway", "error", err)
		}
		if set {
			return
		}
		// Skip if the prompt has already been shown in a previous run.
		if n.cfg.OnboardingShown() {
			return
		}
		fmt.Fprint(w, onboardingMessage)
		// Best-effort: persist the flag. Ignore errors.
		_ = n.cfg.MarkOnboardingShown()
	})
}
