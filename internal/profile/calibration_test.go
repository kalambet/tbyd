package profile

import (
	"strings"
	"testing"
)

func TestGetCalibrationContext_GoExpert(t *testing.T) {
	store := newMockProfileStore()
	store.data["identity.expertise"] = `{"go":"expert"}`
	mgr := NewManager(store)

	ctx, err := mgr.GetCalibrationContext()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ctx.Hints == "" {
		t.Fatal("expected non-empty hints")
	}
	if !strings.Contains(ctx.Hints, "expert") {
		t.Errorf("hints missing expertise level: %q", ctx.Hints)
	}
	if !strings.Contains(ctx.Hints, "go") {
		t.Errorf("hints missing domain name: %q", ctx.Hints)
	}
}

func TestGetCalibrationContext_EmptyProfile(t *testing.T) {
	store := newMockProfileStore()
	mgr := NewManager(store)

	ctx, err := mgr.GetCalibrationContext()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ctx.Hints == "" {
		t.Fatal("expected non-empty generic hints for empty profile")
	}
}

func TestGetCalibrationContext_MultipleExpertise(t *testing.T) {
	store := newMockProfileStore()
	store.data["identity.expertise"] = `{"go":"expert","kubernetes":"intermediate","rust":"beginner"}`
	mgr := NewManager(store)

	ctx, err := mgr.GetCalibrationContext()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, domain := range []string{"go", "kubernetes", "rust"} {
		if !strings.Contains(ctx.Hints, domain) {
			t.Errorf("hints missing domain %q: %q", domain, ctx.Hints)
		}
	}
	for _, level := range []string{"expert", "intermediate", "beginner"} {
		if !strings.Contains(ctx.Hints, level) {
			t.Errorf("hints missing level %q: %q", level, ctx.Hints)
		}
	}
}
