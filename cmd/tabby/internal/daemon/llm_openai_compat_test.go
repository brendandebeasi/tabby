package daemon

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/teilomillet/gollm"
)

// compile-time check that the HTTP client is a drop-in for the gollm client.
var _ promptGenerator = (*openAICompatClient)(nil)

func TestOpenAICompatClient_Generate(t *testing.T) {
	var gotPath, gotAuth, gotCT, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"content":"meow meow"}}]}`)
	}))
	defer srv.Close()

	c := newOpenAICompatClient(srv.URL+"/", "secret", "model-x", 50, 0.7) // trailing slash trimmed
	out, err := c.Generate(context.Background(), gollm.NewPrompt("say hi"))
	assert.NoError(t, err)
	assert.Equal(t, "meow meow", out)
	assert.Equal(t, "/chat/completions", gotPath)
	assert.Equal(t, "Bearer secret", gotAuth)
	assert.Equal(t, "application/json", gotCT)
	assert.Contains(t, gotBody, `"model":"model-x"`)
	assert.Contains(t, gotBody, "say hi")
}

func TestOpenAICompatClient_GenerateHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":{"message":"bad key"}}`)
	}))
	defer srv.Close()

	c := newOpenAICompatClient(srv.URL, "x", "m", 10, 0.1)
	_, err := c.Generate(context.Background(), gollm.NewPrompt("hi"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "401")
}

func TestIsOpenAICompatProvider(t *testing.T) {
	for _, p := range []string{"fireworks", "openrouter", "openai-compatible", "Generic", "OPENROUTER"} {
		assert.True(t, isOpenAICompatProvider(p), p)
	}
	for _, p := range []string{"anthropic", "openai", "ollama", ""} {
		assert.False(t, isOpenAICompatProvider(p), p)
	}
}

func TestOpenAICompatSettings(t *testing.T) {
	t.Run("fireworks defaults with explicit key", func(t *testing.T) {
		url, key, mdl, ref, ok := openAICompatSettings("fireworks", "", "fw-key", "")
		assert.True(t, ok)
		assert.Equal(t, "https://api.fireworks.ai/inference/v1", url)
		assert.Equal(t, "fw-key", key)
		assert.Contains(t, mdl, "fireworks")
		assert.Equal(t, "", ref)
	})

	t.Run("openrouter sets referer + honors explicit model", func(t *testing.T) {
		url, _, mdl, ref, ok := openAICompatSettings("openrouter", "some/model", "or-key", "")
		assert.True(t, ok)
		assert.Equal(t, "https://openrouter.ai/api/v1", url)
		assert.Equal(t, "some/model", mdl)
		assert.NotEmpty(t, ref)
	})

	t.Run("explicit base URL overrides provider default", func(t *testing.T) {
		url, _, _, _, ok := openAICompatSettings("fireworks", "m", "k", "https://proxy.local/v1")
		assert.True(t, ok)
		assert.Equal(t, "https://proxy.local/v1", url)
	})

	t.Run("key from env when not explicit", func(t *testing.T) {
		t.Setenv("FIREWORKS_API_KEY", "env-fw")
		_, key, _, _, ok := openAICompatSettings("fireworks", "m", "", "")
		assert.True(t, ok)
		assert.Equal(t, "env-fw", key)
	})

	t.Run("openai-compatible requires a base URL", func(t *testing.T) {
		_, _, _, _, ok := openAICompatSettings("openai-compatible", "m", "k", "")
		assert.False(t, ok, "no base URL -> not usable")

		_, _, _, _, ok2 := openAICompatSettings("openai-compatible", "m", "k", "https://x.local/v1")
		assert.True(t, ok2)
	})
}
