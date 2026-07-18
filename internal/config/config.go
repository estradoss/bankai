package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/estradoss/bankai/internal/auth"
	"github.com/estradoss/bankai/internal/provider"
)

type Config struct {
	Model   string
	DataDir string
	Auth    provider.AuthSource
	// Source describes how we authenticated: "oauth:env" / "oauth:keychain" /
	// "oauth:file" / "api-key".
	Source string
	// OAuth holds the underlying provider when Auth is Bearer-based (nil otherwise).
	OAuth *auth.Provider
}

const DefaultModel = "claude-opus-4-7"

func Load() (*Config, error) {
	model := os.Getenv("BANKAI_MODEL")
	if model == "" {
		model = DefaultModel
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(home, ".bankai")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	cfg := &Config{Model: model, DataDir: dir}

	// Prefer Claude OAuth (subscription) → fall back to ANTHROPIC_API_KEY.
	oauth, _ := auth.NewProvider()
	if oauth != nil {
		cfg.OAuth = oauth
		cfg.Auth = provider.BearerAuth{
			TokenFunc:  oauth.AccessToken,
			BetaHeader: auth.BetaHeader,
		}
		cfg.Source = "oauth:" + oauth.Source()
		return cfg, nil
	}
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		cfg.Auth = provider.APIKeyAuth{Key: key}
		cfg.Source = "api-key"
		return cfg, nil
	}
	return nil, fmt.Errorf("no credentials: set ANTHROPIC_API_KEY or log in via `claude /login` first")
}
