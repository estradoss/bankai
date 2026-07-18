package voice

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// Go port of vibelearn's voiceStreamSTT.ts — a real-time push-to-talk STT
// client over Anthropic's voice_stream WebSocket endpoint. The wire protocol is
// JSON control messages ({"type":"KeepAlive"}, {"type":"CloseStream"}) plus
// binary linear16/16kHz/mono audio frames; the server replies with
// TranscriptText (interim), TranscriptEndpoint (utterance end) and
// TranscriptError JSON messages. Held-key semantics: stream audio while held,
// Finalize() on release to flush the last transcript.
//
// This replaces the "batch push-to-talk only" limitation noted in the roadmap:
// bankai now has both whisper batch dictation and streaming voice_stream.

const (
	voiceStreamPath      = "/api/ws/speech_to_text/voice_stream"
	keepAliveMsg         = `{"type":"KeepAlive"}`
	closeStreamMsg       = `{"type":"CloseStream"}`
	keepAliveIntervalMS  = 8 * time.Second
	finalizeSafetyMS     = 5 * time.Second
	finalizeNoDataMS     = 1500 * time.Millisecond
	defaultVoiceBaseHost = "wss://api.anthropic.com"
)

// StreamCallbacks receive live transcription events.
type StreamCallbacks struct {
	OnTranscript func(text string, final bool)
	OnError      func(msg string, fatal bool)
	OnClose      func()
}

// StreamOptions tune the connection.
type StreamOptions struct {
	Language string
	Keyterms []string
	// BaseURL overrides the wss host (env VOICE_STREAM_BASE_URL wins over this).
	BaseURL string
}

// StreamConn is a live voice_stream connection.
type StreamConn struct {
	ws *wsConn

	mu         sync.Mutex
	finalized  bool // CloseStream sent — further audio dropped
	finalizing bool
	closed     bool
	lastText   string

	finalizeCh   chan string
	resolveOnce  sync.Once
	cancelNoData chan struct{}
	cb           StreamCallbacks
}

// ConnectVoiceStream opens the WebSocket, starts the read + keepalive loops, and
// invokes callbacks. accessToken is the Anthropic OAuth bearer token. Returns an
// error if the upgrade fails (4xx are fatal — same token won't succeed on retry).
func ConnectVoiceStream(ctx context.Context, accessToken string, cb StreamCallbacks, opts StreamOptions) (*StreamConn, error) {
	if accessToken == "" {
		return nil, fmt.Errorf("voice_stream requires an Anthropic OAuth access token")
	}
	base := os.Getenv("VOICE_STREAM_BASE_URL")
	if base == "" {
		base = opts.BaseURL
	}
	if base == "" {
		base = defaultVoiceBaseHost
	}
	base = strings.Replace(base, "https://", "wss://", 1)
	base = strings.Replace(base, "http://", "ws://", 1)
	base = strings.TrimRight(base, "/")

	lang := opts.Language
	if lang == "" {
		lang = "en"
	}
	q := url.Values{}
	q.Set("encoding", "linear16")
	q.Set("sample_rate", "16000")
	q.Set("channels", "1")
	q.Set("endpointing_ms", "300")
	q.Set("utterance_end_ms", "1000")
	q.Set("language", lang)
	for _, kt := range opts.Keyterms {
		if kt != "" {
			q.Add("keyterms", kt)
		}
	}
	full := base + voiceStreamPath + "?" + q.Encode()

	headers := map[string]string{
		"Authorization": "Bearer " + accessToken,
		"User-Agent":    "bankai-cli",
		"x-app":         "cli",
	}
	ws, err := wsDial(full, headers, 15*time.Second)
	if err != nil {
		fatal := strings.Contains(err.Error(), "rejected") // 4xx upgrade rejections
		if cb.OnError != nil {
			cb.OnError(err.Error(), fatal)
		}
		return nil, err
	}

	c := &StreamConn{
		ws:         ws,
		finalizeCh: make(chan string, 1),
		cb:         cb,
	}

	// Immediate KeepAlive: audio init can take >1s; keep the server from
	// dropping the connection before capture starts.
	_ = ws.WriteText(keepAliveMsg)

	go c.keepAliveLoop()
	go c.readLoop()
	return c, nil
}

func (c *StreamConn) keepAliveLoop() {
	t := time.NewTicker(keepAliveIntervalMS)
	defer t.Stop()
	for range t.C {
		c.mu.Lock()
		closed := c.closed
		c.mu.Unlock()
		if closed {
			return
		}
		if err := c.ws.WriteText(keepAliveMsg); err != nil {
			return
		}
	}
}

type streamMsg struct {
	Type        string `json:"type"`
	Data        string `json:"data"`
	ErrorCode   string `json:"error_code"`
	Description string `json:"description"`
	Message     string `json:"message"`
}

func (c *StreamConn) readLoop() {
	for {
		op, payload, err := c.ws.ReadMessage()
		if err != nil {
			c.onWSClose()
			return
		}
		if op != opText {
			continue
		}
		var m streamMsg
		if json.Unmarshal(payload, &m) != nil {
			continue
		}
		switch m.Type {
		case "TranscriptText":
			c.handleTranscript(m.Data)
		case "TranscriptEndpoint":
			c.handleEndpoint()
		case "TranscriptError":
			desc := m.Description
			if desc == "" {
				desc = m.ErrorCode
			}
			if desc == "" {
				desc = "unknown transcription error"
			}
			c.emitError(desc)
		case "error":
			detail := m.Message
			if detail == "" {
				detail = string(payload)
			}
			c.emitError(detail)
		}
	}
}

func (c *StreamConn) handleTranscript(transcript string) {
	c.mu.Lock()
	// Data arrived after CloseStream — disarm the no-data timer so a
	// slow-but-real flush isn't cut off.
	if c.finalized && c.cancelNoData != nil {
		close(c.cancelNoData)
		c.cancelNoData = nil
	}
	if transcript == "" {
		c.mu.Unlock()
		return
	}
	// New-segment detection: if neither prev nor next is a prefix of the
	// other, the server moved to a new utterance — promote the previous
	// text as final so it isn't overwritten.
	prev := strings.TrimLeft(c.lastText, " ")
	next := strings.TrimLeft(transcript, " ")
	promote := ""
	if prev != "" && next != "" && !strings.HasPrefix(next, prev) && !strings.HasPrefix(prev, next) {
		promote = c.lastText
	}
	c.lastText = transcript
	cb := c.cb
	c.mu.Unlock()

	if promote != "" && cb.OnTranscript != nil {
		cb.OnTranscript(promote, true)
	}
	if cb.OnTranscript != nil {
		cb.OnTranscript(transcript, false) // interim
	}
}

func (c *StreamConn) handleEndpoint() {
	c.mu.Lock()
	final := c.lastText
	c.lastText = ""
	finalized := c.finalized
	cb := c.cb
	c.mu.Unlock()

	if final != "" && cb.OnTranscript != nil {
		cb.OnTranscript(final, true)
	}
	if finalized {
		c.resolve("post_closestream_endpoint")
	}
}

func (c *StreamConn) emitError(msg string) {
	c.mu.Lock()
	suppress := c.finalizing
	cb := c.cb
	c.mu.Unlock()
	if !suppress && cb.OnError != nil {
		cb.OnError(msg, false)
	}
}

func (c *StreamConn) onWSClose() {
	c.mu.Lock()
	c.closed = true
	final := c.lastText
	c.lastText = ""
	cb := c.cb
	c.mu.Unlock()
	// Promote any unreported interim so no text is lost.
	if final != "" && cb.OnTranscript != nil {
		cb.OnTranscript(final, true)
	}
	c.resolve("ws_close")
	if cb.OnClose != nil {
		cb.OnClose()
	}
}

// Send streams a binary audio frame (linear16 PCM). Dropped after Finalize.
func (c *StreamConn) Send(audio []byte) {
	c.mu.Lock()
	if c.finalized || c.closed {
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	_ = c.ws.WriteBinary(audio)
}

// Finalize sends CloseStream and waits for the server's final flush (or a
// timeout). Returns how it resolved. Safe to call once; further calls return
// "ws_already_closed".
func (c *StreamConn) Finalize() string {
	c.mu.Lock()
	if c.finalizing || c.finalized {
		c.mu.Unlock()
		return "ws_already_closed"
	}
	c.finalizing = true
	if c.closed {
		c.mu.Unlock()
		c.resolve("ws_already_closed")
		return <-c.finalizeCh
	}
	c.cancelNoData = make(chan struct{})
	cancelNoData := c.cancelNoData
	c.finalized = true
	c.mu.Unlock()

	// Tell the server we're done sending audio.
	_ = c.ws.WriteText(closeStreamMsg)

	safety := time.NewTimer(finalizeSafetyMS)
	noData := time.NewTimer(finalizeNoDataMS)
	defer safety.Stop()
	defer noData.Stop()

	select {
	case src := <-c.finalizeCh:
		return src
	case <-safety.C:
		c.resolve("safety_timeout")
		return <-c.finalizeCh
	case <-noData.C:
		c.resolve("no_data_timeout")
		return <-c.finalizeCh
	case <-cancelNoData:
		// no-data disarmed (real data arriving); fall back to safety/endpoint.
		select {
		case src := <-c.finalizeCh:
			return src
		case <-safety.C:
			c.resolve("safety_timeout")
			return <-c.finalizeCh
		}
	}
}

func (c *StreamConn) resolve(source string) {
	c.resolveOnce.Do(func() {
		// Promote a trailing interim before resolving so it isn't lost.
		c.mu.Lock()
		final := c.lastText
		c.lastText = ""
		cb := c.cb
		c.mu.Unlock()
		if final != "" && cb.OnTranscript != nil {
			cb.OnTranscript(final, true)
		}
		c.finalizeCh <- source
	})
}

// Close tears down the connection.
func (c *StreamConn) Close() {
	c.mu.Lock()
	c.finalized = true
	c.closed = true
	c.mu.Unlock()
	_ = c.ws.Close()
}
