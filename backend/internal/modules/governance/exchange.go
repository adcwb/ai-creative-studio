package governance

import (
	"context"
	"errors"
	"math/big"
	"regexp"
	"strings"
	"time"
)

// ExchangeQuote only reports a provider-supplied quotation; it never mutates a
// budget or silently converts ledger entries. No provider means no conversion.
type ExchangeQuote struct {
	From   string    `json:"from"`
	To     string    `json:"to"`
	Rate   string    `json:"rate"`
	Source string    `json:"source"`
	AsOf   time.Time `json:"as_of"`
	Stale  bool      `json:"stale"`
}

type ExchangeProvider interface {
	Quote(context.Context, string, string) (ExchangeQuote, error)
}

var exchangeRatePattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)

func GetExchangeQuote(ctx context.Context, provider ExchangeProvider, from, to string, now time.Time, maxAge time.Duration) (ExchangeQuote, error) {
	from, err := NormalizeCurrency(from)
	if err != nil {
		return ExchangeQuote{}, err
	}
	to, err = NormalizeCurrency(to)
	if err != nil {
		return ExchangeQuote{}, err
	}
	if provider == nil {
		return ExchangeQuote{}, errors.New("exchange provider unavailable; amounts are not converted")
	}
	quote, err := provider.Quote(ctx, from, to)
	if err != nil {
		return ExchangeQuote{}, errors.New("exchange quote unavailable; amounts are not converted")
	}
	if len(quote.Rate) > 64 || !exchangeRatePattern.MatchString(quote.Rate) || len(quote.Source) > 120 || strings.ContainsAny(quote.Source, "\x00\r\n") {
		return ExchangeQuote{}, errors.New("invalid exchange quote")
	}
	rate, ok := new(big.Rat).SetString(quote.Rate)
	if !ok || rate.Sign() <= 0 || quote.From != from || quote.To != to || quote.Source == "" || quote.AsOf.IsZero() || quote.AsOf.After(now) || maxAge <= 0 {
		return ExchangeQuote{}, errors.New("invalid exchange quote")
	}
	quote.Stale = quote.Stale || now.Sub(quote.AsOf) > maxAge
	return quote, nil
}

// ValidExchangeRate accepts the same decimal syntax as execution quotations.
func ValidExchangeRate(rate string) bool { return exchangeRatePattern.MatchString(rate) }
