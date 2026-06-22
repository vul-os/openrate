package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vul-os/openrate/internal/graph"
)

// BoCURL is the Bank of Canada Valet API — the cleanest documented central-bank
// REST feed: daily indicative rates, headless-friendly, no auth. Series are named
// FX<CCY>CAD (value = CAD per 1 CCY), base CAD. We query explicit series rather
// than the FX_RATES_DAILY group, because the group's latest row can be a sparse
// partial day. recent=2 returns the last couple of observations so we always have
// a complete prior business day to read. Includes FXZARCAD.
const BoCURL = "https://www.bankofcanada.ca/valet/observations/" +
	"FXUSDCAD,FXEURCAD,FXGBPCAD,FXAUDCAD,FXJPYCAD,FXCHFCAD,FXZARCAD/json?recent=2"

type BoC struct {
	URL    string
	Client *http.Client
}

func NewBoC() *BoC {
	return &BoC{URL: BoCURL, Client: &http.Client{Timeout: 15 * time.Second}}
}

func (b *BoC) Name() string { return "boc" }

type bocResp struct {
	Observations []map[string]json.RawMessage `json:"observations"`
}

func (b *BoC) Fetch(ctx context.Context) ([]graph.Edge, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.URL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := b.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("boc: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var br bocResp
	if err := json.Unmarshal(body, &br); err != nil {
		return nil, fmt.Errorf("boc: parse: %w", err)
	}
	if len(br.Observations) == 0 {
		return nil, fmt.Errorf("boc: no observations")
	}

	// Merge across observations (chronological): keep the newest non-empty value
	// per currency, so a sparse partial latest row doesn't drop the others.
	latest := map[string]graph.Edge{}
	for _, obs := range br.Observations {
		ts := time.Now().UTC()
		if d, ok := obs["d"]; ok {
			var ds string
			if json.Unmarshal(d, &ds) == nil {
				if t, e := time.Parse("2006-01-02", ds); e == nil {
					ts = t
				}
			}
		}
		for key, raw := range obs {
			// Series like "FXUSDCAD" -> CCY=USD, base=CAD.
			if !strings.HasPrefix(key, "FX") || !strings.HasSuffix(key, "CAD") {
				continue
			}
			ccy := strings.TrimSuffix(strings.TrimPrefix(key, "FX"), "CAD")
			if len(ccy) != 3 || !allowed(ccy) {
				continue
			}
			var v struct {
				V string `json:"v"`
			}
			if json.Unmarshal(raw, &v) != nil || v.V == "" {
				continue
			}
			rate, perr := strconv.ParseFloat(v.V, 64)
			if perr != nil || rate <= 0 {
				continue
			}
			// value = CAD per 1 CCY  =>  1 CCY = rate CAD.
			latest[ccy] = graph.Edge{From: ccy, To: "CAD", Rate: rate, Source: b.Name(), Time: ts}
		}
	}
	if len(latest) == 0 {
		return nil, fmt.Errorf("boc: no FX series")
	}
	edges := make([]graph.Edge, 0, len(latest))
	for _, e := range latest {
		edges = append(edges, e)
	}
	return edges, nil
}
