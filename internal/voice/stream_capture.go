package voice

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// StreamDictate is the streaming counterpart to Dictate: instead of recording a
// WAV file then batch-transcribing with whisper, it captures raw linear16 PCM
// and streams it to Anthropic's voice_stream endpoint in real time, then
// returns the finalized transcript (keyterm-corrected, appended to the buffer).
//
// accessToken is an Anthropic OAuth bearer token. `seconds` bounds the capture
// window (push-to-talk style). Passing a nil PCMSource uses the CLI capture
// (arecord/rec/ffmpeg, raw s16le 16kHz mono).
func (s *Session) StreamDictate(ctx context.Context, accessToken string, src PCMSource, seconds int) (string, error) {
	if seconds <= 0 {
		seconds = 8
	}
	if src == nil {
		src = CLIPCMSource{}
	}

	var mu sync.Mutex
	var finals []string
	cb := StreamCallbacks{
		OnTranscript: func(text string, final bool) {
			if !final {
				return
			}
			t := strings.TrimSpace(text)
			if t == "" {
				return
			}
			mu.Lock()
			finals = append(finals, t)
			mu.Unlock()
		},
	}

	conn, err := ConnectVoiceStream(ctx, accessToken, cb, StreamOptions{Keyterms: s.Keyterms()})
	if err != nil {
		return "", err
	}
	defer conn.Close()

	capCtx, cancel := context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
	defer cancel()

	frames, errc, err := src.Stream(capCtx)
	if err != nil {
		return "", err
	}
	for frame := range frames {
		conn.Send(frame)
	}
	if e := <-errc; e != nil && capCtx.Err() == nil {
		// Capture ended abnormally (not just the timeout window closing).
		return "", e
	}

	conn.Finalize()

	mu.Lock()
	joined := strings.Join(finals, " ")
	mu.Unlock()
	if joined == "" {
		return "", fmt.Errorf("no speech transcribed")
	}
	corrected := s.applyKeyterms(joined)
	s.mu.Lock()
	s.buffer = append(s.buffer, corrected)
	s.mu.Unlock()
	return corrected, nil
}

// PCMSource yields raw linear16 (s16le) 16kHz mono audio frames over a channel.
type PCMSource interface {
	Stream(ctx context.Context) (<-chan []byte, <-chan error, error)
}

// CLIPCMSource captures raw PCM from the first available recorder CLI, writing
// s16le/16kHz/mono to stdout and chunking it into ~100ms frames.
type CLIPCMSource struct{}

func (CLIPCMSource) Stream(ctx context.Context) (<-chan []byte, <-chan error, error) {
	// Candidate raw-PCM capture commands (stdout, s16le, 16kHz, mono).
	candidates := [][]string{
		{"arecord", "-q", "-t", "raw", "-f", "S16_LE", "-r", "16000", "-c", "1"},
		{"rec", "-q", "-t", "raw", "-b", "16", "-e", "signed-integer", "-r", "16000", "-c", "1", "-"}, // sox
		{"ffmpeg", "-hide_banner", "-loglevel", "quiet", "-f", "alsa", "-i", "default", "-ar", "16000", "-ac", "1", "-f", "s16le", "-"},
	}
	var cmd *exec.Cmd
	for _, c := range candidates {
		if _, err := exec.LookPath(c[0]); err == nil {
			cmd = exec.CommandContext(ctx, c[0], c[1:]...)
			break
		}
	}
	if cmd == nil {
		return nil, nil, fmt.Errorf("no raw-PCM recorder found (install alsa-utils/arecord, sox, or ffmpeg)")
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}

	frames := make(chan []byte, 16)
	errc := make(chan error, 1)
	go func() {
		defer close(frames)
		// 100ms @ 16kHz, 16-bit mono = 16000 * 0.1 * 2 = 3200 bytes.
		buf := make([]byte, 3200)
		for {
			n, rerr := io.ReadFull(stdout, buf)
			if n > 0 {
				frame := make([]byte, n)
				copy(frame, buf[:n])
				select {
				case frames <- frame:
				case <-ctx.Done():
					_ = cmd.Process.Kill()
					errc <- nil
					_ = cmd.Wait()
					return
				}
			}
			if rerr != nil {
				// EOF / ErrUnexpectedEOF at capture end is normal; ctx timeout
				// kills the process. Report only genuine errors.
				_ = cmd.Process.Kill()
				if ctx.Err() != nil {
					errc <- nil
				} else if rerr == io.EOF || rerr == io.ErrUnexpectedEOF {
					errc <- nil
				} else {
					errc <- rerr
				}
				_ = cmd.Wait()
				return
			}
		}
	}()
	return frames, errc, nil
}
