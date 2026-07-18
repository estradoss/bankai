package voice

import (
	"bufio"
	"net"
	"testing"
)

func TestComputeAccept(t *testing.T) {
	// RFC 6455 §1.3 worked example.
	if got := computeAccept("dGhlIHNhbXBsZSBub25jZQ=="); got != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" {
		t.Fatalf("computeAccept = %q", got)
	}
}

func pair() (*wsConn, *wsConn) {
	a, b := net.Pipe()
	return &wsConn{conn: a, br: bufio.NewReader(a)}, &wsConn{conn: b, br: bufio.NewReader(b)}
}

func TestFrameRoundTripText(t *testing.T) {
	c1, c2 := pair()
	defer c1.conn.Close()
	defer c2.conn.Close()

	go func() { _ = c1.WriteText(`{"type":"KeepAlive"}`) }()
	op, payload, err := c2.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if op != opText || string(payload) != `{"type":"KeepAlive"}` {
		t.Fatalf("op=%d payload=%q", op, payload)
	}
}

func TestFrameRoundTripBinaryLarge(t *testing.T) {
	c1, c2 := pair()
	defer c1.conn.Close()
	defer c2.conn.Close()

	data := make([]byte, 3200) // one 100ms PCM frame
	for i := range data {
		data[i] = byte(i % 251)
	}
	go func() { _ = c1.WriteBinary(data) }()
	op, payload, err := c2.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if op != opBinary || len(payload) != len(data) {
		t.Fatalf("op=%d len=%d", op, len(payload))
	}
	for i := range data {
		if payload[i] != data[i] {
			t.Fatalf("byte %d mismatch", i)
		}
	}
}

func TestPingAnswered(t *testing.T) {
	c1, c2 := pair()
	defer c1.conn.Close()
	defer c2.conn.Close()

	// c1 sends ping then text; c2.ReadMessage should skip ping, answer pong,
	// and return the text. Read the pong on c1 concurrently.
	go func() {
		_ = c1.writeFrame(opPing, []byte("hi"))
		_ = c1.WriteText("done")
	}()
	go func() { _, _, _ = c1.ReadMessage() }() // consume the pong

	op, payload, err := c2.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if op != opText || string(payload) != "done" {
		t.Fatalf("op=%d payload=%q", op, payload)
	}
}
