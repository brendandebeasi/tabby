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
	"strings"
	"time"

	"github.com/brendandebeasi/tabby/pkg/tmux"
	"github.com/teilomillet/gollm"
)

// summaryCooldown is the minimum time between LLM re-summarizations of the same
// window. It bounds how fast an actively-changing tab can be renamed, which both
// keeps names readable (no per-second flapping) and breaks the
// rename -> after-rename-window hook -> re-summarize -> rename feedback loop.
const summaryCooldown = 60 * time.Second

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
	maxChars := cfg.MaxChars
	windows := c.GetWindows()
	go func() {
		defer c.summaryFetching.Store(false)

		changed := false
		for _, win := range windows {
			if win.NameLocked {
				// A hard lock on a GENERIC stub (claude/zsh/~/...) is bogus — the
				// `r` rename binding pre-fills the auto-name and sets the lock when
				// accepted. Clear it and fall through to summarize. A non-generic
				// lock is a deliberate user name: composeTabBaseName shows it
				// verbatim and hides the summary, so don't spend an LLM call — just
				// clear any stale transient title lingering behind it.
				if isGenericTabName(win.Name) {
					exec.Command("tmux", "set-window-option", "-t", win.ID, "-u", "@tabby_name_locked").Run()
					win.NameLocked = false
					changed = true
					// fall through — summarize this window this pass
				} else {
					if strings.TrimSpace(win.AITitle) != "" {
						exec.Command("tmux", "set-window-option", "-t", win.ID, "-u", "@tabby_ai_title").Run()
						changed = true
					}
					continue
				}
			}

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
				continue // unchanged since last tick — skip the LLM call
			}

			// Per-window cooldown: never re-summarize a window more than once per
			// summaryCooldown, no matter how often rename/tick triggers fire. An
			// active tab (a live Claude/log window) changes content continuously,
			// so the hash check above never skips it. The cooldown bounds the LLM
			// call rate. Check-and-claim atomically, recording now BEFORE the LLM
			// call so a slow/failed call also cools the window down instead of
			// being retried on the next trigger.
			c.summaryHashMu.Lock()
			if last, ok := c.summaryLastAt[win.ID]; ok && time.Since(last) < summaryCooldown {
				c.summaryHashMu.Unlock()
				continue
			}
			if c.summaryLastAt == nil {
				c.summaryLastAt = make(map[string]time.Time)
			}
			c.summaryLastAt[win.ID] = time.Now()
			c.summaryHashMu.Unlock()

			// Generate an ephemeral, TASK-ONLY summary stored on @tabby_ai_title.
			// The deterministic project prefix is added at render time by
			// composeTabBaseName (windowDirCode), so the LLM is asked only for the
			// component/task with no project hint — and the result is never
			// persisted to disk nor shared across windows.
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
			out, err := client.Generate(ctx, gollm.NewPrompt(workPrompt(content, maxChars)))
			cancel()
			if err != nil {
				continue
			}
			summary := truncateSummary(out, maxWords, maxChars)
			if summary == "" {
				continue
			}

			exec.Command("tmux", "set-window-option", "-t", win.ID, "@tabby_ai_title", summary).Run()
			c.recordSummaryHash(win.ID, h)
			changed = true
		}

		if changed && c.OnRefreshLayout != nil {
			c.OnRefreshLayout()
		}
	}()
}

// recordSummaryHash stores the content hash for a window so an unchanged pane
// doesn't trigger another LLM call next tick.
func (c *Coordinator) recordSummaryHash(winID, h string) {
	c.summaryHashMu.Lock()
	if c.summaryHash == nil {
		c.summaryHash = make(map[string]string)
	}
	c.summaryHash[winID] = h
	c.summaryHashMu.Unlock()
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

// asciiOnlyRules is the shared output-language constraint appended to every
// summary prompt. Repeated and explicit because some small models (qwen,
// gpt-oss) still slip Chinese/Cyrillic/emoji into the label when the terminal
// content contains non-ASCII text. We tell the model exactly what's allowed
// AND what's banned, with concrete reject examples — empirically this lands
// the constraint better than a single soft sentence. A defense-in-depth ASCII
// filter in truncateSummaryWords strips anything that still slips through.
const asciiOnlyRules = " OUTPUT LANGUAGE: ENGLISH ONLY. " +
	"Use ONLY the 26 lowercase ASCII letters a-z and ASCII digits 0-9, separated by " +
	"single ASCII spaces. NO Chinese characters. NO Japanese. NO Korean. NO Cyrillic. " +
	"NO Arabic. NO accented Latin (no á é í ñ ü). NO emoji. NO punctuation. NO quotes. " +
	"NO markdown. NO explanation. If the terminal content is in another language, " +
	"translate the gist into English first, then label in plain ASCII. If you cannot " +
	"produce a valid ASCII label, reply with the single word \"work\". "

// workPrompt asks the LLM for just the task/component being worked on — no
// project name. The deterministic project prefix is added separately at render
// time (composeTabBaseName / windowDirCode), so the summary stays a pure,
// per-window task topic that is never persisted.
func workPrompt(content string, maxChars int) string {
	lengthRule := "reply with ONLY 2-3 short lowercase words for the "
	if maxChars > 0 {
		// A char budget is set: ask the model to fit the sidebar width directly,
		// so the label rarely needs display-side truncation.
		lengthRule = fmt.Sprintf("reply with ONLY a short lowercase label of at most %d "+
			"characters (a couple of words) for the ", maxChars)
	}
	return "You are labeling a terminal tab in a narrow sidebar. Based on the recent " +
		"terminal output below, " + lengthRule +
		"specific component or task being worked on. Do NOT mention the project, repo, " +
		"or directory name. Abbreviate aggressively (e.g. \"paperclip resend\", " +
		"\"deploy fix\", \"gh token\")." +
		asciiOnlyRules +
		"\n\n--- terminal output ---\n" + content
}

// truncateSummary cleans an LLM reply into a short label: strips control
// chars, forces ASCII-only (defense-in-depth against the prompt's
// English-only constraint occasionally being ignored — Chinese/CJK/Cyrillic
// would otherwise reach the sidebar and render as boxes or "?"s), strips
// surrounding quotes and trailing punctuation, keeps the first maxWords words,
// then caps the result to maxChars characters. maxWords/maxChars <= 0 disable
// their respective limit. The char cap drops whole words from the end where it
// can, only hard-cutting when the very first word already exceeds the budget —
// the sidebar's display truncation still guarantees the final fit, so this just
// keeps the stored title tidy and close to the target width.
func truncateSummary(s string, maxWords, maxChars int) string {
	s = strings.TrimSpace(stripControl(s))
	s = stripNonASCIIWord(s)
	s = strings.Trim(s, "\"'`.,:;!?-")
	fields := strings.Fields(s)
	if maxWords > 0 && len(fields) > maxWords {
		fields = fields[:maxWords]
	}
	if maxChars > 0 {
		fields = capWords(fields, maxChars)
	}
	return strings.Join(fields, " ")
}

// capWords trims trailing words until the joined result fits maxChars. If even
// the first word is too long, it is hard-cut to maxChars runes.
func capWords(fields []string, maxChars int) []string {
	for len(fields) > 1 && len(strings.Join(fields, " ")) > maxChars {
		fields = fields[:len(fields)-1]
	}
	if len(fields) == 1 && len([]rune(fields[0])) > maxChars {
		fields[0] = string([]rune(fields[0])[:maxChars])
	}
	return fields
}

// stripNonASCIIWord drops any rune that isn't printable ASCII, lowercasing
// uppercase Latin in the process. Multi-byte runes (CJK, accented Latin,
// emoji) become nothing, so a label like "deploy 部署 fix" collapses to
// "deploy fix" — preferable to letting boxes/replacement chars into the
// sidebar. Spaces between surviving runs of letters/digits are preserved so
// truncateSummaryWords' Fields split still works.
func stripNonASCIIWord(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= '0' && r <= '9',
			r == ' ', r == '-', r == '_':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		default:
			// Drop. Multi-byte rune → 0 ASCII bytes; isolated punctuation
			// → dropped (truncateSummaryWords' trailing Trim handled it
			// before, but only at the edges — a CJK char mid-string would
			// have survived without this).
		}
	}
	return b.String()
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
