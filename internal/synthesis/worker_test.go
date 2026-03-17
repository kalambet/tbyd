package synthesis

import (
	"testing"

	"github.com/kalambet/tbyd/internal/storage"
)

func TestBuildSignalCountDeltas_PositiveAndNegative(t *testing.T) {
	signals := []PreferenceSignal{
		{Type: "positive", Pattern: "concise responses"},
		{Type: "negative", Pattern: "verbose explanations"},
	}

	deltas := buildSignalCountDeltas(signals)

	if len(deltas) != 2 {
		t.Fatalf("expected 2 deltas, got %d", len(deltas))
	}
	if deltas[0].Positive != 1 || deltas[0].Negative != 0 {
		t.Errorf("first delta: pos=%d neg=%d, want pos=1 neg=0", deltas[0].Positive, deltas[0].Negative)
	}
	if deltas[1].Positive != 0 || deltas[1].Negative != 1 {
		t.Errorf("second delta: pos=%d neg=%d, want pos=0 neg=1", deltas[1].Positive, deltas[1].Negative)
	}
}

func TestBuildSignalCountDeltas_NormalizesKey(t *testing.T) {
	signals := []PreferenceSignal{
		{Type: "positive", Pattern: "  Concise Responses  "},
	}

	deltas := buildSignalCountDeltas(signals)

	if len(deltas) != 1 {
		t.Fatalf("expected 1 delta, got %d", len(deltas))
	}
	if deltas[0].PatternKey != "concise responses" {
		t.Errorf("pattern key = %q, want %q", deltas[0].PatternKey, "concise responses")
	}
	if deltas[0].PatternDisplay != "  Concise Responses  " {
		t.Errorf("pattern display = %q, want original casing", deltas[0].PatternDisplay)
	}
}

func TestBuildSignalCountDeltas_SkipsBlankPattern(t *testing.T) {
	signals := []PreferenceSignal{
		{Type: "positive", Pattern: ""},
		{Type: "positive", Pattern: "   "},
		{Type: "positive", Pattern: "valid"},
	}

	deltas := buildSignalCountDeltas(signals)

	if len(deltas) != 1 {
		t.Fatalf("expected 1 delta (blank patterns skipped), got %d", len(deltas))
	}
	if deltas[0].PatternKey != "valid" {
		t.Errorf("pattern key = %q, want %q", deltas[0].PatternKey, "valid")
	}
}

func TestBuildSignalCountDeltas_SkipsUnknownType(t *testing.T) {
	signals := []PreferenceSignal{
		{Type: "positive", Pattern: "good"},
		{Type: "unknown", Pattern: "should be skipped"},
		{Type: "negative", Pattern: "bad"},
	}

	deltas := buildSignalCountDeltas(signals)

	if len(deltas) != 2 {
		t.Fatalf("expected 2 deltas (unknown type skipped), got %d", len(deltas))
	}
}

func TestBuildSignalCountDeltas_Empty(t *testing.T) {
	deltas := buildSignalCountDeltas(nil)

	if deltas != nil {
		t.Errorf("expected nil for empty input, got %v", deltas)
	}
}

// Verify the return type matches what PersistSignalsAtomically expects.
func TestBuildSignalCountDeltas_ReturnsStorageType(t *testing.T) {
	signals := []PreferenceSignal{
		{Type: "positive", Pattern: "test"},
	}

	deltas := buildSignalCountDeltas(signals)

	// Compile-time check: deltas must be []storage.SignalCountDelta.
	var _ []storage.SignalCountDelta = deltas
	if len(deltas) != 1 {
		t.Fatalf("expected 1 delta, got %d", len(deltas))
	}
}
