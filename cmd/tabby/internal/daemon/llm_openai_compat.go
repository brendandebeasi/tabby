package daemon

// OpenAI-compatible LLM client. Powers the pet's thoughts/Q&A via any service
// that speaks the OpenAI /chat/completions API — Fireworks, OpenRouter, Groq,
// or a local server — without going through gollm's provider registry (which
// hardcodes its known providers and doesn't expose Fireworks/OpenRouter).
//
// It implements promptGenerator, the same narrow interface gollm's client
// satisfies, so it drops into the existing call sites unchanged.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/teilomillet/gollm/llm"
)

// promptGenerator is the subset of gollm's llm.LLM that Tabby actually uses for
// the pet (a single Generate call). Both gollm's client and openAICompatClient
// satisfy it, letting initLLM pick a backend without touching call sites.
type promptGenerator interface {
	Generate(ctx context.Context, prompt *llm.Prompt, opts ...llm.GenerateOption) (string, error)
}

// openAICompatClient talks to an OpenAI-compatible /chat/completions endpoint.
type openAICompatClient struct {
	baseURL     string // e.g. https://api.fireworks.ai/inference/v1 (no trailing slash)
	apiKey      string
	model       string
	maxTokens   int
	temperature float64
	referer     string // sent as HTTP-Referer; OpenRouter uses it to identify the app
	httpClient  *http.Client
}

func newOpenAICompatClient(baseURL, apiKey, model string, maxTokens int, temperature float64) *openAICompatClient {
	return &openAICompatClient{
		baseURL:     strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		apiKey:      strings.TrimSpace(apiKey),
		model:       strings.TrimSpace(model),
		maxTokens:   maxTokens,
		temperature: temperature,
		httpClient:  &http.Client{Timeout: 30 * time.Second},
	}
}

// Generate sends the prompt as a single user message and returns the assistant's
// reply text. Options are ignored — the pet only ever passes a bare prompt.
func (c *openAICompatClient) Generate(ctx context.Context, prompt *llm.Prompt, _ ...llm.GenerateOption) (string, error) {
	text := ""
	if prompt != nil {
		text = strings.TrimSpace(prompt.Input)
		if text == "" {
			text = prompt.String()
		}
	}

	reqBody := map[string]interface{}{
		"model":       c.model,
		"messages":    []map[string]string{{"role": "user", "content": text}},
		"max_tokens":  c.maxTokens,
		"temperature": c.temperature,
	}
	raw, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	if c.referer != "" {
		req.Header.Set("HTTP-Referer", c.referer)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("llm http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("llm parse: %w", err)
	}
	if parsed.Error.Message != "" {
		return "", fmt.Errorf("llm api: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("llm: empty response")
	}
	return parsed.Choices[0].Message.Content, nil
}

// isOpenAICompatProvider reports whether a provider name should be served by the
// generic OpenAI-compatible HTTP client rather than gollm.
func isOpenAICompatProvider(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "fireworks", "openrouter", "openai-compatible", "openai_compatible", "generic":
		return true
	}
	return false
}

// openAICompatSettings resolves the base URL, API key, model, and referer for an
// OpenAI-compatible provider. An explicit baseURL/apiKey/model always wins;
// otherwise per-provider defaults and env/tmux key lookup fill the gaps. ok is
// false when a required value (base URL or key) is missing.
func openAICompatSettings(provider, model, apiKey, baseURL string) (url, key, mdl, referer string, ok bool) {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "fireworks":
		url = "https://api.fireworks.ai/inference/v1"
		key = firstNonEmpty(apiKey, lookupAPIKey("FIREWORKS_API_KEY"))
		mdl = firstNonEmpty(model, "accounts/fireworks/models/llama-v3p1-8b-instruct")
	case "openrouter":
		url = "https://openrouter.ai/api/v1"
		key = firstNonEmpty(apiKey, lookupAPIKey("OPENROUTER_API_KEY"))
		mdl = firstNonEmpty(model, "meta-llama/llama-3.1-8b-instruct")
		referer = "https://github.com/brendandebeasi/tabby"
	default: // openai-compatible / generic — base URL must be supplied by config
		key = firstNonEmpty(apiKey, lookupAPIKey("OPENAI_API_KEY"))
		mdl = model
	}

	if b := strings.TrimSpace(baseURL); b != "" {
		url = b // explicit override always wins
	}

	ok = url != "" && key != "" && mdl != ""
	return
}

// lookupAPIKey reads an API key from the environment, falling back to the tmux
// session environment (mirrors the resolution gollm-backed providers use).
func lookupAPIKey(name string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	// Try the tmux GLOBAL environment first (the daemon has no attached client, so
	// a bare `show-environment` has no reliable session context), then the session
	// env as a fallback.
	for _, args := range [][]string{{"show-environment", "-g", name}, {"show-environment", name}} {
		out, err := exec.Command("tmux", args...).Output()
		if err != nil {
			continue
		}
		line := strings.TrimSpace(string(out))
		if strings.HasPrefix(line, name+"=") {
			if v := strings.TrimSpace(strings.TrimPrefix(line, name+"=")); v != "" {
				return v
			}
		}
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
