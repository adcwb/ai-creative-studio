package governance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

// ExecutionRequest is assembled from the durable task, never from a submitted
// billing amount. Text is reviewed in memory and is not written to the ledger.
type ExecutionRequest struct {
	UpstreamID                                    string
	ID, Tenant, Provider, Model, Capability, Text string
	HasMedia                                      bool
	Parameters                                    json.RawMessage
	Stage                                         string
	Media                                         []ExecutionMedia
	Selection                                     *RuntimeSelection
}

type ExecutionMedia struct {
	Data []byte
	MIME string
}

func ExecutionRequestDigest(r ExecutionRequest) string {
	hashes := []string{}
	for _, m := range r.Media {
		sum := sha256.Sum256(m.Data)
		hashes = append(hashes, m.MIME+":"+hex.EncodeToString(sum[:]))
	}
	voice := ""
	if r.Selection != nil {
		voice = r.Selection.Voice
	}
	raw, _ := json.Marshal(struct {
		Contract, Provider, Model, Capability, Text, Voice string
		HasMedia                                           bool
		Parameters                                         json.RawMessage
		MediaHashes                                        []string
	}{"studio-request-v1", r.Provider, r.Model, r.Capability, r.Text, voice, r.HasMedia, r.Parameters, hashes})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

type ExecutionQuote struct {
	Currency     string
	MaximumMinor int64
	Evidence     string
	ExpiresAt    time.Time
}

type ExecutionQuoter interface {
	QuoteExecution(context.Context, ExecutionRequest) (ExecutionQuote, error)
}

// ExecutionModerator must explicitly review the requested modality. A text-only
// allow response cannot authorize images/audio/video by pretending to review it.
type ExecutionModerator interface {
	ReviewExecution(context.Context, ExecutionRequest) (ModerationDecision, error)
}

type BillingEvidence struct {
	State       string // settled or released; empty/unknown keeps the whole reservation
	ActualMinor int64
	Reference   string
}

type ModerationDecision struct {
	Provider   string    `json:"provider"`
	Model      string    `json:"model"`
	Status     string    `json:"status"` // allow, review, unavailable; never infer allow on error
	Categories []string  `json:"categories"`
	CheckedAt  time.Time `json:"checked_at"`
}

type ContentModerator interface {
	CheckText(context.Context, string) (ModerationDecision, error)
}
