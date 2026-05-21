package daemon

// Phase-3 LLM question generation. Sibling to llm.go (kept in its own file
// per the brief so llm.go stays below its line-budget). This file owns:
//
//   - A separate in-memory buffer of pre-generated PendingQuestion objects.
//   - Persistent caching to disk so questions survive daemon restarts.
//   - A batched generator (e.g. 20 questions per call) that runs in a
//     background goroutine — NEVER synchronously inside PickQuestion.
//   - A dedicated gollm client with a higher max_tokens budget so the JSON
//     reply doesn't get truncated. The thought generator's client stays at
//     100 tokens; this one runs at ~2000.
//   - Defensive JSON parsing so a malformed LLM reply never crashes the
//     daemon — bad entries are skipped, good ones survive.
//
// Gate: every entry point checks
//
//     llmClient != nil && cfg.Widgets.Pet.QA.LLMQuestions
//
// before doing anything observable. With the flag off, this file is inert.
//
// Refresh rhythm matches the thought generator's `thoughtRefreshHours` (12h
// by default) — explicit per the Phase-3 brief in
// /Users/b/.claude/plans/wiggly-discovering-starlight.md. The buffer is
// regenerated when (a) it is empty, or (b) >12h have passed since the last
// generation, whichever comes first.

import (
	gocontext "context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/brendandebeasi/tabby/pkg/daemon"
	"github.com/brendandebeasi/tabby/pkg/paths"
	"github.com/teilomillet/gollm"
	"github.com/teilomillet/gollm/llm"
)

// llmQuestionsClient is a dedicated gollm client for question generation.
// We keep it separate from the global llmClient (thoughts) so the higher
// max_tokens budget for JSON responses doesn't bleed into thought
// generation, and so SetOption mutations on one don't race the other.
// Initialised lazily inside initLLMQuestions; left nil until then.
var llmQuestionsClient llm.LLM

// llmQuestionsClientOnce guards lazy init so concurrent triggers don't both
// race to build the same client. sync.Once is the standard pattern for "do
// this on first call" in Go.
var llmQuestionsClientOnce sync.Once

// llmQuestionsClientInitErr captures the error from the one-shot init so
// subsequent triggers see the failure without retrying repeatedly.
var llmQuestionsClientInitErr error

// llmQuestionsProvider/Model/Key are stashed at initLLM time so the lazy
// once-init has the same provider configuration as the thoughts client.
var (
	llmQuestionsProvider string
	llmQuestionsModel    string
	llmQuestionsAPIKey   string
)

// questionBuffer caches parsed PendingQuestion entries ready to be drawn
// by PickQuestion. Newly-generated batches replace the buffer on
// time-based refresh; low-buffer refills append.
var questionBuffer []daemon.PendingQuestion
var questionBufferMutex = &sync.Mutex{}

// questionBufferPath is set by initLLM (alongside thoughtBufferPath) and
// points at <state-dir>/question_buffer.txt. Kept under StatePath rather
// than a new dedicated path constant because it's the same shape as the
// existing thought buffer cache.
var questionBufferPath string

// lastQuestionGeneration tracks when we last requested a fresh batch from
// the LLM. Used together with questionGenerationInterval to decide whether
// a time-based refresh is due.
var lastQuestionGeneration time.Time

// questionGenerationInterval mirrors thoughtGenerationInterval (default
// 12h). Stays a var so a future config knob can override it without
// touching this file.
var questionGenerationInterval = 12 * time.Hour

// questionGenerationInFlight is set to true while a background goroutine
// is calling the LLM, so concurrent triggers don't pile up. Guarded by
// questionBufferMutex.
var questionGenerationInFlight bool

// questionBatchSize is how many questions we ask the LLM to produce per
// call. 20 is the value called out in the brief — enough to amortise the
// API cost across many picks, small enough to fit comfortably under the
// max_tokens budget.
const questionBatchSize = 20

// questionBufferLowWatermark triggers a refill when the buffer drops below
// this many entries (independent of the time-based refresh). Smaller than
// thoughts because we draw from the LLM pool less often than we draw
// thoughts.
const questionBufferLowWatermark = 4

// questionMaxTokens is the per-call max_tokens budget for the question
// generator. 20 questions × ~80 tokens each + JSON framing fits in 2000
// tokens with room to spare; if the LLM truncates mid-array we just lose
// the tail and parse whatever survives.
const questionMaxTokens = 2000

// initLLMQuestions sets up the question-buffer persistence path and stashes
// the provider/model/key for lazy client construction. Called from initLLM
// after the thoughts client is configured, so the question generator picks
// up the same auth.
//
// Does NOT build the llmQuestionsClient yet — that happens on first use so
// users who never flip on LLMQuestions don't pay the client-construction
// cost. Returns no error because failure here would silently disable the
// feature anyway; the trigger entry point is the visible failure boundary.
func initLLMQuestions(provider, model, apiKey string) {
	questionBufferPath = paths.StatePath("question_buffer.txt")
	llmQuestionsProvider = provider
	llmQuestionsModel = model
	llmQuestionsAPIKey = apiKey

	loadQuestionBuffer()
}

// ensureLLMQuestionsClient builds the dedicated gollm client on first use.
// Subsequent calls are no-ops once it's built (or once the init has errored
// once — we don't retry, same policy as the thoughts client).
//
// Returns the client (possibly nil if init failed). Callers must nil-check.
func ensureLLMQuestionsClient() llm.LLM {
	llmQuestionsClientOnce.Do(func() {
		provider := llmQuestionsProvider
		if provider == "" {
			provider = "anthropic"
		}
		model := llmQuestionsModel
		if model == "" {
			switch provider {
			case "anthropic":
				model = "claude-3-haiku-20240307"
			case "openai":
				model = "gpt-3.5-turbo"
			case "ollama":
				model = "llama3"
			}
		}
		// API key resolution is already handled by initLLM (it sets the env
		// vars), so gollm picks them up here without us re-walking the
		// tmux/env fallback chain.
		client, err := gollm.NewLLM(
			gollm.SetProvider(provider),
			gollm.SetModel(model),
			gollm.SetMaxTokens(questionMaxTokens),
			gollm.SetTemperature(0.8),
		)
		if err != nil {
			llmQuestionsClientInitErr = err
			return
		}
		llmQuestionsClient = client
	})
	return llmQuestionsClient
}

// loadQuestionBuffer hydrates questionBuffer from disk on daemon startup.
// File format: one JSON-encoded PendingQuestion per line. Malformed lines
// are skipped silently so a corrupt buffer file doesn't take down the
// daemon — worst case we regenerate the buffer on the next refresh.
func loadQuestionBuffer() {
	if questionBufferPath == "" {
		return
	}
	data, err := os.ReadFile(questionBufferPath)
	if err != nil {
		return // file doesn't exist yet — normal first-run state
	}
	var loaded []daemon.PendingQuestion
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var q daemon.PendingQuestion
		if jsonErr := json.Unmarshal([]byte(line), &q); jsonErr != nil {
			continue // skip malformed entries
		}
		if q.ID == "" || q.Text == "" {
			continue // skip entries missing the load-bearing fields
		}
		loaded = append(loaded, q)
	}
	questionBufferMutex.Lock()
	questionBuffer = loaded
	questionBufferMutex.Unlock()
}

// saveQuestionBuffer writes the current buffer to disk so generated
// questions survive a daemon restart. Mirrors saveThoughtBuffer — best
// effort, no panic on I/O errors (failure just means the next start
// regenerates).
func saveQuestionBuffer() {
	if questionBufferPath == "" {
		return
	}
	if _, err := paths.EnsureStateDir(); err != nil {
		return
	}
	questionBufferMutex.Lock()
	snap := make([]daemon.PendingQuestion, len(questionBuffer))
	copy(snap, questionBuffer)
	questionBufferMutex.Unlock()

	var b strings.Builder
	for i := range snap {
		raw, err := json.Marshal(&snap[i])
		if err != nil {
			continue
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	// Atomic-ish write: write the whole contents in one syscall. We don't
	// bother with a temp+rename because a corrupt buffer recovers on next
	// regeneration anyway (loadQuestionBuffer skips bad lines).
	_ = os.WriteFile(questionBufferPath, []byte(b.String()), 0644)
}

// llmQuestionsEnabled reports whether the Phase-3 feature is fully wired:
// LLM client present AND config flag flipped on. Centralised here so the
// trigger / draw paths don't drift apart.
func llmQuestionsEnabled(llmQuestionsCfg bool) bool {
	return llmQuestionsCfg && (llmClient != nil)
}

// triggerQuestionGeneration starts a background generation goroutine when
// the buffer needs refilling AND the feature is enabled. Safe to call from
// any goroutine (hot paths in particular) — it returns immediately and
// does no work when not needed.
//
// `traits` and `recent` are snapshots from the caller's pet state; we
// deliberately don't take a pet pointer here so that hot paths (PickQuestion
// running under a state lock in the CLI handler) can prepare the snapshot
// while holding the lock and then release it before this function does any
// work. The data is small (≤20 traits + ≤3 answers).
func triggerQuestionGeneration(traits []daemon.PersonalityTrait, recent []daemon.AnsweredQuestion, llmQuestionsCfg bool) {
	if !llmQuestionsEnabled(llmQuestionsCfg) {
		return
	}

	questionBufferMutex.Lock()
	if questionGenerationInFlight {
		questionBufferMutex.Unlock()
		return
	}
	bufferLow := len(questionBuffer) < questionBufferLowWatermark
	timeExpired := questionGenerationInterval > 0 &&
		(lastQuestionGeneration.IsZero() || time.Since(lastQuestionGeneration) > questionGenerationInterval)
	if !bufferLow && !timeExpired {
		questionBufferMutex.Unlock()
		return
	}
	questionGenerationInFlight = true
	questionBufferMutex.Unlock()

	// Copy the inputs so the goroutine has its own slice — caller is free
	// to keep mutating its state once this returns.
	traitsCopy := append([]daemon.PersonalityTrait(nil), traits...)
	recentCopy := append([]daemon.AnsweredQuestion(nil), recent...)

	go func() {
		defer func() {
			questionBufferMutex.Lock()
			questionGenerationInFlight = false
			questionBufferMutex.Unlock()
		}()

		fresh := generateBulkQuestions(traitsCopy, recentCopy, questionBatchSize)
		if len(fresh) == 0 {
			return
		}

		questionBufferMutex.Lock()
		// Time-expired refresh replaces; low-buffer refill appends. Mirrors
		// the thought buffer's behaviour so the two pools age out the same
		// way.
		if timeExpired {
			questionBuffer = fresh
		} else {
			questionBuffer = append(questionBuffer, fresh...)
		}
		lastQuestionGeneration = time.Now()
		questionBufferMutex.Unlock()

		saveQuestionBuffer()
	}()
}

// drawLLMQuestions returns the eligible LLM-generated questions from the
// buffer that haven't already been answered. Caller is responsible for
// passing the set of answered IDs (PickQuestion already builds this).
//
// Returns a fresh slice (caller may mutate freely). Order is buffer order —
// callers that want to randomise should do so themselves.
func drawLLMQuestions(answeredIDs map[string]bool, freeTextAllowed bool) []daemon.PendingQuestion {
	questionBufferMutex.Lock()
	defer questionBufferMutex.Unlock()

	if len(questionBuffer) == 0 {
		return nil
	}
	out := make([]daemon.PendingQuestion, 0, len(questionBuffer))
	for _, q := range questionBuffer {
		if q.ID == "" || q.Text == "" {
			continue
		}
		if answeredIDs[q.ID] {
			continue
		}
		if q.Kind == "free_text" && !freeTextAllowed {
			continue
		}
		out = append(out, q)
	}
	return out
}

// removeLLMQuestion drops a question by ID from the buffer once it's been
// picked, so a subsequent pick doesn't return the same LLM question twice.
// (Seed questions don't need this — they live in the immutable bank and
// `recentlyAnswered` filtering happens after AnswerQuestion records the
// answer.) Best effort: silently ignores unknown IDs.
func removeLLMQuestion(id string) {
	if id == "" {
		return
	}
	questionBufferMutex.Lock()
	defer questionBufferMutex.Unlock()

	for i, q := range questionBuffer {
		if q.ID == id {
			questionBuffer = append(questionBuffer[:i], questionBuffer[i+1:]...)
			break
		}
	}
}

// generateBulkQuestions calls the LLM to produce `count` questions in one
// batch, returns parsed PendingQuestion entries. Returns nil on any
// fatal error (client missing, network failure, totally unparseable
// response) — partial successes return whatever survived parsing.
//
// Synchronous: the caller wraps this in a goroutine.
func generateBulkQuestions(traits []daemon.PersonalityTrait, recent []daemon.AnsweredQuestion, count int) []daemon.PendingQuestion {
	client := ensureLLMQuestionsClient()
	if client == nil {
		return nil
	}
	if count <= 0 {
		count = questionBatchSize
	}

	prompt := buildQuestionGenerationPrompt(traits, recent, count)

	ctx, cancel := gocontext.WithTimeout(gocontext.Background(), 30*time.Second)
	defer cancel()

	llmPrompt := gollm.NewPrompt(prompt)
	response, err := client.Generate(ctx, llmPrompt)
	if err != nil {
		return nil
	}

	return parseGeneratedQuestions(response)
}

// buildQuestionGenerationPrompt assembles the LLM instruction string. The
// shape mirrors generateBulkThoughts' prompt (system framing + state
// context + explicit output format) so future tweaks live in one place
// stylistically.
//
// Includes existing traits + recent answers so the LLM can avoid asking
// duplicates and tailor follow-ups (e.g. "you said you're a night owl —
// what music do you keep on at 2am?"). The data is the same shape that
// petPersonalitySection injects into the thoughts prompt; we just frame it
// for questions instead.
func buildQuestionGenerationPrompt(traits []daemon.PersonalityTrait, recent []daemon.AnsweredQuestion, count int) string {
	var b strings.Builder

	b.WriteString("You are a cat asking its human follow-up questions to learn more about them. ")
	b.WriteString("Generate fresh questions tailored to what you already know about the human, without repeating anything they've already told you.\n\n")

	if len(traits) > 0 {
		b.WriteString("What you already know about your human:\n")
		// Cap at the top 10 traits so the prompt stays small. The caller is
		// expected to have already trimmed, but this defends against future
		// callers that pass everything.
		max := len(traits)
		if max > 10 {
			max = 10
		}
		for i := 0; i < max; i++ {
			b.WriteString("- ")
			b.WriteString(strings.TrimSpace(traits[i].Text))
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}

	if len(recent) > 0 {
		b.WriteString("Recent questions you've asked (DO NOT repeat these):\n")
		max := len(recent)
		if max > 10 {
			max = 10
		}
		for i := 0; i < max; i++ {
			text := strings.TrimSpace(recent[i].Text)
			if text == "" {
				continue
			}
			b.WriteString("- ")
			b.WriteString(text)
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}

	fmt.Fprintf(&b, "Generate %d new questions. ", count)
	b.WriteString("Output ONLY a JSON array, no prose before or after. ")
	b.WriteString("Each element must have exactly these keys:\n")
	b.WriteString(`  "id": short snake_case slug (e.g. "weekend_music_pick")` + "\n")
	b.WriteString(`  "text": the question text (max 140 chars)` + "\n")
	b.WriteString(`  "kind": "choice" or "free_text"` + "\n")
	b.WriteString(`  "choices": array of 3-5 short strings (only for "choice"; omit or empty array for "free_text")` + "\n")
	b.WriteString(`  "trait_for": object mapping a choice (or "*" for free_text) to a trait template like "user prefers %s"` + "\n\n")
	b.WriteString("Rules:\n")
	b.WriteString("- IDs must be unique within this batch.\n")
	b.WriteString("- For free_text questions, trait_for keys must be the literal string \"*\" and the value must contain exactly one %s placeholder.\n")
	b.WriteString("- For choice questions, every choice in `choices` should appear as a key in trait_for (use empty string for choices that shouldn't produce a trait).\n")
	b.WriteString("- Tailor questions to what you already know — go deeper on existing themes, don't restart from zero.\n")
	b.WriteString("- No medical, financial, or otherwise sensitive topics.\n")
	b.WriteString("Output the JSON array now:\n")

	return b.String()
}

// rawGeneratedQuestion is the wire shape we expect from the LLM. Lenient:
// any field can be missing/wrong and we'll either fix it up in
// parseGeneratedQuestions or skip the entry entirely.
type rawGeneratedQuestion struct {
	ID       string            `json:"id"`
	Text     string            `json:"text"`
	Kind     string            `json:"kind"`
	Choices  []string          `json:"choices,omitempty"`
	TraitFor map[string]string `json:"trait_for,omitempty"`
}

// parseGeneratedQuestions extracts a JSON array from the LLM response and
// turns each element into a daemon.PendingQuestion. Defensive:
//
//   - Strips Markdown code fences (```json ... ```) if the LLM wraps the
//     array in one despite our "no prose" instruction.
//   - Slices from the first `[` to the last `]` so leading/trailing prose
//     doesn't break json.Unmarshal.
//   - Skips entries with empty/missing required fields.
//   - Mints a fallback ID (`llm-<short-hash>`) when the LLM forgets to
//     supply one.
//   - Normalises Kind to "choice" / "free_text"; unknown kinds become
//     "free_text" (lowest-effort default).
//
// Returns the surviving entries. nil response → nil return (no panic).
func parseGeneratedQuestions(response string) []daemon.PendingQuestion {
	raw := strings.TrimSpace(response)
	if raw == "" {
		return nil
	}
	// Strip ```json ... ``` fences if present.
	if strings.HasPrefix(raw, "```") {
		// Drop the first line (```json or just ```)
		if nl := strings.IndexByte(raw, '\n'); nl >= 0 {
			raw = raw[nl+1:]
		}
		if idx := strings.LastIndex(raw, "```"); idx >= 0 {
			raw = raw[:idx]
		}
		raw = strings.TrimSpace(raw)
	}
	// Slice from first '[' to last ']' so prose around the array doesn't
	// trip the decoder.
	start := strings.IndexByte(raw, '[')
	end := strings.LastIndexByte(raw, ']')
	if start < 0 || end < start {
		return nil
	}
	jsonBlob := raw[start : end+1]

	var entries []rawGeneratedQuestion
	if err := json.Unmarshal([]byte(jsonBlob), &entries); err != nil {
		return nil
	}

	out := make([]daemon.PendingQuestion, 0, len(entries))
	seen := make(map[string]bool, len(entries))
	for i := range entries {
		e := &entries[i]
		text := strings.TrimSpace(e.Text)
		if text == "" {
			continue
		}
		// Cap absurdly long question text so a buggy LLM reply can't blow
		// out the pet.json sidecar.
		if len(text) > 280 {
			text = text[:280]
		}

		kind := strings.ToLower(strings.TrimSpace(e.Kind))
		switch kind {
		case "choice", "free_text":
			// keep as-is
		default:
			kind = "free_text"
		}

		id := normaliseLLMQuestionID(e.ID, text)
		// Dedupe within this batch — the LLM occasionally reuses an ID.
		if seen[id] {
			continue
		}
		seen[id] = true

		var choices []string
		if kind == "choice" {
			for _, c := range e.Choices {
				c = strings.TrimSpace(c)
				if c == "" {
					continue
				}
				if len(c) > 60 {
					c = c[:60]
				}
				choices = append(choices, c)
			}
			// A choice question with no valid choices is unusable — drop it.
			if len(choices) == 0 {
				continue
			}
		}

		out = append(out, daemon.PendingQuestion{
			ID:      id,
			Text:    text,
			Kind:    kind,
			Choices: choices,
			Source:  "llm",
			// Expires intentionally left zero — PickQuestion (when it draws
			// this) overlays the configured ExpireHours from qa config so
			// the renderer applies a consistent window across bank + LLM
			// questions.
		})
		_ = e.TraitFor // reserved for future trait distillation; not used in Phase 3 picking
	}

	return out
}

// normaliseLLMQuestionID accepts the LLM's proposed ID (or "") and produces
// a stable, prefixed ID we can safely use as PendingQuestion.ID.
//
//   - Always prefixes with "llm-" so seed and LLM IDs never collide.
//   - Falls back to a short hash of the question text when the LLM omits
//     the ID entirely.
//   - Strips whitespace + most punctuation so the ID survives JSON
//     round-trips and log lines cleanly.
func normaliseLLMQuestionID(raw, text string) string {
	id := strings.ToLower(strings.TrimSpace(raw))
	// Replace anything that isn't [a-z0-9_-] with '_'.
	var clean strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
			clean.WriteRune(r)
		case r == ' ':
			clean.WriteByte('_')
		}
	}
	id = clean.String()
	if id == "" {
		// Hash the question text so re-runs of the same text produce the
		// same ID — handy for the dedupe-within-batch logic and for any
		// future "is this the question I just saved" check.
		id = shortHash(text)
	}
	if !strings.HasPrefix(id, "llm-") {
		id = "llm-" + id
	}
	// Cap ID length so it doesn't dominate log lines.
	if len(id) > 64 {
		id = id[:64]
	}
	return id
}

// shortHash returns a stable short identifier for a string. Uses FNV-1a
// (sum32) because we don't need cryptographic strength — just enough
// entropy that two distinct questions don't collapse to the same ID in
// practice. Output is 8 hex chars.
func shortHash(s string) string {
	const (
		offset32 uint32 = 2166136261
		prime32  uint32 = 16777619
	)
	h := offset32
	for _, b := range []byte(s) {
		h ^= uint32(b)
		h *= prime32
	}
	return fmt.Sprintf("%08x", h)
}
