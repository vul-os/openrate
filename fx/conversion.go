package fx

import (
	"errors"
	"math"
	"time"
)

// The errors Describe returns. They are the machine-readable form of the three
// refusals the HTTP API has always made, and callers should test them with
// errors.Is rather than matching on message text.
var (
	// ErrUnknownPair means the two currencies are not connected in the snapshot
	// — either one is unknown, or no path of quoted edges joins them.
	ErrUnknownPair = errors.New("unknown or unreachable currency pair")
	// ErrInvalidAmount means the amount is not a finite number. NaN and ±Inf
	// parse cleanly from a query string but poison the arithmetic downstream.
	ErrInvalidAmount = errors.New("invalid amount")
	// ErrAmountOutOfRange means the amount and the rate are each finite but
	// their product is not, so there is no representable answer to return.
	ErrAmountOutOfRange = errors.New("amount out of range for this pair")
	// ErrUnknownBase means a snapshot that does know some currencies does not
	// know the one asked for as a presentation base.
	ErrUnknownBase = errors.New("unknown base currency")
)

// SourceQuote is one source's direct quote for the pair, aged as of the moment
// the conversion was described.
type SourceQuote struct {
	Source string  `json:"source"`
	Rate   float64 `json:"rate"`
	AgeSec float64 `json:"age_sec"`
}

// Conversion is a complete answer to "what is X in Y": the number, the
// arithmetic behind it, where each hop came from, how old it is, and how much
// to trust it. It is the value openrate's HTTP API marshals and the value an
// embedding program gets from Engine.Convert.
//
// Rate and Legs are carried through from the snapshot's Pair untouched. Rate is
// the product of the Legs' rates bit for bit (see precision_test.go), and
// nothing here re-derives it — a facade that recomputed the product, or that
// rounded on the way through, would break an invariant this package pins.
type Conversion struct {
	From   string  `json:"from"`
	To     string  `json:"to"`
	Amount float64 `json:"amount"`
	// Result is Amount × Rate, the value the caller asked for.
	Result float64 `json:"result"`

	Rate    float64       `json:"rate"`
	Hops    int           `json:"hops"`
	AsOf    time.Time     `json:"as_of"`
	AgeSec  float64       `json:"age_sec"`
	Path    []string      `json:"path"`
	Sources []string      `json:"sources"`
	Quality Assessment    `json:"quality"`
	Legs    []Leg         `json:"legs"`   // each hop's actual rate + source (the calculation)
	Quotes  []SourceQuote `json:"quotes"` // per-source direct quotes behind the number
}

// Describe converts amount from one currency to another against a snapshot and
// returns the full provenance with it. now is taken as the current instant for
// ageing and grading, so a caller with an injected clock gets a deterministic
// answer.
//
// It is pure: no clock, no network, no environment. Everything openrate serves
// on /api/v1/rates and /api/v1/convert is built from this one function, so the
// HTTP surface and the in-process surface cannot describe the same rate
// differently.
func Describe(snap *Snapshot, from, to string, amount float64, now time.Time) (Conversion, error) {
	if math.IsInf(amount, 0) || math.IsNaN(amount) {
		return Conversion{}, ErrInvalidAmount
	}
	p, ok := snap.Lookup(from, to)
	if !ok {
		return Conversion{}, ErrUnknownPair
	}
	// The amount and the rate can each be finite while their product is not
	// (e.g. amount near math.MaxFloat64). There is no answer to give, and
	// pretending otherwise streams a number no JSON decoder will accept.
	result := amount * p.Rate
	if math.IsInf(result, 0) || math.IsNaN(result) {
		return Conversion{}, ErrAmountOutOfRange
	}

	quotes := snap.DirectQuotes(from, to)
	var aged []SourceQuote
	for _, q := range quotes {
		aged = append(aged, SourceQuote{Source: q.Source, Rate: q.Rate, AgeSec: now.Sub(q.Time).Seconds()})
	}
	// The snapshot is shared across goroutines and must stay immutable, so the
	// legs are copied rather than aliased. A struct copy moves the float64 bits
	// verbatim; it is not arithmetic and cannot perturb the rate.
	legs := append([]Leg(nil), p.Legs...)

	return Conversion{
		From:    from,
		To:      to,
		Amount:  amount,
		Result:  result,
		Rate:    p.Rate,
		Hops:    p.Hops,
		AsOf:    p.AsOf,
		AgeSec:  now.Sub(p.AsOf).Seconds(),
		Path:    append([]string(nil), p.Path...),
		Sources: append([]string(nil), p.Sources...),
		Quality: Assess(from, to, p, quotes, now),
		Legs:    legs,
		Quotes:  aged,
	}, nil
}
