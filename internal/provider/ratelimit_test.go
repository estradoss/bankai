package provider

import (
	"net/http"
	"testing"
)

func TestRateLimitCaptureAndSnapshot(t *testing.T) {
	var rl RateLimit
	if rl.Snapshot().Seen {
		t.Fatal("empty RateLimit should not report seen")
	}
	h := http.Header{}
	h.Set("anthropic-ratelimit-requests-limit", "1000")
	h.Set("anthropic-ratelimit-requests-remaining", "999")
	h.Set("anthropic-ratelimit-tokens-remaining", "50000")
	h.Set("retry-after", "12")
	rl.capture(h)

	s := rl.Snapshot()
	if !s.Seen {
		t.Fatal("should be seen after capture")
	}
	if s.RequestsLimit != "1000" || s.RequestsRemaining != "999" {
		t.Fatalf("requests headers: %+v", s)
	}
	if s.TokensRemaining != "50000" || s.RetryAfter != "12" {
		t.Fatalf("token/retry headers: %+v", s)
	}
}
