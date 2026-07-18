package voice

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
)

// Recorder captures microphone audio to a file for a bounded duration. Record
// returns the audio path and a cleanup func to remove the temp file.
type Recorder interface {
	Record(ctx context.Context, seconds int) (audioPath string, cleanup func(), err error)
}

// CLIRecorder captures audio via the first available recorder CLI (arecord on
// Linux/ALSA, sox's `rec`, or ffmpeg). It fails gracefully when none is present.
type CLIRecorder struct{}

func (CLIRecorder) Record(ctx context.Context, seconds int) (string, func(), error) {
	if seconds <= 0 {
		seconds = 5
	}
	tmp, err := os.CreateTemp("", "bankai-dictate-*.wav")
	if err != nil {
		return "", nil, err
	}
	path := tmp.Name()
	_ = tmp.Close()
	cleanup := func() { _ = os.Remove(path) }

	// Candidate recorder command lines, in preference order.
	dur := strconv.Itoa(seconds)
	candidates := [][]string{
		{"arecord", "-q", "-f", "cd", "-d", dur, path},
		{"rec", "-q", path, "trim", "0", dur}, // sox
		{"ffmpeg", "-y", "-f", "alsa", "-i", "default", "-t", dur, path},
	}
	for _, c := range candidates {
		if _, err := exec.LookPath(c[0]); err != nil {
			continue
		}
		cmd := exec.CommandContext(ctx, c[0], c[1:]...)
		if err := cmd.Run(); err != nil {
			cleanup()
			return "", nil, fmt.Errorf("%s failed: %w", c[0], err)
		}
		return path, cleanup, nil
	}
	cleanup()
	return "", nil, fmt.Errorf("no audio recorder found (install alsa-utils/arecord, sox, or ffmpeg)")
}

// Dictate records `seconds` of microphone audio and transcribes it (applying
// keyterms and appending to the buffer). This is the push-to-talk / dictation
// entry point. A nil recorder uses the CLI recorder.
func (s *Session) Dictate(ctx context.Context, rec Recorder, seconds int) (string, error) {
	if rec == nil {
		rec = CLIRecorder{}
	}
	path, cleanup, err := rec.Record(ctx, seconds)
	if err != nil {
		return "", err
	}
	if cleanup != nil {
		defer cleanup()
	}
	return s.Transcribe(ctx, path)
}
