package session

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"time"
)

// Session is a running conversation with its own id + dir on disk.
type Session struct {
	ID        string
	Dir       string
	StartedAt time.Time
}

// New creates a fresh session directory under dataDir/sessions/<id>.
func New(dataDir string) (*Session, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return nil, err
	}
	id := hex.EncodeToString(buf[:])
	dir := filepath.Join(dataDir, "sessions", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Session{ID: id, Dir: dir, StartedAt: time.Now()}, nil
}
