package cmd

import (
	"strings"
	"testing"
)

func TestEndpointFromEnv(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want string
	}{
		{"unset falls back to the OpenRouter default", "", defaultSystemOneURL},
		{"full endpoint used verbatim", "https://x.example/api/alpha/decisions", "https://x.example/api/alpha/decisions"},
		{"bare base URL gets the decisions path", "https://ai-llm-gateway.fbr.land", "https://ai-llm-gateway.fbr.land/api/alpha/decisions"},
		{"bare base URL with trailing slash", "https://ai-llm-gateway.fbr.land/", "https://ai-llm-gateway.fbr.land/api/alpha/decisions"},
		{"trailing slash trimmed on a full URL", "https://x.example/api/alpha/decisions/", "https://x.example/api/alpha/decisions"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(envSystemOneURL, tt.env)

			if got := endpointFromEnv(); got != tt.want {
				t.Fatalf("endpointFromEnv() = %q want %q", got, tt.want)
			}
		})
	}
}

func TestModelFromEnv(t *testing.T) {
	t.Setenv(envSystemOneModel, "")

	if got := modelFromEnv(); got != defaultSystemOneModel {
		t.Fatalf("default model = %q want %q", got, defaultSystemOneModel)
	}

	t.Setenv(envSystemOneModel, "custom-model")

	if got := modelFromEnv(); got != "custom-model" {
		t.Fatalf("env model = %q want custom-model", got)
	}

	// A whitespace-only value must not shadow the default.
	t.Setenv(envSystemOneModel, "   ")

	if got := modelFromEnv(); got != defaultSystemOneModel {
		t.Fatalf("blank env model = %q want default", got)
	}
}

func TestResolveToken(t *testing.T) {
	const (
		openRouter = "https://openrouter.ai/api/alpha/decisions"
		aiHub      = "https://ai-llm-gateway.fbr.land/api/alpha/decisions"
	)

	t.Run("explicit token wins", func(t *testing.T) {
		t.Setenv(envSystemOneToken, "explicit")
		t.Setenv(envOpenRouterKey, "openrouter")
		t.Setenv(envAIHubToken, "aihub")

		got, err := resolveToken(openRouter)
		if err != nil || got != "explicit" {
			t.Fatalf("got %q err %v want explicit", got, err)
		}
	})

	t.Run("OPENROUTER_API_KEY is the default source", func(t *testing.T) {
		t.Setenv(envSystemOneToken, "")
		t.Setenv(envOpenRouterKey, "openrouter")
		t.Setenv(envAIHubToken, "aihub")

		got, err := resolveToken(openRouter)
		if err != nil || got != "openrouter" {
			t.Fatalf("got %q err %v want openrouter", got, err)
		}
	})

	t.Run("AIHUB_TOKEN used only against the AI Hub gateway", func(t *testing.T) {
		t.Setenv(envSystemOneToken, "")
		t.Setenv(envOpenRouterKey, "")
		t.Setenv(envAIHubToken, "aihub")

		got, err := resolveToken(aiHub)
		if err != nil || got != "aihub" {
			t.Fatalf("got %q err %v want aihub", got, err)
		}

		// Against any other endpoint the AI Hub token must not leak.
		if _, err := resolveToken(openRouter); err == nil {
			t.Fatal("AIHUB_TOKEN must not be used for a non-AI-Hub endpoint")
		}
	})

	t.Run("no token is an error naming the override and the default", func(t *testing.T) {
		t.Setenv(envSystemOneToken, "")
		t.Setenv(envOpenRouterKey, "")
		t.Setenv(envAIHubToken, "")

		_, err := resolveToken(openRouter)
		if err == nil {
			t.Fatal("want an error when no token is available")
		}

		for _, want := range []string{envSystemOneToken, envOpenRouterKey} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error %q does not mention %s", err, want)
			}
		}
	})
}

func TestRootFlagDefaultsComeFromEnv(t *testing.T) {
	t.Setenv(envSystemOneURL, "https://env.example/api/alpha/decisions")
	t.Setenv(envSystemOneModel, "env-model")

	root := newRootCmd()

	ep, err := root.Flags().GetString("endpoint")
	if err != nil {
		t.Fatal(err)
	}

	if ep != "https://env.example/api/alpha/decisions" {
		t.Fatalf("endpoint default = %q, env not applied", ep)
	}

	model, merr := root.Flags().GetString("model")
	if merr != nil {
		t.Fatal(merr)
	}

	if model != "env-model" {
		t.Fatalf("model default = %q, env not applied", model)
	}
}

func TestRootFlagDefaultsWithoutEnv(t *testing.T) {
	t.Setenv(envSystemOneURL, "")
	t.Setenv(envSystemOneModel, "")

	root := newRootCmd()

	ep, err := root.Flags().GetString("endpoint")
	if err != nil {
		t.Fatal(err)
	}

	if ep != defaultSystemOneURL {
		t.Fatalf("endpoint default = %q want %q", ep, defaultSystemOneURL)
	}

	model, merr := root.Flags().GetString("model")
	if merr != nil {
		t.Fatal(merr)
	}

	if model != defaultSystemOneModel {
		t.Fatalf("model default = %q want %q", model, defaultSystemOneModel)
	}
}
