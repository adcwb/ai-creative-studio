package application

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"
)

type ModelCall struct {
	TaskID          string     `json:"task_id,omitempty"`
	ID              string     `json:"id"`
	Tenant          string     `json:"tenant"`
	GatewayID       string     `json:"gateway_id"`
	GatewayName     string     `json:"gateway_name"`
	ModelConfigID   string     `json:"model_config_id"`
	ModelName       string     `json:"model_name"`
	Model           string     `json:"model"`
	FeatureKey      string     `json:"feature_key"`
	Capability      string     `json:"capability"`
	State           string     `json:"state"`
	ErrorCode       string     `json:"error_code,omitempty"`
	RequestID       string     `json:"request_id,omitempty"`
	HTTPCalls       int64      `json:"http_calls"`
	HTTPStatus      int        `json:"http_status"`
	InputTokens     *int64     `json:"input_tokens"`
	OutputTokens    *int64     `json:"output_tokens"`
	CachedTokens    *int64     `json:"cached_tokens"`
	ReasoningTokens *int64     `json:"reasoning_tokens"`
	DurationMS      int64      `json:"duration_ms"`
	StartedAt       time.Time  `json:"started_at"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
	Currency        string     `json:"currency"`
	MaximumMinor    int64      `json:"maximum_minor"`
}
type modelUsageSnapshot struct {
	Version int                  `json:"version"`
	Calls   map[string]ModelCall `json:"calls"`
}
type ModelUsageStore struct {
	mu     stateMutex
	pg     *postgresScope
	path   string
	data   modelUsageSnapshot
	failed error
}

func openModelUsageStore(dir string) (*ModelUsageStore, error) {
	s := &ModelUsageStore{path: filepath.Join(dir, "model-usage.json"), data: modelUsageSnapshot{Version: 1, Calls: map[string]ModelCall{}}}
	raw, e := os.ReadFile(s.path)
	if e == nil {
		if json.Unmarshal(raw, &s.data) != nil || s.data.Version != 1 || s.data.Calls == nil {
			return nil, errors.New("invalid model usage state")
		}
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	return s, nil
}
func atomicGatewayJSON(path string, data any) error {
	raw, e := json.Marshal(data)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".model-state-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(raw)
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	if e == nil {
		e = os.Rename(f.Name(), path)
	}
	if e != nil {
		return e
	}
	dir, e := os.Open(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer dir.Close()
	return dir.Sync()
}
func (s *ModelUsageStore) save(call ModelCall) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed != nil {
		return s.failed
	}
	if s.pg != nil && s.pg.err != nil {
		return s.pg.err
	}
	if len(s.data.Calls) >= 10000 && s.data.Calls[call.ID].ID == "" {
		return errors.New("model usage capacity reached")
	}
	next := modelUsageSnapshot{Version: 1, Calls: make(map[string]ModelCall, len(s.data.Calls)+1)}
	for k, v := range s.data.Calls {
		next.Calls[k] = v
	}
	next.Calls[call.ID] = call
	var e error
	if s.pg != nil {
		e = s.pg.save(next)
	} else {
		e = atomicGatewayJSON(s.path, next)
	}
	if e != nil {
		s.failed = e
		return e
	}
	s.data = next
	return nil
}

type ModelUsageSummary struct {
	GatewayID     string `json:"gateway_id"`
	GatewayName   string `json:"gateway_name"`
	ModelConfigID string `json:"model_config_id"`
	ModelName     string `json:"model_name"`
	Model         string `json:"model"`
	Capability    string `json:"capability"`
	Currency      string `json:"currency"`
	Calls         int    `json:"calls"`
	Succeeded     int    `json:"succeeded"`
	Failed        int    `json:"failed"`
	Unknown       int    `json:"unknown"`
	HTTPCalls     int64  `json:"http_calls"`
	DurationMS    int64  `json:"duration_ms"`
	AverageMS     int64  `json:"average_ms"`
	P95MS         int64  `json:"p95_ms"`
	InputTokens   int64  `json:"input_tokens"`
	OutputTokens  int64  `json:"output_tokens"`
	MeteredCalls  int    `json:"metered_calls"`
	MaximumMinor  int64  `json:"maximum_minor"`
}
type ModelUsageReport struct {
	Items      []ModelCall         `json:"items"`
	Summary    []ModelUsageSummary `json:"summary"`
	Total      int                 `json:"total"`
	NextOffset *int                `json:"next_offset"`
}

func (s *ModelUsageStore) report(tenant string, r *http.Request) (ModelUsageReport, error) {
	out := ModelUsageReport{Items: []ModelCall{}, Summary: []ModelUsageSummary{}}
	q := r.URL.Query()
	offset, e := strconv.Atoi(q.Get("offset"))
	if q.Get("offset") == "" {
		offset = 0
		e = nil
	}
	if e != nil || offset < 0 {
		return out, errModelInput
	}
	var from, to time.Time
	for key, target := range map[string]*time.Time{"from": &from, "to": &to} {
		if q.Get(key) != "" {
			v, e := time.Parse(time.RFC3339, q.Get(key))
			if e != nil {
				return out, errModelInput
			}
			*target = v
		}
	}
	if !from.IsZero() && !to.IsZero() && from.After(to) {
		return out, errModelInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed != nil || s.pg != nil && s.pg.err != nil {
		return out, errRuntimeUnavailable
	}
	groups := map[string]*ModelUsageSummary{}
	durations := map[string][]int64{}
	for _, c := range s.data.Calls {
		if c.Tenant != tenant || !from.IsZero() && c.StartedAt.Before(from) || !to.IsZero() && !c.StartedAt.Before(to) || q.Get("gateway_id") != "" && c.GatewayID != q.Get("gateway_id") || q.Get("model_config_id") != "" && c.ModelConfigID != q.Get("model_config_id") || q.Get("feature_key") != "" && c.FeatureKey != q.Get("feature_key") || q.Get("state") != "" && c.State != q.Get("state") {
			continue
		}
		out.Items = append(out.Items, c)
		key := c.GatewayID + ":" + c.ModelConfigID + ":" + c.Capability + ":" + c.Currency
		g := groups[key]
		if g == nil {
			g = &ModelUsageSummary{GatewayID: c.GatewayID, GatewayName: c.GatewayName, ModelConfigID: c.ModelConfigID, ModelName: c.ModelName, Model: c.Model, Capability: c.Capability, Currency: c.Currency}
			groups[key] = g
		}
		g.Calls++
		g.HTTPCalls += c.HTTPCalls
		g.DurationMS += c.DurationMS
		if c.State == "succeeded" {
			g.Succeeded++
		} else if c.State == "failed" {
			g.Failed++
		} else {
			g.Unknown++
		}
		if c.InputTokens != nil {
			g.InputTokens += *c.InputTokens
		}
		if c.OutputTokens != nil {
			g.OutputTokens += *c.OutputTokens
		}
		if c.InputTokens != nil || c.OutputTokens != nil {
			g.MeteredCalls++
		}
		if c.MaximumMinor <= 9007199254740991-g.MaximumMinor {
			g.MaximumMinor += c.MaximumMinor
		} else {
			return out, errors.New("model usage amount exceeds safe display range")
		}
		if c.FinishedAt != nil {
			durations[key] = append(durations[key], c.DurationMS)
		}
	}
	for key, g := range groups {
		ds := durations[key]
		if len(ds) > 0 {
			sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
			g.AverageMS = g.DurationMS / int64(len(ds))
			g.P95MS = ds[(len(ds)*95+99)/100-1]
		}
		out.Summary = append(out.Summary, *g)
	}
	sort.Slice(out.Summary, func(i, j int) bool {
		a, b := out.Summary[i], out.Summary[j]
		return a.GatewayID+a.ModelConfigID+a.Capability+a.Currency < b.GatewayID+b.ModelConfigID+b.Capability+b.Currency
	})
	sort.Slice(out.Items, func(i, j int) bool {
		if out.Items[i].StartedAt.Equal(out.Items[j].StartedAt) {
			return out.Items[i].ID < out.Items[j].ID
		}
		return out.Items[i].StartedAt.After(out.Items[j].StartedAt)
	})
	out.Total = len(out.Items)
	if offset > out.Total {
		offset = out.Total
	}
	end := offset + 100
	if end < out.Total {
		out.NextOffset = &end
	} else {
		end = out.Total
	}
	out.Items = out.Items[offset:end]
	return out, nil
}
