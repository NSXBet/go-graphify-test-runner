package cmd

import (
	"errors"
	"os"
	"strings"
)

// Environment variables for the SystemOne decisions integration. The URL and
// model seed the matching flags' defaults (so a flag still wins); the token has
// no flag — secrets do not belong in shell history or process args.
const (
	envSystemOneURL   = "GOSMARTTESTRUNNER_SYSTEMONE_URL"
	envSystemOneModel = "GOSMARTTESTRUNNER_SYSTEMONE_MODEL"
	envSystemOneToken = "GOSMARTTESTRUNNER_SYSTEMONE_TOKEN"

	// Fallback token sources, used when the explicit token env var is unset.
	envOpenRouterKey = "OPENROUTER_API_KEY"
	envAIHubToken    = "AIHUB_TOKEN"
)

// Defaults: OpenRouter's decisions endpoint and the NSX decision model.
const (
	defaultSystemOneURL   = "https://openrouter.ai/api/alpha/decisions"
	defaultSystemOneModel = "jev-latest"

	// DecisionsPath is appended to a bare base URL (see endpointFromEnv).
	decisionsPath = "/api/alpha/decisions"
)

// endpointFromEnv returns the decisions endpoint: GOSMARTTESTRUNNER_SYSTEMONE_URL
// when set, else the OpenRouter default. A bare base URL (scheme and host, no
// path) gets the standard decisions path appended, so both
// `https://ai-llm-gateway.fbr.land` and a full endpoint URL work.
func endpointFromEnv() string {
	raw := strings.TrimSpace(os.Getenv(envSystemOneURL))
	if raw == "" {
		return defaultSystemOneURL
	}

	return normalizeEndpoint(raw)
}

// normalizeEndpoint appends the decisions path to a bare base URL; a URL that
// already carries a path is returned verbatim (minus a trailing slash).
func normalizeEndpoint(raw string) string {
	trimmed := strings.TrimRight(raw, "/")

	if rest, ok := strings.CutPrefix(trimmed, schemePrefix(trimmed)); ok && !strings.Contains(rest, "/") {
		return trimmed + decisionsPath
	}

	return trimmed
}

// schemePrefix returns the "scheme://" prefix of a URL, or "" when absent.
func schemePrefix(raw string) string {
	i := strings.Index(raw, "://")
	if i < 0 {
		return ""
	}

	return raw[:i+len("://")]
}

// modelFromEnv returns the decision model: GOSMARTTESTRUNNER_SYSTEMONE_MODEL
// when set, else the default.
func modelFromEnv() string {
	if m := strings.TrimSpace(os.Getenv(envSystemOneModel)); m != "" {
		return m
	}

	return defaultSystemOneModel
}

// resolveToken resolves the bearer token, in order:
//
//  1. GOSMARTTESTRUNNER_SYSTEMONE_TOKEN — the explicit override;
//  2. OPENROUTER_API_KEY — the default source;
//  3. AIHUB_TOKEN — so a pre-existing AI Hub set-up keeps working, but only
//     when the endpoint is the AI Hub gateway (it would otherwise be sent to
//     OpenRouter, which it is not valid for).
func resolveToken(endpoint string) (string, error) {
	if t := os.Getenv(envSystemOneToken); t != "" {
		return t, nil
	}

	if t := os.Getenv(envOpenRouterKey); t != "" {
		return t, nil
	}

	if t := os.Getenv(envAIHubToken); t != "" && strings.Contains(endpoint, "ai-llm-gateway.fbr.land") {
		return t, nil
	}

	return "", errors.New(envSystemOneToken + " (or " + envOpenRouterKey + ") not set")
}
