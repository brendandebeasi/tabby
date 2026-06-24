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

		aiSummaryOnly := cfg.AISummaryOnly
		changed := false
		for _, win := range windows {
			// A hard-locked name (explicit user rename) is authoritative — never
			// override it. Clear any transient title a prior tick left so a stale
			// summary doesn't linger, then move on without calling the LLM.
			if win.NameLocked {
				if strings.TrimSpace(win.AITitle) != "" {
					exec.Command("tmux", "set-window-option", "-t", win.ID, "-u", "@tabby_ai_title").Run()
					changed = true
				}
				continue
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

			// Decide between two behaviors:
			//   - NAME this project: the window resolves to a stable project key
			//     (local git root, or ssh://host/root) and is NOT a Claude-Code
			//     tab in ai_summary_only mode. We infer a persisted per-project
			//     name (soft) so new sessions in the same project reuse it.
			//   - Transient SUMMARY (legacy @tabby_ai_title): everything else —
			//     no usable key (e.g. an ssh tab whose remote-cwd hook hasn't
			//     reported yet), or an ai_summary_only Claude tab that wants a
			//     live task label rather than a static project name.
			key, hasKey := c.windowNameKey(win)
			nameThis := hasKey && !(aiSummaryOnly && isAIWindow(win))

			if nameThis {
				// A user-sourced name owns this project; the LLM must not touch it
				// (applyCWDIdentityMappings restores it). Record the hash so we
				// don't re-capture this window's content until it changes.
				if rec, ok := c.getCWDColorMapping(key); ok && strings.TrimSpace(rec.Name) != "" && (rec.NameSource == "user" || rec.NameSource == "") {
					c.recordSummaryHash(win.ID, h)
					continue
				}

				project := projectBasename(win, key)
				fixed := projAbbrev[strings.ToLower(project)]

				ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
				out, err := client.Generate(ctx, gollm.NewPrompt(projectNamePrompt(project, content)))
				cancel()
				if err != nil {
					continue
				}
				name := truncateSummaryWords(out, maxWords)
				if fixed != "" {
					// A configured abbreviation is authoritative for the lead token.
					work := truncateSummaryWords(out, maxWords-1)
					name = strings.TrimSpace(fixed + " " + work)
				}
				if name == "" {
					continue
				}

				// Soft-persist (no-ops when unchanged; precedence protects any
				// user name) and apply to this window as a soft auto-name.
				c.captureCWDIdentity(key, name, "", false, "llm")
				if win.Name != name {
					exec.Command("tmux", "rename-window", "-t", win.ID, name).Run()
				}
				if !win.NameAuto {
					exec.Command("tmux", "set-window-option", "-t", win.ID, "@tabby_name_auto", "1").Run()
				}
				c.recordSummaryHash(win.ID, h)
				changed = true
				continue
			}

			// Transient summary (legacy @tabby_ai_title) path.
			project := projectBasename(win, key)
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

// projectBasename returns the human-facing folder name of a window's project for
// use as an LLM hint / abbreviation lookup. Prefers the resolved name key's
// final path component (the project root, or the remote topmost dir for an
// ssh:// key), falling back to the first content pane's cwd basename.
func projectBasename(win tmux.Window, key string) string {
	if key != "" {
		if b := filepath.Base(key); b != "" && b != "." && b != "/" {
			return b
		}
	}
	if cwd := firstPaneCWD(win); cwd != "" {
		return filepath.Base(cwd)
	}
	return ""
}

// projectNamePrompt asks the LLM for a SHORT, STABLE label identifying the
// project itself (not the live task), so every tab/session in the same project
// resolves to the same name. The directory name is the dominant signal; the
// terminal content only disambiguates. Kept deliberately identity-focused (vs.
// summaryPrompt's task focus) so the persisted soft name doesn't churn as work
// changes.
func projectNamePrompt(project, content string) string {
	hint := ""
	if p := strings.TrimSpace(project); p != "" {
		hint = "The project directory is named \"" + p + "\". Base the label primarily " +
			"on this name; clean it up into 2-3 readable lowercase words (e.g. " +
			"\"studiodome-infra\"->\"studiodome infra\", \"gunpowder-msg\"->\"gunpowder msg\", " +
			"\"tabby\"->\"tabby\"). "
	}
	return "You are giving a stable name to a terminal tab's PROJECT in a narrow " +
		"sidebar. " + hint + "Reply with ONLY 2-3 short lowercase words naming the " +
		"project or codebase — NOT the current task or command. Prefer the directory " +
		"name; use the terminal output only to disambiguate." +
		asciiOnlyRules +
		"\n\n--- terminal output ---\n" + content
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

// workPrompt asks for just the task/component (no project name), used when a
// project abbreviation is configured and prepended deterministically.
func workPrompt(content string) string {
	return "You are labeling a terminal tab in a narrow sidebar. Based on the recent " +
		"terminal output below, reply with ONLY 2-3 short lowercase words for the " +
		"specific component or task being worked on. Do NOT mention the project, repo, " +
		"or directory name. Abbreviate aggressively (e.g. \"paperclip resend\", " +
		"\"deploy fix\", \"gh token\")." +
		asciiOnlyRules +
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
		"\"gp paperclip\", \"infra deploy fix\")." +
		asciiOnlyRules +
		"\n\n--- terminal output ---\n" + content
}

// truncateSummaryWords cleans an LLM reply into a short label: strips control
// chars, forces ASCII-only (defense-in-depth against the prompt's
// English-only constraint occasionally being ignored — Chinese/CJK/Cyrillic
// would otherwise reach the sidebar and render as boxes or "?"s), strips
// surrounding quotes and trailing punctuation, and keeps the first n words.
func truncateSummaryWords(s string, n int) string {
	s = strings.TrimSpace(stripControl(s))
	s = stripNonASCIIWord(s)
	s = strings.Trim(s, "\"'`.,:;!?-")
	fields := strings.Fields(s)
	if n > 0 && len(fields) > n {
		fields = fields[:n]
	}
	return strings.Join(fields, " ")
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
