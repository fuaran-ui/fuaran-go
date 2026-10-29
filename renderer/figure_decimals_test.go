package renderer

// Phase 2499 — every figure case carries `decimals`, Currency included. This
// host renders the locale-free form for every figure (a located one too: it
// declines located formats until the host formatter seam of Phase 2498), so the
// member reaches it as the fixed digit count of `CODE x.toFixed(d)`.

import (
	"testing"

	"github.com/fuaran-ui/fuaran-go/wire"
)

func currency(code string, decimals *int) wire.Value {
	fields := map[string]wire.Value{"code": wire.Str(code)}
	if decimals != nil {
		fields["decimals"] = wire.Int(*decimals)
	}
	return wire.Obj{Tag: "Currency", Fields: fields}
}

func TestCurrencyDecimalsSetTheLocaleFreeDigits(t *testing.T) {
	zero, three := 0, 3
	cases := []struct {
		format wire.Value
		value  float64
		want   string
	}{
		{currency("GBP", nil), 412380, "GBP 412380.00"},
		{currency("GBP", &zero), 412380.4, "GBP 412380"},
		{currency("USD", &three), 1.5, "USD 1.500"},
	}
	for _, c := range cases {
		if got := formatNumber(c.format, wire.Float(c.value)); got != c.want {
			t.Errorf("formatNumber(%v, %v) = %q, want %q", c.format, c.value, got, c.want)
		}
	}
}
