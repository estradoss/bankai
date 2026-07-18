package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/estradoss/bankai/internal/voice"
)

// TranscribeTool converts an audio file to text via the voice session's STT
// backend, applying registered keyterms. Ported from vibelearn's voice
// dictation. Optional keyterms in the call are registered before transcribing.
type TranscribeTool struct{ Session *voice.Session }

func (TranscribeTool) Name() string { return "transcribe" }

func (TranscribeTool) Description() string {
	return "Transcribe an audio file to text using the local speech-to-text backend. Optionally pass keyterms (domain words) to bias recognition and snap near-misses onto. Errors if no STT backend is installed."
}

func (TranscribeTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"audio_path": {"type": "string", "description": "Absolute path to the audio file"},
			"keyterms": {"type": "array", "items": {"type": "string"}, "description": "Optional domain terms to bias toward"}
		},
		"required": ["audio_path"]
	}`)
}

func (t TranscribeTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	if t.Session == nil {
		return Result{IsError: true, Output: "voice session not configured"}, nil
	}
	var in struct {
		AudioPath string   `json:"audio_path"`
		Keyterms  []string `json:"keyterms"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{IsError: true, Output: fmt.Sprintf("bad input: %v", err)}, nil
	}
	if in.AudioPath == "" {
		return Result{IsError: true, Output: "audio_path is required"}, nil
	}
	if !filepath.IsAbs(in.AudioPath) {
		return Result{IsError: true, Output: "audio_path must be absolute"}, nil
	}
	for _, k := range in.Keyterms {
		t.Session.AddKeyterm(k)
	}
	text, err := t.Session.Transcribe(ctx, in.AudioPath)
	if err != nil {
		return Result{IsError: true, Output: "transcription failed: " + err.Error()}, nil
	}
	if strings.TrimSpace(text) == "" {
		return Result{Output: "(no speech detected)"}, nil
	}
	return Result{Output: text}, nil
}

// StreamTranscribeTool captures live microphone audio and transcribes it in
// real time over Anthropic's voice_stream WebSocket endpoint (the streaming
// counterpart to `transcribe`, which is batch/file-based). Requires Anthropic
// OAuth auth — Token yields a fresh bearer token.
type StreamTranscribeTool struct {
	Session *voice.Session
	Token   func() (string, error)
}

func (StreamTranscribeTool) Name() string { return "transcribe_stream" }

func (StreamTranscribeTool) Description() string {
	return "Record microphone audio for N seconds and transcribe it live via Anthropic's real-time voice_stream STT. Push-to-talk style: captures a bounded window then returns the finalized transcript (keyterm-corrected). Requires Anthropic OAuth and a raw-PCM recorder (arecord/sox/ffmpeg)."
}

func (StreamTranscribeTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"seconds": {"type": "integer", "description": "Capture window in seconds (default 8)"},
			"keyterms": {"type": "array", "items": {"type": "string"}, "description": "Optional domain terms to bias toward"}
		}
	}`)
}

func (t StreamTranscribeTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	if t.Session == nil || t.Token == nil {
		return Result{IsError: true, Output: "streaming voice not configured (requires Anthropic OAuth)"}, nil
	}
	var in struct {
		Seconds  int      `json:"seconds"`
		Keyterms []string `json:"keyterms"`
	}
	if len(input) > 0 {
		if err := json.Unmarshal(input, &in); err != nil {
			return Result{IsError: true, Output: fmt.Sprintf("bad input: %v", err)}, nil
		}
	}
	for _, k := range in.Keyterms {
		t.Session.AddKeyterm(k)
	}
	tok, err := t.Token()
	if err != nil {
		return Result{IsError: true, Output: "could not get OAuth token: " + err.Error()}, nil
	}
	text, err := t.Session.StreamDictate(ctx, tok, nil, in.Seconds)
	if err != nil {
		return Result{IsError: true, Output: "streaming transcription failed: " + err.Error()}, nil
	}
	return Result{Output: text}, nil
}
