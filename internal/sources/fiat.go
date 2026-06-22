package sources

// fiatAllow is the ISO-4217 set we admit from broad multi-asset feeds (e.g.
// Coinbase lists hundreds of crypto tokens; we only want real currencies plus a
// few major crypto that bridge to venue feeds like Luno). Keeping the set small
// keeps the graph and the rates table clean.
var fiatAllow = set(
	"AUD", "BGN", "BRL", "CAD", "CHF", "CNY", "CZK", "DKK", "EUR", "GBP",
	"HKD", "HRK", "HUF", "IDR", "ILS", "INR", "ISK", "JPY", "KRW", "MXN",
	"MYR", "NOK", "NZD", "PHP", "PLN", "RON", "SEK", "SGD", "THB", "TRY",
	"USD", "ZAR", "AED", "SAR", "NGN", "KES", "GHS", "EGP", "MAD", "BWP",
)

// cryptoAllow are the few crypto assets we keep so venue feeds (Luno BTC/ZAR,
// ETH/ZAR) can bridge into the fiat graph via a shared node.
var cryptoAllow = set("BTC", "ETH", "USDT", "USDC")

func set(xs ...string) map[string]bool {
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		m[x] = true
	}
	return m
}

func allowed(code string) bool { return fiatAllow[code] || cryptoAllow[code] }
