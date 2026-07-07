package daemon

// Anthropic /v1/messages client with a CONFIGURABLE base URL. gollm's anthropic
// provider hardcodes https://api.anthropic.com/v1/messages, so when the user
// routes Anthropic through a proxy (e.g. teamclaude, via ANTHROPIC_BASE_URL — the
// same way Claude Code is pointed), tabby's LLM calls would still hit
// api.anthropic.com and get transparently MITM'd/re-signed, tripping a recurring
// "verify Anthropic TLS cert" prompt. This client sends the Anthropic message
// format to a configured base URL instead, so requests land on the proxy's own
// certificate and never touch api.anthropic.com. Implements promptGenerator, so
// it drops into the existing thoughts/Q&A call sites unchanged.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/teilomillet/gollm/llm"
)

type anthropicBaseURLClient struct {
	baseURL     string // e.g. https://teamclaude.vm.dbz.xyz (no trailing slash)
	apiKey      string
	model       string
	maxTokens   int
	temperature float64
	httpClient  *http.Client
}

func newAnthropicBaseURLClient(baseURL, apiKey, model string, maxTokens int, temperature float64) *anthropicBaseURLClient {
	return &anthropicBaseURLClient{
		baseURL:     strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		apiKey:      strings.TrimSpace(apiKey),
		model:       strings.TrimSpace(model),
		maxTokens:   maxTokens,
		temperature: temperature,
		httpClient:  &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *anthropicBaseURLClient) Generate(ctx context.Context, prompt *llm.Prompt, _ ...llm.GenerateOption) (string, error) {
	text := ""
	if prompt != nil {
		text = strings.TrimSpace(prompt.Input)
		if text == "" {
			text = prompt.String()
		}
	}
	reqBody := map[string]interface{}{
		"model":       c.model,
		"max_tokens":  c.maxTokens,
		"temperature": c.temperature,
		"messages":    []map[string]string{{"role": "user", "content": text}},
	}
	raw, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/messages", bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("anthropic http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var parsed struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("anthropic parse: %w", err)
	}
	if parsed.Error.Message != "" {
		return "", fmt.Errorf("anthropic api: %s", parsed.Error.Message)
	}
	for _, blk := range parsed.Content {
		if blk.Type == "text" && strings.TrimSpace(blk.Text) != "" {
			return blk.Text, nil
		}
	}
	return "", fmt.Errorf("anthropic: empty response")
}

// resolveAnthropicBaseURL returns a proxy base URL to use for Anthropic instead
// of gollm's hardcoded api.anthropic.com: the explicit config value if set, else
// the ANTHROPIC_BASE_URL env / tmux-session var (the same one Claude Code reads).
// Empty means "no proxy configured — fall back to gollm/api.anthropic.com".
func resolveAnthropicBaseURL(configBaseURL string) string {
	if b := strings.TrimSpace(configBaseURL); b != "" {
		return b
	}
	return strings.TrimSpace(lookupAPIKey("ANTHROPIC_BASE_URL"))
}
