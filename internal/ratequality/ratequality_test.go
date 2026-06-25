package ratequality

import (
	"testing"
	"time"

	"github.com/vul-os/openrate/internal/rates"
)

func TestAssessGradesFreshCorroborated(t *testing.T) {
	now := time.Date(2026, 6, 25, 0, 0, 0, 0, time.UTC)
	s := rates.Series{
		Series: "za.ref.zaronia", Area: "ZA", Type: rates.TypeReference,
		Value: 7.05, Date: now, Source: "sarbrates",
		Latest: []rates.Quote{
			{Source: "sarbrates", Value: 7.05, Date: now},
			{Source: "bis", Value: 7.04, Date: now},
		},
	}
	a := Assess(s, now)
	if a.Grade != "A" {
		t.Errorf("grade = %s, want A (fresh, issuer, corroborated). conf=%v", a.Grade, a.Confidence)
	}
	if !a.Corroboration.Agree {
		t.Errorf("expected corroboration agreement at 1bp spread, got spread=%v", a.Corroboration.SpreadBps)
	}
}

func TestAssessSingleSourceCaveat(t *testing.T) {
	now := time.Date(2026, 6, 25, 0, 0, 0, 0, time.UTC)
	s := rates.Series{
		Series: "us.policy", Area: "US", Type: rates.TypePolicy,
		Value: 3.625, Date: now, Source: "bis",
		Latest: []rates.Quote{{Source: "bis", Value: 3.625, Date: now}},
	}
	a := Assess(s, now)
	if len(a.Caveats) < 2 {
		t.Errorf("want target-range + single-source caveats, got %v", a.Caveats)
	}
	if a.Corroboration.Sources != 1 {
		t.Errorf("sources = %d, want 1", a.Corroboration.Sources)
	}
}

func TestAssessStaleDowngrades(t *testing.T) {
	now := time.Date(2026, 6, 25, 0, 0, 0, 0, time.UTC)
	s := rates.Series{
		Series: "xx.policy", Area: "XX", Type: rates.TypePolicy,
		Value: 5, Date: now.AddDate(0, 0, -200), Source: "bis",
		Latest: []rates.Quote{{Source: "bis", Value: 5, Date: now.AddDate(0, 0, -200)}},
	}
	a := Assess(s, now)
	if a.Freshness != "old" {
		t.Errorf("freshness = %s, want old", a.Freshness)
	}
	if a.Grade != "D" {
		t.Errorf("grade = %s, want D for 200-day-old data", a.Grade)
	}
}
