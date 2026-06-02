package daemon

// Auto-generated tab work-summaries. When ai.tab_summary.auto_generate is on, a
// background ticker periodically captures each window's recent terminal output,
// asks a cheap LLM (Fireworks by default) for a 2-3 word summary, and sets it on
// the window as @tabby_ai_title — which the sidebar renders as "CODE - summary".
//
// All capture + LLM work runs in a single spawned goroutine (coalesced via
// summaryFetching) so the event loop never blocks. Windows whose pane content is
// unchanged since the last summary are skipped, so idle tabs don't re-call the LLM.

import (
	"context"
	"fmt"
	"hash/crc32"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/brendandebeasi/tabby/pkg/tmux"
	"github.com/teilomillet/gollm"
)

// ensureSummaryClient lazily builds the summary LLM client from the
// ai.tab_summary config. Defaults (when unset) to the self-hosted Ollama GPU
// endpoint at ollama.vm.dbz.xyz serving qwen2.5:7b-instruct — free local
// inference. Override llm_provider / llm_base_url / llm_model in config to point
// elsewhere (fireworks, openrouter, another openai-compatible endpoint).
// Returns nil if no usable provider/key/model is configured.
func (c *Coordinator) ensureSummaryClient() promptGenerator {
	c.summaryClientOnce.Do(func() {
		cfg := c.config.AI.TabSummary
		provider := strings.TrimSpace(cfg.LLMProvider)
		baseURL := strings.TrimSpace(cfg.LLMBaseURL)
		model := strings.TrimSpace(cfg.LLMModel)
		apiKey := cfg.LLMAPIKey
		if provider == "" {
			// No LLM configured for tab summaries — default to the self-hosted
			// Ollama GPU endpoint so a fresh install gets free local inference
			// out of the box. Only applies when nothing is set: an explicitly
			// chosen provider keeps its own required fields (e.g. an explicit
			// openai-compatible still needs a base URL, else the feature stays
			// dormant).
			provider = "openai-compatible"
			if baseURL == "" {
				baseURL = "https://ollama.vm.dbz.xyz/v1"
			}
			if model == "" {
				model = "qwen2.5:7b-instruct"
			}
			if strings.TrimSpace(apiKey) == "" {
				apiKey = "ollama" // endpoint is IP-allowlisted, not key-gated
			}
		}
		url, key, mdl, referer, ok := openAICompatSettings(provider, model, apiKey, baseURL)
		if !ok {
			return // leave summaryClient nil — feature stays dormant
		}
		// Budget headroom so reasoning models (e.g. gpt-oss) can finish their
		// hidden reasoning and still emit the short final label in `content`.
		// The model stops when done, so this is a ceiling, not the per-call cost.
		client := newOpenAICompatClient(url, key, mdl, 512, 0.4)
		client.referer = referer
		c.summaryClient = client
	})
	return c.summaryClient
}

// RefreshTabSummaries is the tick entry point. It returns immediately; the slow
// capture + LLM work happens in a coalesced background goroutine. Mirrors the
// async/coalesce shape of RefreshTeamClaude.
func (c *Coordinator) RefreshTabSummaries() {
	cfg := c.config.AI.TabSummary
	if !cfg.AutoGenerate {
		return
	}
	client := c.ensureSummaryClient()
	if client == nil {
		return
	}
	if !c.summaryFetching.CompareAndSwap(false, true) {
		return // a refresh is already in flight
	}

	maxWords := cfg.MaxWords
	projAbbrev := parseAbbreviations(cfg.ProjectNames) // dir basename (lower) -> exact abbreviation
	windows := c.GetWindows()
	go func() {
		defer c.summaryFetching.Store(false)

		changed := false
		for _, win := range windows {
			paneID := firstContentPaneID(win)
			if paneID == "" {
				continue
			}
			content := strings.TrimSpace(capturePaneText(paneID, 40))
			if content == "" {
				continue
			}

			h := hashString(content)
			c.summaryHashMu.Lock()
			prev := c.summaryHash[win.ID]
			c.summaryHashMu.Unlock()
			if prev == h {
				continue // unchanged since last summary — skip the LLM call
			}

			project := ""
			if cwd := firstPaneCWD(win); cwd != "" {
				project = filepath.Base(cwd)
			}
			// A configured project abbreviation is prepended deterministically;
			// the LLM then only fills in the task (so its own guess can't drift).
			fixed := projAbbrev[strings.ToLower(project)]

			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
			prompt := summaryPrompt(project, content)
			if fixed != "" {
				prompt = workPrompt(content)
			}
			out, err := client.Generate(ctx, gollm.NewPrompt(prompt))
			cancel()
			if err != nil {
				continue
			}

			var summary string
			if fixed != "" {
				work := truncateSummaryWords(out, maxWords-1) // leave room for the prefix
				summary = strings.TrimSpace(fixed + " " + work)
			} else {
				summary = truncateSummaryWords(out, maxWords)
			}
			if summary == "" {
				continue
			}

			exec.Command("tmux", "set-window-option", "-t", win.ID, "@tabby_ai_title", summary).Run()

			c.summaryHashMu.Lock()
			if c.summaryHash == nil {
				c.summaryHash = make(map[string]string)
			}
			c.summaryHash[win.ID] = h
			c.summaryHashMu.Unlock()
			changed = true
		}

		if changed && c.OnRefreshLayout != nil {
			c.OnRefreshLayout()
		}
	}()
}

// firstContentPaneID returns the id of the window's first non-auxiliary pane
// (the user's content pane, not Tabby's sidebar/header panes).
func firstContentPaneID(win tmux.Window) string {
	for i := range win.Panes {
		if isAuxiliaryPane(win.Panes[i]) {
			continue
		}
		if id := strings.TrimSpace(win.Panes[i].ID); id != "" {
			return id
		}
	}
	if len(win.Panes) > 0 {
		return strings.TrimSpace(win.Panes[0].ID)
	}
	return ""
}

// capturePaneText returns the last `lines` rows of a pane's visible content.
func capturePaneText(paneID string, lines int) string {
	out, err := exec.Command("tmux", "capture-pane", "-p", "-t", paneID, "-S", fmt.Sprintf("-%d", lines)).Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// workPrompt asks for just the task/component (no project name), used when a
// project abbreviation is configured and prepended deterministically.
func workPrompt(content string) string {
	return "You are labeling a terminal tab in a narrow sidebar. Based on the recent " +
		"terminal output below, reply with ONLY 2-3 short lowercase words for the " +
		"specific component or task being worked on. Do NOT mention the project, repo, " +
		"or directory name. Abbreviate aggressively (e.g. \"paperclip resend\", " +
		"\"deploy fix\", \"gh token\"). Reply in English only using plain ASCII letters — " +
		"never other languages or non-ASCII characters. No punctuation, no quotes, no explanation." +
		"\n\n--- terminal output ---\n" + content
}

// summaryPrompt builds the LLM instruction for an ultra-terse tab label that
// leads with an abbreviation of the project/working directory so the user can
// tell which project the tab belongs to (e.g. "sd paperclip resend", "infra gp").
func summaryPrompt(project, content string) string {
	hint := ""
	if p := strings.TrimSpace(project); p != "" {
		hint = "The project / working directory is \"" + p + "\". Start the label with a " +
			"very short abbreviation of it (e.g. \"studiodome-infra\"->\"sd\", " +
			"\"gunpowder-infra\"->\"gp\", \"infras\"->\"infra\", \"tabby\"->\"tby\"). "
	}
	return "You are labeling a terminal tab in a narrow sidebar. " + hint +
		"Based on the recent terminal output below, reply with ONLY a short lowercase " +
		"label: the project abbreviation, then the component or task, then the action. " +
		"Up to 4 short words, abbreviated aggressively (e.g. \"sd paperclip resend\", " +
		"\"gp paperclip\", \"infra deploy fix\"). Reply in English only using plain ASCII " +
		"letters — never other languages or non-ASCII characters. No punctuation, no quotes, no " +
		"explanation.\n\n--- terminal output ---\n" + content
}

// truncateSummaryWords cleans an LLM reply into a short label: strips control
// chars, surrounding quotes and trailing punctuation, and keeps the first n words.
func truncateSummaryWords(s string, n int) string {
	s = strings.TrimSpace(stripControl(s))
	s = strings.Trim(s, "\"'`.,:;!?-")
	fields := strings.Fields(s)
	if n > 0 && len(fields) > n {
		fields = fields[:n]
	}
	return strings.Join(fields, " ")
}

func hashString(s string) string {
	return fmt.Sprintf("%08x", crc32.ChecksumIEEE([]byte(s)))
}

// stripControl collapses tabs/newlines to spaces and drops other control chars.
func stripControl(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			b.WriteRune(' ')
		case r >= 0x20:
			b.WriteRune(r)
		}
	}
	return b.String()
}
