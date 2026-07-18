package voice

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Minimal RFC 6455 WebSocket client, client-side only, no third-party deps —
// consistent with bankai's stdlib-only transport choices (the HTTP+SSE server
// deliberately avoids a ws library). Just enough to drive the Anthropic
// voice_stream STT endpoint: TLS dial + upgrade handshake, masked text/binary
// writes, and unmasked frame reads with continuation + control-frame handling.

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// wsOpcode is a WebSocket frame opcode.
type wsOpcode byte

const (
	opContinuation wsOpcode = 0x0
	opText         wsOpcode = 0x1
	opBinary       wsOpcode = 0x2
	opClose        wsOpcode = 0x8
	opPing         wsOpcode = 0x9
	opPong         wsOpcode = 0xA
)

// wsConn is an open WebSocket connection.
type wsConn struct {
	conn net.Conn
	br   *bufio.Reader
}

// wsDial performs the TLS + HTTP upgrade handshake against a wss:// URL. Extra
// request headers (Authorization, User-Agent, x-app) are sent verbatim.
func wsDial(rawURL string, headers map[string]string, timeout time.Duration) (*wsConn, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "wss" && u.Scheme != "ws" {
		return nil, fmt.Errorf("unsupported websocket scheme %q", u.Scheme)
	}
	host := u.Host
	port := u.Port()
	if port == "" {
		if u.Scheme == "wss" {
			host += ":443"
		} else {
			host += ":80"
		}
	}

	d := &net.Dialer{Timeout: timeout}
	var conn net.Conn
	if u.Scheme == "wss" {
		conn, err = tls.DialWithDialer(d, "tcp", host, &tls.Config{ServerName: u.Hostname()})
	} else {
		conn, err = d.Dial("tcp", host)
	}
	if err != nil {
		return nil, err
	}

	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		conn.Close()
		return nil, err
	}
	key := base64.StdEncoding.EncodeToString(keyBytes)

	reqPath := u.RequestURI()
	var b strings.Builder
	fmt.Fprintf(&b, "GET %s HTTP/1.1\r\n", reqPath)
	fmt.Fprintf(&b, "Host: %s\r\n", u.Host)
	b.WriteString("Upgrade: websocket\r\n")
	b.WriteString("Connection: Upgrade\r\n")
	fmt.Fprintf(&b, "Sec-WebSocket-Key: %s\r\n", key)
	b.WriteString("Sec-WebSocket-Version: 13\r\n")
	for k, v := range headers {
		fmt.Fprintf(&b, "%s: %s\r\n", k, v)
	}
	b.WriteString("\r\n")

	if timeout > 0 {
		conn.SetDeadline(time.Now().Add(timeout))
	}
	if _, err := io.WriteString(conn, b.String()); err != nil {
		conn.Close()
		return nil, err
	}

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: "GET"})
	if err != nil {
		conn.Close()
		return nil, err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		resp.Body.Close()
		conn.Close()
		return nil, fmt.Errorf("websocket upgrade rejected: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	// Verify the accept hash.
	want := computeAccept(key)
	if got := resp.Header.Get("Sec-WebSocket-Accept"); got != want {
		conn.Close()
		return nil, fmt.Errorf("websocket accept mismatch: got %q want %q", got, want)
	}
	conn.SetDeadline(time.Time{}) // clear; caller sets per-op deadlines
	return &wsConn{conn: conn, br: br}, nil
}

func computeAccept(key string) string {
	h := sha1.New()
	io.WriteString(h, key+wsGUID)
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// writeFrame writes one masked client frame (fin=true).
func (c *wsConn) writeFrame(op wsOpcode, payload []byte) error {
	var header []byte
	b0 := byte(0x80) | byte(op) // FIN + opcode
	header = append(header, b0)

	n := len(payload)
	switch {
	case n <= 125:
		header = append(header, byte(0x80)|byte(n)) // MASK bit + len
	case n <= 0xFFFF:
		header = append(header, byte(0x80)|126)
		var ext [2]byte
		binary.BigEndian.PutUint16(ext[:], uint16(n))
		header = append(header, ext[:]...)
	default:
		header = append(header, byte(0x80)|127)
		var ext [8]byte
		binary.BigEndian.PutUint64(ext[:], uint64(n))
		header = append(header, ext[:]...)
	}

	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		return err
	}
	header = append(header, mask[:]...)

	masked := make([]byte, n)
	for i := 0; i < n; i++ {
		masked[i] = payload[i] ^ mask[i%4]
	}

	if _, err := c.conn.Write(header); err != nil {
		return err
	}
	if n > 0 {
		if _, err := c.conn.Write(masked); err != nil {
			return err
		}
	}
	return nil
}

// WriteText sends a text frame.
func (c *wsConn) WriteText(s string) error { return c.writeFrame(opText, []byte(s)) }

// WriteBinary sends a binary frame.
func (c *wsConn) WriteBinary(b []byte) error { return c.writeFrame(opBinary, b) }

// ReadMessage returns the next application (text/binary) message, transparently
// answering pings and coalescing continuation frames. Returns io.EOF on close.
func (c *wsConn) ReadMessage() (wsOpcode, []byte, error) {
	var msgOp wsOpcode
	var buf []byte
	for {
		fin, op, payload, err := c.readFrame()
		if err != nil {
			return 0, nil, err
		}
		switch op {
		case opPing:
			c.writeFrame(opPong, payload)
			continue
		case opPong:
			continue
		case opClose:
			return 0, nil, io.EOF
		case opContinuation:
			buf = append(buf, payload...)
		default:
			msgOp = op
			buf = append(buf, payload...)
		}
		if fin && op != opPing && op != opPong {
			return msgOp, buf, nil
		}
	}
}

func (c *wsConn) readFrame() (fin bool, op wsOpcode, payload []byte, err error) {
	var h [2]byte
	if _, err = io.ReadFull(c.br, h[:]); err != nil {
		return
	}
	fin = h[0]&0x80 != 0
	op = wsOpcode(h[0] & 0x0F)
	masked := h[1]&0x80 != 0
	n := int(h[1] & 0x7F)
	switch n {
	case 126:
		var ext [2]byte
		if _, err = io.ReadFull(c.br, ext[:]); err != nil {
			return
		}
		n = int(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err = io.ReadFull(c.br, ext[:]); err != nil {
			return
		}
		n = int(binary.BigEndian.Uint64(ext[:]))
	}
	var mask [4]byte
	if masked { // servers shouldn't mask, but be tolerant
		if _, err = io.ReadFull(c.br, mask[:]); err != nil {
			return
		}
	}
	payload = make([]byte, n)
	if _, err = io.ReadFull(c.br, payload); err != nil {
		return
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return
}

// SetReadDeadline bounds the next read.
func (c *wsConn) SetReadDeadline(t time.Time) { c.conn.SetReadDeadline(t) }

// Close sends a close frame and tears down the connection.
func (c *wsConn) Close() error {
	c.writeFrame(opClose, nil)
	return c.conn.Close()
}
