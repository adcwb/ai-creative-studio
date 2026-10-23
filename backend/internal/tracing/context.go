// Package tracing propagates correlation only. It never supplies identity,
// tenant ownership or execution authorization, and does not forward baggage.
package tracing

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"sync/atomic"
)

type parentKey struct{}
type tracerKey struct{}

var defaultTracer atomic.Pointer[Tracer]

// ValidParent accepts the supported W3C version 00 format only. Unsupported
// versions/flags and malformed or all-zero identifiers start a fresh trace.
func ValidParent(value string) bool {
	if len(value) != 55 || value[:3] != "00-" || value[35] != '-' || value[52] != '-' || (value[53:] != "00" && value[53:] != "01") {
		return false
	}
	for _, part := range []string{value[3:35], value[36:52]} {
		if strings.Trim(part, "0") == "" {
			return false
		}
		for _, c := range part {
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
				return false
			}
		}
	}
	return true
}
func WithParent(ctx context.Context, value string) context.Context {
	if !ValidParent(value) {
		value = ""
	}
	return context.WithValue(ctx, parentKey{}, value)
}
func Parent(ctx context.Context) string {
	value, _ := ctx.Value(parentKey{}).(string)
	if !ValidParent(value) {
		return ""
	}
	return value
}
func WithTracer(ctx context.Context, tracer *Tracer) context.Context {
	return context.WithValue(ctx, tracerKey{}, tracer)
}
func SetDefault(tracer *Tracer) { defaultTracer.Store(tracer) }
func Current(ctx context.Context) *Tracer {
	if t, ok := ctx.Value(tracerKey{}).(*Tracer); ok {
		return t
	}
	return defaultTracer.Load()
}
func DefaultStats() Stats {
	if t := defaultTracer.Load(); t != nil {
		return t.Stats()
	}
	return Stats{}
}
func randomID(size int) string {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return ""
	}
	return hex.EncodeToString(data)
}
