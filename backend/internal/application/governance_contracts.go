package application

import (
	"context"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/governance"
)

type RuntimeSelection = governance.RuntimeSelection
type ModelAdapter = governance.ModelAdapter
type ModelConfigEntry = governance.ModelConfigEntry
type ModelConfigInput = governance.ModelConfigInput
type ModelCredentialInput = governance.ModelCredentialInput
type ModelConfigReport = governance.ModelConfigReport
type CostEntry = governance.CostEntry
type BudgetChange = governance.BudgetChange
type ExchangeQuote = governance.ExchangeQuote
type ExchangeProvider = governance.ExchangeProvider
type ExecutionRequest = governance.ExecutionRequest
type ExecutionMedia = governance.ExecutionMedia
type ExecutionQuote = governance.ExecutionQuote
type ExecutionQuoter = governance.ExecutionQuoter
type ExecutionModerator = governance.ExecutionModerator
type BillingEvidence = governance.BillingEvidence
type ModerationDecision = governance.ModerationDecision
type ContentModerator = governance.ContentModerator

func CurrencyMinorUnits(currency string) (int, error) { return governance.CurrencyMinorUnits(currency) }
func normalizeCurrency(currency string) (string, error) {
	return governance.NormalizeCurrency(currency)
}
func GetExchangeQuote(ctx context.Context, provider ExchangeProvider, from, to string, now time.Time, maxAge time.Duration) (ExchangeQuote, error) {
	return governance.GetExchangeQuote(ctx, provider, from, to, now, maxAge)
}
func ExecutionRequestDigest(r ExecutionRequest) string { return governance.ExecutionRequestDigest(r) }
