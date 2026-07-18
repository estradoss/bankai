// Package voice implements a dictation/transcription subsystem — the Go slice of
// vibelearn's src/services/voice*. It manages keyterms (domain words the STT
// pass should bias toward and that post-processing snaps near-misses onto) and a
// dictation buffer, and turns audio into text through an injectable Transcriber
// (default: the `whisper` CLI when present). Audio capture / push-to-talk wiring
// lives in the host; this package is the transcription + keyterm core.
package voice

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
)

// Transcriber turns an audio file into text.
type Transcriber interface {
	Transcribe(ctx context.Context, audioPath string) (string, error)
}

// Session holds keyterms and an accumulated dictation buffer.
type Session struct {
	mu       sync.Mutex
	keyterms []string
	buffer   []string
	tr       Transcriber
}

// NewSession builds a session using the given transcriber (nil → whisper CLI).
func NewSession(tr Transcriber) *Session {
	if tr == nil {
		tr = WhisperTranscriber{}
	}
	return &Session{tr: tr}
}

// AddKeyterm registers a domain term to bias transcription toward.
func (s *Session) AddKeyterm(term string) {
	term = strings.TrimSpace(term)
	if term == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range s.keyterms {
		if strings.EqualFold(k, term) {
			return
		}
	}
	s.keyterms = append(s.keyterms, term)
}

// Keyterms returns the current keyterm list.
func (s *Session) Keyterms() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.keyterms...)
}

// Transcribe converts an audio file to text, applies keyterm correction, and
// appends the result to the dictation buffer.
func (s *Session) Transcribe(ctx context.Context, audioPath string) (string, error) {
	raw, err := s.tr.Transcribe(ctx, audioPath)
	if err != nil {
		return "", err
	}
	corrected := s.applyKeyterms(raw)
	s.mu.Lock()
	s.buffer = append(s.buffer, corrected)
	s.mu.Unlock()
	return corrected, nil
}

// Buffer returns the accumulated dictation text.
func (s *Session) Buffer() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.buffer, " ")
}

// ClearBuffer empties the dictation buffer.
func (s *Session) ClearBuffer() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buffer = nil
}

// applyKeyterms snaps each word that is a near-miss (edit distance ≤ threshold)
// of a keyterm onto the keyterm's canonical spelling, preserving other words.
func (s *Session) applyKeyterms(text string) string {
	s.mu.Lock()
	terms := append([]string(nil), s.keyterms...)
	s.mu.Unlock()
	if len(terms) == 0 {
		return text
	}
	words := strings.Fields(text)
	for i, w := range words {
		lw := strings.ToLower(strings.Trim(w, ".,!?;:"))
		if lw == "" {
			continue
		}
		for _, term := range terms {
			lt := strings.ToLower(term)
			if lw == lt {
				break // already correct
			}
			// Only correct words of comparable length within a small edit budget.
			budget := 1
			if len(lt) >= 8 {
				budget = 2
			}
			if abs(len(lw)-len(lt)) <= budget && levenshtein(lw, lt) <= budget {
				words[i] = restoreTrailingPunct(w, term)
				break
			}
		}
	}
	return strings.Join(words, " ")
}

func restoreTrailingPunct(original, replacement string) string {
	trail := ""
	for i := len(original) - 1; i >= 0; i-- {
		if strings.ContainsRune(".,!?;:", rune(original[i])) {
			trail = string(original[i]) + trail
		} else {
			break
		}
	}
	return replacement + trail
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// levenshtein computes the edit distance between two strings.
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min3(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}

// WhisperTranscriber shells out to a `whisper` CLI (openai-whisper or
// whisper.cpp's `whisper-cli`). It fails gracefully when none is installed.
type WhisperTranscriber struct{}

func (WhisperTranscriber) Transcribe(ctx context.Context, audioPath string) (string, error) {
	for _, bin := range []string{"whisper-cli", "whisper"} {
		if path, err := exec.LookPath(bin); err == nil {
			out, err := exec.CommandContext(ctx, path, audioPath, "--output-txt", "-").CombinedOutput()
			if err != nil {
				return "", fmt.Errorf("%s failed: %s", bin, strings.TrimSpace(string(out)))
			}
			return strings.TrimSpace(string(out)), nil
		}
	}
	return "", fmt.Errorf("no speech-to-text backend found (install whisper or whisper.cpp)")
}
