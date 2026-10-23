package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type ExecutionPriceRule struct {
	RecoveryQueriesFree   bool      `json:"recovery_queries_free,omitempty"`
	RequestContract       string    `json:"request_contract,omitempty"`
	InputMinorPerMillion  string    `json:"input_minor_per_million,omitempty"`
	OutputMinorPerMillion string    `json:"output_minor_per_million,omitempty"`
	RequestDigest         string    `json:"request_sha256"`
	Provider              string    `json:"provider"`
	Model                 string    `json:"model"`
	Capability            string    `json:"capability"`
	Currency              string    `json:"currency"`
	MaximumMinor          int64     `json:"maximum_minor"`
	MaximumInputBytes     int       `json:"maximum_input_bytes"`
	Source                string    `json:"source"`
	ExpiresAt             time.Time `json:"expires_at"`
}
type ExecutionPolicy struct {
	Version             int                  `json:"version"`
	Prices              []ExecutionPriceRule `json:"prices"`
	ModerationCommand   string               `json:"moderation_command"`
	ModerationContract  string               `json:"moderation_contract"`
	ModerationSource    string               `json:"moderation_source"`
	ModerationExpiresAt time.Time            `json:"moderation_expires_at"`
	BillingCommand      string               `json:"billing_command,omitempty"`
	BillingContract     string               `json:"billing_contract,omitempty"`
	BillingSource       string               `json:"billing_source,omitempty"`
	BillingExpiresAt    time.Time            `json:"billing_expires_at,omitempty"`
}

// Rules are operator assertions of a conservative bounded request quote, not an
// exchange-rate or guessed market price. No matching fresh rule means no send.
func (p *ExecutionPolicy) QuoteExecution(_ context.Context, r ExecutionRequest) (ExecutionQuote, error) {
	for _, v := range p.Prices {
		if v.Provider != r.Provider || v.Model != r.Model || v.Capability != r.Capability || len(r.Text) > v.MaximumInputBytes {
			continue
		}
		amount := v.MaximumMinor
		if v.RequestContract == "text-64k-6000-v1" {
			if r.HasMedia || r.Capability != "text" || (r.Provider != "openai-text" && r.Provider != "anthropic-text") {
				continue
			}
			var e error
			amount, e = boundedTextAmount(v)
			if e != nil {
				continue
			}
		} else if v.RequestDigest == "" || v.RequestDigest != ExecutionRequestDigest(r) {
			continue
		}
		return ExecutionQuote{Currency: v.Currency, MaximumMinor: amount, Evidence: v.Source, ExpiresAt: v.ExpiresAt}, nil
	}

	return ExecutionQuote{}, ErrExecutionGovernance
}

type limitedCommandBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitedCommandBuffer) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		return 0, errors.New("policy command output limit")
	}
	return b.Buffer.Write(p)
}
func policyCommand(ctx context.Context, path string, in, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	raw, e := json.Marshal(in)
	if e != nil || len(raw) > 64<<20 {
		return ErrExecutionGovernance
	}
	cmd := exec.CommandContext(ctx, path)
	cmd.WaitDelay = time.Second
	cmd.Stdin = bytes.NewReader(raw)
	// Explicitly empty inherited environment prevents accidental platform secret
	// exposure. Commands needing services must use operator-controlled references.
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	buf := &limitedCommandBuffer{limit: 16 << 10}
	cmd.Stdout = buf
	cmd.Stderr = io.Discard
	if cmd.Run() != nil {
		return ErrExecutionGovernance
	}
	d := json.NewDecoder(&buf.Buffer)
	d.DisallowUnknownFields()
	if d.Decode(out) != nil {
		return ErrExecutionGovernance
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return ErrExecutionGovernance
	}
	return nil
}
func nonbillableCommand(contract, source string, expires time.Time) bool {
	return contract == "local-or-free-v1" && strings.TrimSpace(source) != "" && len(source) <= 256 && expires.After(time.Now())
}
func (p *ExecutionPolicy) billingIsNonbillable() bool {
	return p != nil && nonbillableCommand(p.BillingContract, p.BillingSource, p.BillingExpiresAt)
}
func (p *ExecutionPolicy) moderationIsNonbillable() bool {
	return p != nil && nonbillableCommand(p.ModerationContract, p.ModerationSource, p.ModerationExpiresAt)
}

// Readiness is dynamic: an expired nonbillable review approval cannot remain
// advertised as an executable managed-model configuration.
func (p *ExecutionPolicy) ReviewReady() bool { return p.moderationIsNonbillable() }
func (p *ExecutionPolicy) ReviewExecution(ctx context.Context, r ExecutionRequest) (ModerationDecision, error) {
	var result ModerationDecision
	// Review happens before the model reservation. Until separately metered
	// review is implemented, only explicitly reviewed nonbillable adapters run.
	if !p.moderationIsNonbillable() {
		return result, ErrExecutionGovernance
	}
	if r.HasMedia && len(r.Media) == 0 {
		return result, ErrExecutionGovernance
	}
	err := policyCommand(ctx, p.ModerationCommand, r, &result)
	return result, err
}

type ExecutionBillingReader interface {
	ReadExecutionBilling(context.Context, ExecutionRequest) (BillingEvidence, error)
}

func (p *ExecutionPolicy) ReadExecutionBilling(ctx context.Context, r ExecutionRequest) (BillingEvidence, error) {
	if p == nil || p.BillingCommand == "" || !p.billingIsNonbillable() {
		return BillingEvidence{}, ErrExecutionGovernance
	}
	var result BillingEvidence
	// Prompt/media never sent to the billing reconciler.
	in := struct{ ID, Tenant, Provider, Model, Capability string }{r.ID, r.Tenant, r.Provider, r.Model, r.Capability}
	err := policyCommand(ctx, p.BillingCommand, in, &result)
	return result, err
}
func validatePolicyCommand(path string) error {
	if !filepath.IsAbs(path) {
		return ErrExecutionGovernance
	}
	f, e := os.Stat(path)
	if e != nil || !f.Mode().IsRegular() || f.Mode().Perm()&0111 == 0 {
		return ErrExecutionGovernance
	}
	return nil
}

// ConfigureExecutionFromEnv reads only the explicitly named non-secret policy.
// No credentials, supplier calls or paid probes occur during configuration.
func ConfigureExecutionFromEnv(s *FileStore) error {
	path := os.Getenv("EXECUTION_POLICY_FILE")
	if path == "" {
		return nil
	}
	f, e := os.Open(path)
	if e != nil {
		return errors.New("execution policy unavailable")
	}
	defer f.Close()
	raw, e := io.ReadAll(io.LimitReader(f, 65537))
	if e != nil || len(raw) > 65536 {
		return ErrExecutionGovernance
	}
	var policy ExecutionPolicy
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&policy) != nil {
		return ErrExecutionGovernance
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return ErrExecutionGovernance
	}
	if !policy.moderationIsNonbillable() || policy.Version != 1 || len(policy.Prices) == 0 || len(policy.Prices) > 200 || validatePolicyCommand(policy.ModerationCommand) != nil {
		return ErrExecutionGovernance
	}
	if policy.BillingCommand != "" && (!policy.billingIsNonbillable() || validatePolicyCommand(policy.BillingCommand) != nil) {
		return ErrExecutionGovernance
	}
	seen := map[string]bool{}
	for _, v := range policy.Prices {
		key := v.RequestDigest + v.Provider + "\x00" + v.Model + "\x00" + v.Capability
		_, ce := CurrencyMinorUnits(v.Currency)
		if (v.RequestContract != "" && v.RequestContract != "text-64k-6000-v1") || ((v.RequestContract != "text-64k-6000-v1" || (v.Provider != "openai-text" && v.Provider != "anthropic-text") || v.Capability != "text") && len(v.RequestDigest) != 64) || seen[key] || v.Provider == "" || v.Model == "" || v.Capability == "" || v.Currency == "" || ce != nil || (v.RequestContract == "" && v.MaximumMinor <= 0) || v.MaximumInputBytes <= 0 || v.MaximumInputBytes > 1<<20 || v.Source == "" || len(v.Source) > 256 || !v.ExpiresAt.After(time.Now()) {
			return ErrExecutionGovernance
		}
		if v.RequestContract != "" {
			if _, err := boundedTextAmount(v); err != nil {
				return err
			}
		}
		seen[key] = true
	}
	ConfigureExecutionGovernance(s, &ExecutionGovernor{Ledger: s.budget, Quoter: &policy, Moderator: &policy, Billing: &policy})
	return nil
}

// Upper bound treats every encoded request byte as one input token, including
// schema/system overhead, and reserves all 6000 allowed output/reasoning tokens.
func boundedTextAmount(v ExecutionPriceRule) (int64, error) {
	parse := func(s string) (*big.Rat, bool) {
		if len(s) > 32 || !exchangeRatePattern.MatchString(s) {
			return nil, false
		}
		r, ok := new(big.Rat).SetString(s)
		return r, ok && r.Sign() >= 0
	}
	input, ok := parse(v.InputMinorPerMillion)
	if !ok {
		return 0, ErrExecutionGovernance
	}
	output, ok := parse(v.OutputMinorPerMillion)
	if !ok {
		return 0, ErrExecutionGovernance
	}
	sum := new(big.Rat).Add(new(big.Rat).Mul(input, big.NewRat(65536, 1000000)), new(big.Rat).Mul(output, big.NewRat(6000, 1000000)))
	q, rem := new(big.Int), new(big.Int)
	q.QuoRem(sum.Num(), sum.Denom(), rem)
	if rem.Sign() > 0 {
		q.Add(q, big.NewInt(1))
	}
	if !q.IsInt64() || q.Sign() <= 0 {
		return 0, ErrExecutionGovernance
	}
	return q.Int64(), nil
}

func (p *ExecutionPolicy) RecoveryExpiry(_ context.Context, r ExecutionRequest) (time.Time, error) {
	for _, v := range p.Prices {
		if v.RecoveryQueriesFree && v.Provider == r.Provider && v.Model == r.Model && v.Capability == r.Capability && v.Currency == r.Selection.Currency && v.Source != "" && v.ExpiresAt.After(time.Now()) {
			return v.ExpiresAt, nil
		}
	}
	return time.Time{}, ErrExecutionGovernance
}
