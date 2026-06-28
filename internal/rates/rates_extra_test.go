package rates

import (
	"testing"
	"time"
)

var rNow = time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)

func obs(series, area, typ string, value float64, date time.Time, source string) Observation {
	return Observation{Series: series, Area: area, Type: typ, Value: value, Date: date, Source: source}
}

// ─── Headline selection tie-break ────────────────────────────────────────────

// When two sources have equal authority rank, the one with the more recent
// observation date must win the headline.
func TestMaterializeEqualAuthorityFresherWins(t *testing.T) {
	b := New()
	day0 := rNow.AddDate(0, 0, -2)
	day1 := rNow.AddDate(0, 0, -1) // one day fresher

	// bis=rank 3, fred=rank 3 — equal authority.
	b.Replace("bis", []Observation{
		obs("us.policy", "US", TypePolicy, 5.25, day0, "bis"),
	})
	b.Replace("fred", []Observation{
		obs("us.policy", "US", TypePolicy, 5.33, day1, "fred"),
	})
	snap := b.Materialize(rNow)

	s, ok := snap.Lookup("us.policy")
	if !ok {
		t.Fatal("us.policy must be in snapshot")
	}
	if s.Source != "fred" {
		t.Errorf("headline source = %q, want fred (same rank, fresher date)", s.Source)
	}
	if s.Value != 5.33 {
		t.Errorf("headline value = %v, want 5.33", s.Value)
	}
}

// ─── Empty book ──────────────────────────────────────────────────────────────

func TestMaterializeEmptyBook(t *testing.T) {
	b := New()
	snap := b.Materialize(rNow)

	if len(snap.Series) != 0 {
		t.Errorf("empty book: got %d series, want 0", len(snap.Series))
	}
	if len(snap.IDs()) != 0 {
		t.Errorf("empty book IDs = %v, want []", snap.IDs())
	}
}

// ─── Replace clears source ───────────────────────────────────────────────────

func TestMaterializeReplaceClearsSource(t *testing.T) {
	b := New()
	b.Replace("bis", []Observation{
		obs("za.policy", "ZA", TypePolicy, 7.0, rNow, "bis"),
	})
	b.Replace("bis", nil) // clear
	snap := b.Materialize(rNow)

	if _, ok := snap.Lookup("za.policy"); ok {
		t.Error("cleared source must not leave observations behind")
	}
}

// ─── Multiple independent series ─────────────────────────────────────────────

func TestMaterializeMultipleSeries(t *testing.T) {
	b := New()
	b.Replace("bis", []Observation{
		obs("us.policy", "US", TypePolicy, 5.25, rNow, "bis"),
		obs("za.policy", "ZA", TypePolicy, 8.25, rNow, "bis"),
	})
	snap := b.Materialize(rNow)

	for _, id := range []string{"us.policy", "za.policy"} {
		if _, ok := snap.Lookup(id); !ok {
			t.Errorf("%s must appear in snapshot", id)
		}
	}
	if len(snap.IDs()) != 2 {
		t.Errorf("IDs count = %d, want 2", len(snap.IDs()))
	}
}

// ─── History ordering ────────────────────────────────────────────────────────

func TestMaterializeHistoryAscending(t *testing.T) {
	b := New()
	b.Replace("bis", []Observation{
		obs("us.policy", "US", TypePolicy, 5.0, rNow.AddDate(0, 0, -3), "bis"),
		obs("us.policy", "US", TypePolicy, 5.25, rNow.AddDate(0, 0, -1), "bis"),
		obs("us.policy", "US", TypePolicy, 5.1, rNow.AddDate(0, 0, -2), "bis"),
	})
	snap := b.Materialize(rNow)
	s, _ := snap.Lookup("us.policy")

	for i := 1; i < len(s.History); i++ {
		if !s.History[i].Date.After(s.History[i-1].Date) {
			t.Errorf("history[%d] = %v not after history[%d] = %v (want ascending)",
				i, s.History[i].Date, i-1, s.History[i-1].Date)
		}
	}
}

// ─── Snapshot index ──────────────────────────────────────────────────────────

func TestSnapshotIDsSorted(t *testing.T) {
	b := New()
	b.Replace("bis", []Observation{
		obs("za.policy", "ZA", TypePolicy, 8.0, rNow, "bis"),
		obs("us.policy", "US", TypePolicy, 5.0, rNow, "bis"),
		obs("xm.ref.estr", "XM", TypeReference, 3.9, rNow, "bis"),
	})
	snap := b.Materialize(rNow)
	ids := snap.IDs()

	for i := 1; i < len(ids); i++ {
		if ids[i] < ids[i-1] {
			t.Errorf("IDs not sorted: %q comes after %q", ids[i], ids[i-1])
		}
	}
}

func TestSnapshotLookupMissingReturnsNotFound(t *testing.T) {
	b := New()
	snap := b.Materialize(rNow)
	if _, ok := snap.Lookup("nonexistent.series"); ok {
		t.Error("Lookup on empty snapshot must return ok=false")
	}
}

// ─── Invalid observation filtering ───────────────────────────────────────────

func TestMaterializeEmptySeriesIDSkipped(t *testing.T) {
	b := New()
	b.Replace("bis", []Observation{
		{Series: "", Area: "US", Type: TypePolicy, Value: 5.0, Date: rNow, Source: "bis"},
		obs("us.policy", "US", TypePolicy, 5.25, rNow, "bis"),
	})
	snap := b.Materialize(rNow)

	// Only the valid observation must appear.
	if len(snap.IDs()) != 1 {
		t.Errorf("IDs = %v, want exactly 1 (empty Series skipped)", snap.IDs())
	}
}

// ─── Corroboration quotes ────────────────────────────────────────────────────

func TestMaterializeCorroborationQuotesBothSources(t *testing.T) {
	b := New()
	b.Replace("sarbrates", []Observation{
		obs("za.ref.zaronia", "ZA", TypeReference, 7.05, rNow.AddDate(0, 0, -1), "sarbrates"),
	})
	b.Replace("bis", []Observation{
		obs("za.ref.zaronia", "ZA", TypeReference, 7.10, rNow, "bis"),
	})
	snap := b.Materialize(rNow)
	s, _ := snap.Lookup("za.ref.zaronia")

	if len(s.Latest) != 2 {
		t.Errorf("corroboration quotes = %d, want 2", len(s.Latest))
	}
	// Latest should be sorted by source name.
	if len(s.Latest) == 2 && s.Latest[0].Source > s.Latest[1].Source {
		t.Errorf("Latest quotes not sorted by source: %v", s.Latest)
	}
}

// ─── Source rank helper ──────────────────────────────────────────────────────

func TestRankKnownSources(t *testing.T) {
	cases := map[string]int{
		"sarbrates": 4,
		"bis":       3,
		"fred":      3,
		"ecbrates":  3,
		"unknown":   0,
	}
	for src, want := range cases {
		if got := Rank(src); got != want {
			t.Errorf("Rank(%q) = %d, want %d", src, got, want)
		}
	}
}

// ─── dedupeByDate ────────────────────────────────────────────────────────────

func TestDedupeByDateLatestWins(t *testing.T) {
	day := time.Date(2026, 6, 24, 0, 0, 0, 0, time.UTC)
	obs := []Observation{
		{Series: "s", Value: 3.50, Date: day, Source: "bis"},
		{Series: "s", Value: 3.75, Date: day.Add(6 * time.Hour), Source: "bis"}, // same day, later
	}
	pts := dedupeByDate(obs)
	if len(pts) != 1 {
		t.Fatalf("dedupeByDate: got %d points, want 1", len(pts))
	}
	if pts[0].Value != 3.75 {
		t.Errorf("deduped value = %v, want 3.75 (later observation wins)", pts[0].Value)
	}
}
