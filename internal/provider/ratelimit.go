package provider

import (
	"net/http"
	"sync"
	"time"
)

// RateLimit holds the most recent Anthropic rate-limit / billing headers
// observed on a response. It is the Go port of vibelearn's rate-limit header
// display (src/services/api/): the API returns anthropic-ratelimit-* headers
// that surface remaining request/token budget and reset times.
type RateLimit struct {
	mu sync.Mutex

	seen bool
	at   time.Time

	RequestsLimit     string
	RequestsRemaining string
	RequestsReset     string
	TokensLimit       string
	TokensRemaining   string
	TokensReset       string
	// Unified* are the combined input+output budget headers Anthropic sends on
	// OAuth/Claude-Code routed requests.
	UnifiedLimit     string
	UnifiedRemaining string
	UnifiedReset     string
	RetryAfter       string
}

// capture reads rate-limit headers from an HTTP response, replacing prior
// values. Header lookups are case-insensitive (http.Header canonicalizes).
func (r *RateLimit) capture(h http.Header) {
	get := func(k string) string { return h.Get(k) }
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = true
	r.at = timeNow()
	r.RequestsLimit = get("anthropic-ratelimit-requests-limit")
	r.RequestsRemaining = get("anthropic-ratelimit-requests-remaining")
	r.RequestsReset = get("anthropic-ratelimit-requests-reset")
	r.TokensLimit = get("anthropic-ratelimit-tokens-limit")
	r.TokensRemaining = get("anthropic-ratelimit-tokens-remaining")
	r.TokensReset = get("anthropic-ratelimit-tokens-reset")
	r.UnifiedLimit = get("anthropic-ratelimit-unified-limit")
	r.UnifiedRemaining = get("anthropic-ratelimit-unified-remaining")
	r.UnifiedReset = get("anthropic-ratelimit-unified-reset")
	r.RetryAfter = get("retry-after")
}

// Snapshot copies the current values out under lock.
func (r *RateLimit) Snapshot() RateLimitSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return RateLimitSnapshot{
		Seen:              r.seen,
		At:                r.at,
		RequestsLimit:     r.RequestsLimit,
		RequestsRemaining: r.RequestsRemaining,
		RequestsReset:     r.RequestsReset,
		TokensLimit:       r.TokensLimit,
		TokensRemaining:   r.TokensRemaining,
		TokensReset:       r.TokensReset,
		UnifiedLimit:      r.UnifiedLimit,
		UnifiedRemaining:  r.UnifiedRemaining,
		UnifiedReset:      r.UnifiedReset,
		RetryAfter:        r.RetryAfter,
	}
}

// RateLimitSnapshot is an immutable copy of RateLimit for display.
type RateLimitSnapshot struct {
	Seen              bool
	At                time.Time
	RequestsLimit     string
	RequestsRemaining string
	RequestsReset     string
	TokensLimit       string
	TokensRemaining   string
	TokensReset       string
	UnifiedLimit      string
	UnifiedRemaining  string
	UnifiedReset      string
	RetryAfter        string
}

// timeNow is a seam for testing.
var timeNow = time.Now
