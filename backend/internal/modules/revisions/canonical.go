package revisions

import (
	"encoding/json"
	"math/big"
	"strings"
)

// JSONB preserves numeric values but can change their lexical representation.
// Normalize exactly, without converting counters through float64 or expanding
// a large exponent into an unbounded run of zeroes.
func canonicalNumbers(v any) any {
	switch x := v.(type) {
	case json.Number:
		raw := x.String()
		sign := ""
		if strings.HasPrefix(raw, "-") {
			sign, raw = "-", raw[1:]
		}
		exponent := new(big.Int)
		if i := strings.IndexAny(raw, "eE"); i >= 0 {
			exponent.SetString(raw[i+1:], 10) // The JSON decoder has validated the grammar.
			raw = raw[:i]
		}
		fraction := 0
		if i := strings.IndexByte(raw, '.'); i >= 0 {
			fraction = len(raw) - i - 1
			raw = raw[:i] + raw[i+1:]
		}
		coefficient := strings.TrimLeft(raw, "0")
		if coefficient == "" {
			return json.Number("0")
		}
		trimmed := strings.TrimRight(coefficient, "0")
		exponent.Add(exponent, big.NewInt(int64(len(coefficient)-len(trimmed)-fraction)))
		coefficient = trimmed
		scientific := new(big.Int).Add(exponent, big.NewInt(int64(len(coefficient)-1)))
		if scientific.IsInt64() && scientific.Int64() >= -6 && scientific.Int64() < 21 {
			point := int(scientific.Int64()) + 1
			switch {
			case point <= 0:
				raw = "0." + strings.Repeat("0", -point) + coefficient
			case point >= len(coefficient):
				raw = coefficient + strings.Repeat("0", point-len(coefficient))
			default:
				raw = coefficient[:point] + "." + coefficient[point:]
			}
		} else {
			raw = coefficient[:1]
			if len(coefficient) > 1 {
				raw += "." + coefficient[1:]
			}
			power := scientific.String()
			if scientific.Sign() >= 0 {
				power = "+" + power
			}
			raw += "e" + power
		}
		return json.Number(sign + raw)
	case map[string]any:
		for key, value := range x {
			x[key] = canonicalNumbers(value)
		}
	case []any:
		for i, value := range x {
			x[i] = canonicalNumbers(value)
		}
	}
	return v
}
