package rates

import (
	"math"
	"testing"
	"time"
)

func TestMaterializeHeadlinePrefersAuthority(t *testing.T) {
	now := time.Date(2026, 6, 25, 0, 0, 0, 0, time.UTC)
	b := New()
	// A less authoritative source (rank 0) with a fresher date, and the issuing
	// bank (sarbrates, rank 4) slightly older. Authority must win the headline.
	b.Replace("randomapi", []Observation{
		{Series: "za.ref.zaronia", Area: "ZA", Type: TypeReference, Value: 7.10, Date: now, Source: "randomapi"},
	})
	b.Replace("sarbrates", []Observation{
		{Series: "za.ref.zaronia", Area: "ZA", Type: TypeReference, Value: 7.05, Date: now.AddDate(0, 0, -1), Source: "sarbrates"},
	})
	snap := b.Materialize(now)
	s, ok := snap.Lookup("za.ref.zaronia")
	if !ok {
		t.Fatal("series not found")
	}
	if s.Source != "sarbrates" {
		t.Errorf("headline source = %q, want sarbrates (higher authority)", s.Source)
	}
	if s.Value != 7.05 {
		t.Errorf("headline value = %v, want 7.05", s.Value)
	}
	if len(s.Latest) != 2 {
		t.Errorf("corroboration quotes = %d, want 2", len(s.Latest))
	}
}

func TestMaterializeSkipsNonFinite(t *testing.T) {
	now := time.Date(2026, 6, 25, 0, 0, 0, 0, time.UTC)
	b := New()
	// BIS emits literal "NaN" for missing days; the model must drop them so a
	// JSON encode of the snapshot never blanks.
	b.Replace("bis", []Observation{
		{Series: "za.policy", Area: "ZA", Type: TypePolicy, Value: math.NaN(), Date: now.AddDate(0, 0, -1), Source: "bis"},
		{Series: "za.policy", Area: "ZA", Type: TypePolicy, Value: 7.0, Date: now, Source: "bis"},
		{Series: "za.policy", Area: "ZA", Type: TypePolicy, Value: math.Inf(1), Date: now.AddDate(0, 0, -2), Source: "bis"},
	})
	snap := b.Materialize(now)
	s, ok := snap.Lookup("za.policy")
	if !ok {
		t.Fatal("series not found")
	}
	if len(s.History) != 1 {
		t.Fatalf("history points = %d, want 1 (NaN/Inf skipped)", len(s.History))
	}
	if s.Value != 7.0 {
		t.Errorf("headline value = %v, want 7.0", s.Value)
	}
}

func TestMaterializeDedupesHistoryByDate(t *testing.T) {
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	day := time.Date(2026, 6, 24, 0, 0, 0, 0, time.UTC)
	b := New()
	b.Replace("bis", []Observation{
		{Series: "us.policy", Area: "US", Type: TypePolicy, Value: 3.50, Date: day, Source: "bis"},
		{Series: "us.policy", Area: "US", Type: TypePolicy, Value: 3.625, Date: day.Add(6 * time.Hour), Source: "bis"}, // same day, later
	})
	snap := b.Materialize(now)
	s, _ := snap.Lookup("us.policy")
	if len(s.History) != 1 {
		t.Fatalf("history points = %d, want 1 (deduped by date)", len(s.History))
	}
	if s.History[0].Value != 3.625 {
		t.Errorf("deduped value = %v, want 3.625 (later wins)", s.History[0].Value)
	}
}
