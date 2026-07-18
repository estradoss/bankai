package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/estradoss/bankai/internal/auth"
	"github.com/estradoss/bankai/internal/codex"
	"github.com/estradoss/bankai/internal/provider"
)

type Config struct {
	Model   string
	DataDir string
	Auth    provider.AuthSource
	// Source describes how we authenticated: "oauth:env" / "oauth:keychain" /
	// "oauth:file" / "api-key" / "codex" / "foundry".
	Source string
	// OAuth holds the underlying provider when Auth is Bearer-based (nil otherwise).
	OAuth *auth.Provider
	// Codex, when non-nil, selects the OpenAI Codex backend (Responses API).
	Codex *codex.Provider
}

const DefaultModel = "claude-opus-4-7"
const DefaultCodexModel = provider.DefaultCodexModel

func Load() (*Config, error) {
	model := os.Getenv("BANKAI_MODEL")
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(home, ".bankai")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	cfg := &Config{DataDir: dir}

	// OpenAI Codex backend (subscription OAuth).
	if os.Getenv("CLAUDE_CODE_USE_OPENAI") == "1" {
		cx := codex.NewProvider()
		if cx == nil {
			return nil, fmt.Errorf("CLAUDE_CODE_USE_OPENAI=1 but no Codex credentials — run `bankai codex login` first")
		}
		if model == "" {
			model = DefaultCodexModel
		}
		cfg.Model = model
		cfg.Codex = cx
		cfg.Source = "codex"
		// Auth is unused on the Codex path but must be non-nil for the client.
		cfg.Auth = provider.APIKeyAuth{Key: "unused-codex"}
		return cfg, nil
	}

	if model == "" {
		model = DefaultModel
	}
	cfg.Model = model

	// Anthropic Foundry (API key + custom base URL via ANTHROPIC_BASE_URL).
	if os.Getenv("CLAUDE_CODE_USE_FOUNDRY") == "1" {
		key := os.Getenv("ANTHROPIC_FOUNDRY_API_KEY")
		if key == "" {
			return nil, fmt.Errorf("CLAUDE_CODE_USE_FOUNDRY=1 but ANTHROPIC_FOUNDRY_API_KEY not set")
		}
		cfg.Auth = provider.APIKeyAuth{Key: key}
		cfg.Source = "foundry"
		return cfg, nil
	}

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
	return nil, fmt.Errorf("no credentials: set ANTHROPIC_API_KEY, run `bankai codex login`, or log in via `claude /login` first")
}
