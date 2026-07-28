package ratequality

import (
	"testing"
	"time"

	"github.com/vul-os/openrate/internal/rates"
)

// docs/interest-rates.md quotes concrete confidence numbers and a worked example
// response. These tests pin them, so the document cannot drift from the engine.

func series(area, source string, date time.Time, quotes ...rates.Quote) rates.Series {
	return rates.Series{
		Series: "x.policy", Area: area, Type: rates.TypePolicy,
		Value: 3.625, Date: date, Source: source, Latest: quotes,
	}
}

// TestDocumentedInterestGrades pins the figures in the "Confidence grade"
// section: 0.85 single / 0.94 corroborated / 0.97 from the issuing bank, and the
// 0.78 and 0.87 the US target-range caveat produces.
func TestDocumentedInterestGrades(t *testing.T) {
	now := time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)
	d := time.Date(2026, 6, 16, 0, 0, 0, 0, time.UTC) // 12 days -> "recent"

	for _, tc := range []struct {
		doc    string
		area   string
		source string
		quotes []rates.Quote
		conf   float64
		grade  string
	}{
		{
			doc: "BIS alone, no area caveat", area: "ZA", source: "bis",
			quotes: []rates.Quote{{Source: "bis", Value: 7.25, Date: d}},
			conf:   0.85, grade: "B",
		},
		{
			doc: "corroborated by a second source", area: "ZA", source: "bis",
			quotes: []rates.Quote{{Source: "bis", Value: 7.25, Date: d}, {Source: "sarbrates", Value: 7.25, Date: d}},
			conf:   0.94, grade: "A",
		},
		{
			doc: "headline from the issuing bank", area: "ZA", source: "sarbrates",
			quotes: []rates.Quote{{Source: "sarbrates", Value: 7.25, Date: d}, {Source: "bis", Value: 7.25, Date: d}},
			conf:   0.97, grade: "A",
		},
		{
			doc: "us.policy from BIS alone (target-range caveat)", area: "US", source: "bis",
			quotes: []rates.Quote{{Source: "bis", Value: 3.625, Date: d}},
			conf:   0.78, grade: "B",
		},
		{
			doc: "us.policy corroborated (still capped by the caveat)", area: "US", source: "bis",
			quotes: []rates.Quote{{Source: "bis", Value: 3.625, Date: d}, {Source: "fred", Value: 3.625, Date: d}},
			conf:   0.87, grade: "B",
		},
	} {
		t.Run(tc.doc, func(t *testing.T) {
			a := Assess(series(tc.area, tc.source, d, tc.quotes...), now)
			if a.Confidence != tc.conf || a.Grade != tc.grade {
				t.Errorf("docs/interest-rates.md says %.2f/%s, engine says %.2f/%s",
					tc.conf, tc.grade, a.Confidence, a.Grade)
			}
		})
	}
}

// TestDocumentedExampleResponse pins the worked us.policy example verbatim.
func TestDocumentedExampleResponse(t *testing.T) {
	now := time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)
	d := time.Date(2026, 6, 16, 0, 0, 0, 0, time.UTC)
	a := Assess(series("US", "bis", d, rates.Quote{Source: "bis", Value: 3.625, Date: d}), now)

	if a.Grade != "B" {
		t.Errorf(`example shows "grade": "B", got %q`, a.Grade)
	}
	if a.Confidence != 0.78 {
		t.Errorf(`example shows "confidence": 0.78, got %v`, a.Confidence)
	}
	if a.Freshness != "recent" {
		t.Errorf(`example shows "freshness": "recent", got %q`, a.Freshness)
	}
	if a.SourceClass != "official_aggregator" {
		t.Errorf(`example shows "source_class": "official_aggregator", got %q`, a.SourceClass)
	}
	if a.Corroboration.Sources != 1 || a.Corroboration.SpreadBps != 0 || a.Corroboration.Agree {
		t.Errorf(`example shows corroboration {sources:1, spread_bps:0, agree:false}, got %+v`, a.Corroboration)
	}
	// The example lists exactly these two caveats, in this order.
	want := []string{
		"US policy rate is a target range; the published value is the midpoint",
		"single source — not independently corroborated",
	}
	if len(a.Caveats) != len(want) {
		t.Fatalf("example lists %d caveats, got %d: %v", len(want), len(a.Caveats), a.Caveats)
	}
	for i := range want {
		if a.Caveats[i] != want[i] {
			t.Errorf("caveat %d = %q, want %q", i, a.Caveats[i], want[i])
		}
	}
}

// TestInterestGradeMatchesPublishedConfidence mirrors the FX guarantee: the two
// published fields can never contradict each other.
func TestInterestGradeMatchesPublishedConfidence(t *testing.T) {
	classes := []float64{1.0, 0.97, 0.9, 0.85, 0.75}
	freshFactors := []float64{1.0, 0.97, 0.82, 0.6, 0.4}
	corrFactors := []float64{1.0, 0.95, 0.9, 0.88, 0.78}
	caveats := []float64{1.0, 0.92}

	band := func(c float64) string {
		switch {
		case c >= 0.90:
			return "A"
		case c >= 0.78:
			return "B"
		case c >= 0.60:
			return "C"
		}
		return "D"
	}

	checked := 0
	for _, cl := range classes {
		for _, f := range freshFactors {
			for _, co := range corrFactors {
				for _, cav := range caveats {
					checked++
					published := round2(cl * f * co * cav)
					if g := grade(published); g != band(published) {
						t.Errorf("published confidence %.2f carries grade %q but the bands say %q",
							published, g, band(published))
					}
				}
			}
		}
	}
	if want := len(classes) * len(freshFactors) * len(corrFactors) * len(caveats); checked != want {
		t.Fatalf("checked %d combinations, expected %d", checked, want)
	}
	t.Logf("verified %d factor combinations", checked)
}
