package governance

import (
	"errors"
	"time"
)

// CurrencyMinorUnits is an explicit supported currency registry. Unknown codes
// fail closed; extending it requires review of the ISO currency's minor units.
func CurrencyMinorUnits(currency string) (int, error) {
	if currency == "" {
		currency = "USD"
	}
	switch currency {
	case "USD", "EUR", "GBP", "CNY", "CAD", "AUD", "CHF", "HKD", "SGD":
		return 2, nil
	case "JPY", "KRW":
		return 0, nil
	case "KWD", "BHD":
		return 3, nil
	default:
		return 0, errors.New("unsupported currency")
	}
}

func NormalizeCurrency(currency string) (string, error) {
	if currency == "" {
		currency = "USD"
	}
	_, err := CurrencyMinorUnits(currency)
	return currency, err
}

// Amounts are integer currency minor units, never floating point or implicitly
// converted. A successful upstream response alone is not billing evidence.
type CostEntry struct {
	ID        string    `json:"id"`
	Tenant    string    `json:"tenant"`
	Provider  string    `json:"provider"`
	Model     string    `json:"model"`
	Currency  string    `json:"currency"`
	Reserved  int64     `json:"reserved_minor"`
	Actual    int64     `json:"actual_minor"`
	State     string    `json:"state"`
	Evidence  string    `json:"evidence_ref,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

type BudgetChange struct {
	Actor    string    `json:"actor"`
	Tenant   string    `json:"tenant"`
	Currency string    `json:"currency"`
	Before   int64     `json:"before_minor"`
	After    int64     `json:"after_minor"`
	At       time.Time `json:"at"`
}
