// Package daemon — Phase 3 cross-cutting LLM tests.
//
// These tests live in their own file (separate from pet_qa_test.go) per the
// Phase 3 testing plan: pet_qa_test.go owns the Phase 1 (48 fns) and the
// distillation-author's 9 LLM-distillation tests; coordinator_qa_render_test.go
// owns Phase 2 render-layer tests. This file fills the cross-cutting gaps
// that neither feature author could write from their own seat:
//
//   - parseGeneratedQuestions / generateBulkQuestions wiring (Phase 3A)
//   - PickQuestion mixed seed+LLM pool integration (Phase 3A ↔ Phase 1)
//   - LLM-question dedup against the seed bank (Phase 3A ↔ Phase 1)
//   - The shared privacy gate that covers BOTH 3A (questions) and 3B
//     (distillation) (Phase 3A ↔ Phase 3B)
//   - Async safety of applyLLMDistillation under c.stateMu (Phase 3B ↔
//     coordinator)
//
// The package-level state owned by llm_questions.go (questionBuffer,
// questionBufferPath, lastQuestionGeneration, questionGenerationInFlight)
// is shared global state. Every test that mutates any of those captures
// the prior value into a t.Cleanup so tests stay isolated regardless of
// run order.
//
// IMPORTANT: this file deliberately does NOT touch the real gollm client.
// All LLM-shaped behaviour is exercised by either (a) parsing canned
// response strings via parseGeneratedQuestions / parseLLMProposals, or
// (b) stubbing the llmGenerator closure that DistillTraitsLLM accepts.
// We treat the gollm.LLM interface as untestable from this layer.
package daemon

import (
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brendandebeasi/tabby/pkg/daemon"
	"github.com/stretchr/testify/assert"
)

// ─── shared helpers ────────────────────────────────────────────────────────

// withCleanQuestionBuffer snapshots the package-level question-buffer state,
// resets it for the test, and restores it afterwards. Required because
// llm_questions.go uses package globals (questionBuffer, questionBufferPath,
// lastQuestionGeneration, questionGenerationInFlight) and a leak between
// tests would silently corrupt parallel runs.
//
// Returns a path inside t.TempDir() so saveQuestionBuffer writes can be
// observed without touching the real ~/.local/state/tabby tree.
func withCleanQuestionBuffer(t *testing.T) string {
	t.Helper()
	bufferPath := filepath.Join(t.TempDir(), "question_buffer.txt")

	questionBufferMutex.Lock()
	prevBuf := questionBuffer
	prevPath := questionBufferPath
	prevLast := lastQuestionGeneration
	prevFlight := questionGenerationInFlight
	questionBuffer = nil
	questionBufferPath = bufferPath
	lastQuestionGeneration = time.Time{}
	questionGenerationInFlight = false
	questionBufferMutex.Unlock()

	t.Cleanup(func() {
		questionBufferMutex.Lock()
		questionBuffer = prevBuf
		questionBufferPath = prevPath
		lastQuestionGeneration = prevLast
		questionGenerationInFlight = prevFlight
		questionBufferMutex.Unlock()
	})
	return bufferPath
}

// seedQuestionBuffer puts entries directly into the package-level
// questionBuffer, mimicking the post-generation state without calling the
// LLM. Caller is responsible for having reserved the buffer via
// withCleanQuestionBuffer.
func seedQuestionBuffer(qs []daemon.PendingQuestion) {
	questionBufferMutex.Lock()
	questionBuffer = append([]daemon.PendingQuestion(nil), qs...)
	questionBufferMutex.Unlock()
}

// llmQ constructs a synthetic LLM-sourced PendingQuestion for use in the
// buffer-seeding tests. The Source field is set to "llm" so the entry is
// indistinguishable from a real LLM-generated one once it lands in the
// pool.
func llmQ(id, text, kind string, choices ...string) daemon.PendingQuestion {
	return daemon.PendingQuestion{
		ID:      id,
		Text:    text,
		Kind:    kind,
		Choices: choices,
		Source:  "llm",
	}
}

// reseedPetQARand pins the package-level random source so the mixing
// proportion tests are deterministic across runs. Restored on test exit.
func reseedPetQARand(t *testing.T, seed int64) {
	t.Helper()
	prev := petQARand
	petQARand = rand.New(rand.NewSource(seed))
	t.Cleanup(func() { petQARand = prev })
}

// stateWithConsentAnswered returns a daemon.PetState that has cleared the
// first-time consent gate so the mixed-pool logic actually runs. The
// consent answer is timestamped well outside the 6-month staleness window
// so it doesn't pollute the eligible bank.
func stateWithConsentAnswered() *daemon.PetState {
	return &daemon.PetState{
		State: "idle",
		AnsweredQuestions: []daemon.AnsweredQuestion{
			{
				ID:        "consent",
				Text:      "consent",
				Answer:    "Yes, ask away",
				Kind:      "choice",
				Timestamp: petTimeNow.Add(-30 * 24 * time.Hour),
			},
		},
	}
}

// ─── parseGeneratedQuestions ───────────────────────────────────────────────

// TestLLMQuestionGen_Parse_ValidJSON pins the happy path: a well-formed
// JSON array round-trips into the buffer with Source=="llm" on every entry,
// IDs are minted with the "llm-" prefix, and choice/free_text Kinds are
// preserved.
func TestLLMQuestionGen_Parse_ValidJSON(t *testing.T) {
	resp := `[
		{"id": "weekend_music_pick", "text": "What music keeps you company on weekends?", "kind": "free_text"},
		{"id": "morning_routine", "text": "Which describes your morning?", "kind": "choice",
		 "choices": ["coffee first", "shower first", "doomscroll first"]}
	]`
	got := parseGeneratedQuestions(resp)
	if !assert.Len(t, got, 2, "two well-formed entries should both parse") {
		t.FailNow()
	}
	for _, q := range got {
		assert.Equal(t, "llm", q.Source, "every parsed entry must be tagged Source=llm")
		assert.True(t, strings.HasPrefix(q.ID, "llm-"),
			"every minted ID must use the llm- prefix; got %q", q.ID)
		assert.NotEmpty(t, q.Text)
	}

	// The first entry is free_text → no choices array carried through.
	freeText := got[0]
	assert.Equal(t, "free_text", freeText.Kind)
	assert.Empty(t, freeText.Choices, "free_text entries must not carry choices")

	// The second entry is choice → choices array preserved (and trimmed).
	choice := got[1]
	assert.Equal(t, "choice", choice.Kind)
	assert.Equal(t, []string{"coffee first", "shower first", "doomscroll first"}, choice.Choices)
}

// TestLLMQuestionGen_Parse_CodeFencesStripped covers the most common LLM
// misbehaviour: wrapping the JSON array in a ```json ... ``` Markdown
// fence despite the prompt's "no prose" instruction. parseGeneratedQuestions
// must strip the fences before json.Unmarshal sees them.
func TestLLMQuestionGen_Parse_CodeFencesStripped(t *testing.T) {
	resp := "```json\n" +
		`[{"id":"foo","text":"why?","kind":"free_text"}]` +
		"\n```"
	got := parseGeneratedQuestions(resp)
	if !assert.Len(t, got, 1, "fenced JSON must still parse") {
		t.FailNow()
	}
	assert.Equal(t, "free_text", got[0].Kind)
	assert.Equal(t, "why?", got[0].Text)
}

// TestLLMQuestionGen_Parse_SkipsMalformedSurvivesValid asserts the partial-
// success behaviour: when a batch contains a mix of valid and malformed
// entries (empty text, totally invalid Kind), the good ones still survive
// while the bad ones are silently dropped. This is the core defensive
// guarantee — a single bad row from the LLM cannot blank the buffer.
func TestLLMQuestionGen_Parse_SkipsMalformedSurvivesValid(t *testing.T) {
	resp := `[
		{"id": "good_one", "text": "valid question?", "kind": "free_text"},
		{"id": "empty_text_skip", "text": "", "kind": "free_text"},
		{"id": "weird_kind_kept", "text": "another?", "kind": "lol_what"},
		{"id": "good_two", "text": "second valid?", "kind": "choice",
		 "choices": ["a", "b"]}
	]`
	got := parseGeneratedQuestions(resp)
	// Empty-text entry should be dropped (2 + 1 weird-kind = 3 survivors).
	// The "weird_kind_kept" entry survives because parseGeneratedQuestions
	// defaults unknown Kind to "free_text".
	assert.Len(t, got, 3, "malformed (empty text) skipped; valid + weird-kind survive")

	ids := map[string]daemon.PendingQuestion{}
	for _, q := range got {
		ids[q.ID] = q
	}
	// Both well-formed entries land in the output.
	_, gotGoodOne := ids["llm-good_one"]
	_, gotGoodTwo := ids["llm-good_two"]
	assert.True(t, gotGoodOne, "valid free_text survived")
	assert.True(t, gotGoodTwo, "valid choice survived")

	// The unknown-kind entry should be normalised to free_text.
	weird, gotWeird := ids["llm-weird_kind_kept"]
	if assert.True(t, gotWeird, "unknown-kind entry should survive with Kind=free_text") {
		assert.Equal(t, "free_text", weird.Kind,
			"unknown Kind must normalise to free_text per parser contract")
	}
}

// TestLLMQuestionGen_Parse_ChoiceWithoutChoicesDropped pins the documented
// rule that a "choice" entry with an empty/missing choices array is
// unusable and must be dropped (not silently downgraded to free_text).
// Without this, the popup would render a choice question with no buttons.
func TestLLMQuestionGen_Parse_ChoiceWithoutChoicesDropped(t *testing.T) {
	resp := `[
		{"id": "broken_choice", "text": "pick one", "kind": "choice"},
		{"id": "another_broken", "text": "another", "kind": "choice", "choices": []},
		{"id": "all_blank_choices", "text": "blanks", "kind": "choice", "choices": ["", "  ", ""]}
	]`
	got := parseGeneratedQuestions(resp)
	assert.Empty(t, got, "choice entries with no valid choices must be dropped, not downgraded")
}

// TestLLMQuestionGen_Parse_MissingIDMinted covers the ID-minting fallback:
// when the LLM forgets the "id" field entirely (or supplies an empty one),
// parseGeneratedQuestions should hash the question text and prefix it with
// "llm-". This guarantees every parsed entry has a stable, unique ID we
// can use as PendingQuestion.ID later.
func TestLLMQuestionGen_Parse_MissingIDMinted(t *testing.T) {
	resp := `[
		{"text": "no id at all", "kind": "free_text"},
		{"id": "", "text": "empty id", "kind": "free_text"}
	]`
	got := parseGeneratedQuestions(resp)
	if !assert.Len(t, got, 2, "missing/empty IDs should be minted, not skip the row") {
		t.FailNow()
	}
	for _, q := range got {
		assert.True(t, strings.HasPrefix(q.ID, "llm-"),
			"minted ID must start with llm- prefix; got %q", q.ID)
		// 8-char hash + "llm-" prefix = 12 chars exactly when the hash is full
		// width; we just check the prefix and non-empty hash.
		assert.True(t, len(q.ID) > len("llm-"),
			"minted ID must contain a hash suffix; got %q", q.ID)
	}
	// Different question text should yield different minted IDs (collision
	// would be a bug in shortHash, not catastrophic but worth pinning).
	assert.NotEqual(t, got[0].ID, got[1].ID,
		"distinct question text should mint distinct IDs")
}

// TestLLMQuestionGen_Parse_IDIsAlwaysLLMPrefixed pins the prefix invariant
// even when the LLM supplies an ID that already starts with a different
// prefix. This is the "every LLM-sourced ID is namespaced" guarantee that
// the dedup-vs-seed-bank tests downstream depend on.
func TestLLMQuestionGen_Parse_IDIsAlwaysLLMPrefixed(t *testing.T) {
	cases := []struct {
		rawID    string
		expectIn string
	}{
		{rawID: "morning_or_night", expectIn: "llm-morning_or_night"}, // collides with seed!
		{rawID: "weekend_music", expectIn: "llm-weekend_music"},
		{rawID: "llm-already_prefixed", expectIn: "llm-already_prefixed"},
		{rawID: "  WeIrD Case  ", expectIn: "llm-weird_case"},
	}
	for _, tc := range cases {
		resp := `[{"id":"` + tc.rawID + `","text":"q?","kind":"free_text"}]`
		got := parseGeneratedQuestions(resp)
		if !assert.Len(t, got, 1, "rawID=%q should parse exactly one entry", tc.rawID) {
			continue
		}
		assert.Equal(t, tc.expectIn, got[0].ID,
			"rawID=%q must normalise to %q (got %q)", tc.rawID, tc.expectIn, got[0].ID)
		assert.True(t, strings.HasPrefix(got[0].ID, "llm-"),
			"every minted ID must use the llm- prefix even when the LLM supplies one without it")
	}
}

// ─── PickQuestion mixed-pool integration ───────────────────────────────────

// TestPickQuestion_MixedPool_GateOffIgnoresBuffer pins the byte-identical
// gate guarantee: with cfg.Widgets.Pet.QA.LLMQuestions=false, PickQuestion
// must NEVER surface a buffered LLM question, even when the buffer is
// populated with attractive entries. This is the regression check that
// proves Phase 3 is invisible when opt-in is off.
func TestPickQuestion_MixedPool_GateOffIgnoresBuffer(t *testing.T) {
	withCleanQuestionBuffer(t)
	reseedPetQARand(t, 42)
	// Seed five LLM questions; if the gate leaks they would surface in a
	// 1000-pick run. Use Kind=choice so they're not affected by free-text
	// opt-outs.
	seedQuestionBuffer([]daemon.PendingQuestion{
		llmQ("llm-q1", "llm question 1", "choice", "a", "b"),
		llmQ("llm-q2", "llm question 2", "choice", "a", "b"),
		llmQ("llm-q3", "llm question 3", "choice", "a", "b"),
		llmQ("llm-q4", "llm question 4", "choice", "a", "b"),
		llmQ("llm-q5", "llm question 5", "choice", "a", "b"),
	})
	pet := stateWithConsentAnswered()
	qa := defaultQAConfig()
	qa.LLMQuestions = false // <- explicit: gate off

	for i := 0; i < 200; i++ {
		// PickQuestion under gate-off must not touch the buffer.
		q := PickQuestion(pet, qa, petTimeNow)
		if q == nil {
			continue
		}
		assert.NotEqual(t, "llm", q.Source,
			"with LLMQuestions=false, no pick may have Source=llm (got id=%s)", q.ID)
		assert.False(t, strings.HasPrefix(q.ID, "llm-"),
			"with LLMQuestions=false, no pick may have an llm- prefixed id (got %s)", q.ID)
	}

	// Buffer must remain untouched (no questions consumed).
	questionBufferMutex.Lock()
	bufLen := len(questionBuffer)
	questionBufferMutex.Unlock()
	assert.Equal(t, 5, bufLen, "buffer must not be drained when gate is off")
}

// TestPickQuestion_MixedPool_GateOnEmptyBufferActsLikeSeedOnly verifies
// that flipping LLMQuestions on but leaving the buffer empty does NOT
// change behaviour vs. Phase 2 — picks are still drawn from the seed bank.
// Catches a regression where an empty llmPool would somehow short-circuit
// or otherwise inhibit the seed pick.
func TestPickQuestion_MixedPool_GateOnEmptyBufferActsLikeSeedOnly(t *testing.T) {
	withCleanQuestionBuffer(t) // buffer stays empty
	reseedPetQARand(t, 1)
	pet := stateWithConsentAnswered()
	qa := defaultQAConfig()
	qa.LLMQuestions = true // gate on, buffer empty

	q := PickQuestion(pet, qa, petTimeNow)
	if !assert.NotNil(t, q, "with gate on + empty buffer, seed bank should still produce a pick") {
		t.FailNow()
	}
	assert.Equal(t, "bank", q.Source, "empty LLM pool → all picks should come from seed bank")
}

// TestPickQuestion_MixedPool_DrawsFromBoth runs many picks and asserts both
// pools surface in proportion to their relative sizes. With the
// implementation's uniform-random mixing policy, the LLM share should be
// roughly len(llmPool) / (len(eligible) + len(llmPool)). We pin a tight
// tolerance: with 1000 picks and a 6/(29+6) ≈ 17% expected share, ±10pp
// is comfortably outside chance.
func TestPickQuestion_MixedPool_DrawsFromBoth(t *testing.T) {
	withCleanQuestionBuffer(t)
	reseedPetQARand(t, 12345)

	// Seed 6 LLM questions (all choice so they're not free-text-filtered).
	// Use IDs that DON'T collide with the seed bank.
	llmEntries := []daemon.PendingQuestion{
		llmQ("llm-extra_a", "extra a", "choice", "x", "y"),
		llmQ("llm-extra_b", "extra b", "choice", "x", "y"),
		llmQ("llm-extra_c", "extra c", "choice", "x", "y"),
		llmQ("llm-extra_d", "extra d", "choice", "x", "y"),
		llmQ("llm-extra_e", "extra e", "choice", "x", "y"),
		llmQ("llm-extra_f", "extra f", "choice", "x", "y"),
	}

	qa := defaultQAConfig()
	qa.LLMQuestions = true

	const iterations = 1000
	llmPicks, bankPicks := 0, 0
	for i := 0; i < iterations; i++ {
		// Re-seed the buffer each iteration so picks don't drain it (each
		// PickQuestion of an LLM entry removes it from the buffer).
		seedQuestionBuffer(llmEntries)

		// State must NOT have the just-picked question in answered history
		// (otherwise it gets filtered as "already answered"). Reset to the
		// minimal post-consent state each iteration.
		pet := stateWithConsentAnswered()

		q := PickQuestion(pet, qa, petTimeNow)
		if q == nil {
			continue
		}
		switch q.Source {
		case "llm":
			llmPicks++
		case "bank":
			bankPicks++
		default:
			t.Fatalf("unexpected Source=%q in mixed-pool pick (id=%s)", q.Source, q.ID)
		}
	}

	// Both pools must produce SOME picks; the actual proportions are what
	// the brief asks us to assert.
	assert.NotZero(t, llmPicks, "LLM pool must surface at least once over %d iterations", iterations)
	assert.NotZero(t, bankPicks, "seed pool must surface at least once over %d iterations", iterations)

	// Compute expected ratio. The seed bank has 30 entries; consent is
	// skipped (special-cased) AND consent is in AnsweredQuestions so it
	// would be filtered anyway → 29 eligible seed entries.
	const eligibleSeedSize = 29
	expectedLLMShare := float64(len(llmEntries)) / float64(eligibleSeedSize+len(llmEntries))
	gotLLMShare := float64(llmPicks) / float64(llmPicks+bankPicks)
	delta := gotLLMShare - expectedLLMShare
	if delta < 0 {
		delta = -delta
	}
	assert.True(t, delta < 0.10,
		"LLM share %.3f should be within ±0.10 of expected %.3f (over %d picks)",
		gotLLMShare, expectedLLMShare, iterations)
}

// TestPickQuestion_MixedPool_PickedLLMQuestionRemoved verifies that a
// successful LLM pick is dropped from the buffer immediately so a second
// pick (before AnswerQuestion has had a chance to record the answer)
// cannot redraw the same entry. This is the "no double-serve" guarantee
// that removeLLMQuestion enforces.
func TestPickQuestion_MixedPool_PickedLLMQuestionRemoved(t *testing.T) {
	withCleanQuestionBuffer(t)
	reseedPetQARand(t, 7)

	// Only ONE LLM entry in the buffer so the second pick can't randomly
	// hit the same one — if it comes back, the removal failed.
	soloLLM := llmQ("llm-only_one", "only one", "choice", "yes", "no")
	seedQuestionBuffer([]daemon.PendingQuestion{soloLLM})

	pet := stateWithConsentAnswered()
	qa := defaultQAConfig()
	qa.LLMQuestions = true

	// Hammer PickQuestion repeatedly until we see the LLM pick (statistically
	// inevitable within 200 tries given the seed bank has only 29 entries
	// vs 1 LLM ≈ 3.3% pick rate).
	var llmPickedOnce bool
	for i := 0; i < 200 && !llmPickedOnce; i++ {
		q := PickQuestion(pet, qa, petTimeNow)
		if q != nil && q.Source == "llm" && q.ID == "llm-only_one" {
			llmPickedOnce = true
		}
	}
	if !llmPickedOnce {
		t.Fatalf("LLM entry never picked in 200 iterations — random source may be broken")
	}

	// Buffer must now be empty (the entry was removed when picked).
	questionBufferMutex.Lock()
	bufLen := len(questionBuffer)
	questionBufferMutex.Unlock()
	assert.Equal(t, 0, bufLen, "picked LLM entry must be removed from buffer")

	// And no subsequent pick can possibly return the same LLM entry.
	for i := 0; i < 100; i++ {
		q := PickQuestion(pet, qa, petTimeNow)
		if q == nil {
			continue
		}
		assert.NotEqual(t, "llm-only_one", q.ID,
			"removed LLM entry must never be re-picked (iter=%d)", i)
	}
}

// TestPickQuestion_MixedPool_AnsweredLLMFiltered pins the cross-pool dedup
// rule: an LLM-generated question whose ID is already in AnsweredQuestions
// is filtered before the random draw, just like seed entries. Otherwise
// a user who answered an LLM question would see it again on the next
// generation cycle.
func TestPickQuestion_MixedPool_AnsweredLLMFiltered(t *testing.T) {
	withCleanQuestionBuffer(t)
	reseedPetQARand(t, 99)

	// Buffer contains one LLM entry whose ID we'll mark as already-answered.
	target := llmQ("llm-already_answered", "should be filtered", "choice", "a", "b")
	seedQuestionBuffer([]daemon.PendingQuestion{target})

	pet := stateWithConsentAnswered()
	// Mark the LLM entry as recently answered.
	pet.AnsweredQuestions = append(pet.AnsweredQuestions, daemon.AnsweredQuestion{
		ID:        "llm-already_answered",
		Text:      "should be filtered",
		Answer:    "a",
		Kind:      "choice",
		Timestamp: petTimeNow.Add(-1 * time.Hour),
	})

	qa := defaultQAConfig()
	qa.LLMQuestions = true

	// Over many picks, the answered LLM ID must NEVER come back.
	for i := 0; i < 300; i++ {
		q := PickQuestion(pet, qa, petTimeNow)
		if q == nil {
			continue
		}
		assert.NotEqual(t, "llm-already_answered", q.ID,
			"already-answered LLM id must be filtered just like seed entries (iter=%d)", i)
	}
	// Buffer entry was NEVER drawn → still in buffer.
	questionBufferMutex.Lock()
	bufLen := len(questionBuffer)
	questionBufferMutex.Unlock()
	assert.Equal(t, 1, bufLen,
		"answered LLM entry should remain in buffer (never picked, never removed)")
}

// TestLLMQuestionGen_BufferRoundTrip verifies the disk persistence path:
// saveQuestionBuffer writes one JSON-encoded entry per line, and a fresh
// loadQuestionBuffer restores the same slice. Drift here would mean a
// daemon restart silently loses the generated buffer.
func TestLLMQuestionGen_BufferRoundTrip(t *testing.T) {
	bufferPath := withCleanQuestionBuffer(t)

	seedQuestionBuffer([]daemon.PendingQuestion{
		llmQ("llm-roundtrip_a", "round trip a", "free_text"),
		llmQ("llm-roundtrip_b", "round trip b", "choice", "yes", "no"),
	})

	// Flush to disk; the file should now contain two lines of JSON.
	saveQuestionBuffer()
	data, err := os.ReadFile(bufferPath)
	if !assert.NoError(t, err, "buffer file must be created by saveQuestionBuffer") {
		t.FailNow()
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	assert.Len(t, lines, 2, "saveQuestionBuffer should write one line per entry")

	// Clear the in-memory buffer and reload from disk.
	questionBufferMutex.Lock()
	questionBuffer = nil
	questionBufferMutex.Unlock()

	loadQuestionBuffer()

	questionBufferMutex.Lock()
	got := append([]daemon.PendingQuestion(nil), questionBuffer...)
	questionBufferMutex.Unlock()

	if !assert.Len(t, got, 2, "loaded buffer must contain both saved entries") {
		t.FailNow()
	}
	ids := map[string]bool{got[0].ID: true, got[1].ID: true}
	assert.True(t, ids["llm-roundtrip_a"])
	assert.True(t, ids["llm-roundtrip_b"])
}

// TestLLMQuestionGen_LoadSkipsMalformedLines pins the load-time
// resilience: a corrupted buffer file (mid-write crash, partial line,
// totally garbage line) must not crash the daemon — bad lines are silently
// skipped and the good ones survive. The next refresh cycle will replace
// the buffer entirely.
func TestLLMQuestionGen_LoadSkipsMalformedLines(t *testing.T) {
	bufferPath := withCleanQuestionBuffer(t)

	// Construct a file with mixed-quality lines.
	good1 := daemon.PendingQuestion{ID: "llm-good_a", Text: "good a", Kind: "free_text", Source: "llm"}
	good2 := daemon.PendingQuestion{ID: "llm-good_b", Text: "good b", Kind: "choice", Choices: []string{"y", "n"}, Source: "llm"}
	rawGood1, _ := json.Marshal(good1)
	rawGood2, _ := json.Marshal(good2)
	mixed := strings.Join([]string{
		string(rawGood1),
		"not json at all {{{",
		"",                       // empty line — should be silently skipped
		`{"id":"","text":""}`,   // empty required fields — skipped
		string(rawGood2),
	}, "\n")
	if err := os.WriteFile(bufferPath, []byte(mixed), 0644); err != nil {
		t.Fatalf("write test buffer: %v", err)
	}

	loadQuestionBuffer()

	questionBufferMutex.Lock()
	loaded := append([]daemon.PendingQuestion(nil), questionBuffer...)
	questionBufferMutex.Unlock()

	if !assert.Len(t, loaded, 2, "two good lines should survive; three bad lines should be skipped") {
		t.FailNow()
	}
	gotIDs := []string{loaded[0].ID, loaded[1].ID}
	assert.Contains(t, gotIDs, "llm-good_a")
	assert.Contains(t, gotIDs, "llm-good_b")
}

// ─── De-dup vs seed bank ───────────────────────────────────────────────────

// TestLLMQuestionGen_DedupSeed_PrefixGuarantee verifies that even when the
// LLM proposes an ID identical to a real seed-bank ID
// (e.g. "morning_or_night"), the parser ALWAYS prefixes it with "llm-",
// so the two namespaces can never collide. This is the core dedup
// guarantee the prompt rules rely on.
func TestLLMQuestionGen_DedupSeed_PrefixGuarantee(t *testing.T) {
	// Real seed bank IDs that the LLM might plausibly echo back.
	collidingIDs := []string{
		"consent",
		"morning_or_night",
		"tabs_or_spaces",
		"coffee_or_tea",
		"editor_loyalty",
		"cat_favourite_food",
		"how_are_you_today",
	}
	for _, seedID := range collidingIDs {
		seed, ok := findSeedByID(seedID)
		if !ok {
			t.Fatalf("seed bank missing expected id %q — test premise broken", seedID)
		}
		// Build a fake LLM response whose "id" field is literally a seed ID.
		resp := `[{"id":"` + seedID + `","text":"q?","kind":"free_text"}]`
		got := parseGeneratedQuestions(resp)
		if !assert.Len(t, got, 1, "LLM proposal echoing seed id %q must still parse", seedID) {
			continue
		}
		// The parsed ID must NOT equal the seed ID — it must be prefixed.
		assert.NotEqual(t, seed.ID, got[0].ID,
			"LLM-side id must never equal seed id %q after normalisation", seedID)
		assert.Equal(t, "llm-"+seedID, got[0].ID,
			"LLM-side id for echo of seed %q must normalise to llm-%s", seedID, seedID)
	}
}

// TestLLMQuestionGen_DedupSeed_PickQuestionPrefersDistinctNamespaces makes
// the dedup guarantee observable end-to-end: a parsed LLM entry that
// LOOKS like the seed bank's "morning_or_night" must NOT be filtered out
// by recently-answering the real seed "morning_or_night". The two live in
// distinct ID namespaces.
func TestLLMQuestionGen_DedupSeed_PickQuestionPrefersDistinctNamespaces(t *testing.T) {
	withCleanQuestionBuffer(t)
	reseedPetQARand(t, 314)

	// Parse an LLM-echo of an existing seed ID. The parser must turn this
	// into a distinct "llm-morning_or_night" entry.
	resp := `[{"id":"morning_or_night","text":"are you a morning lark or night owl?","kind":"choice","choices":["lark","owl"]}]`
	parsed := parseGeneratedQuestions(resp)
	if !assert.Len(t, parsed, 1, "LLM echo of seed id must still produce one entry") {
		t.FailNow()
	}
	llmEcho := parsed[0]
	assert.Equal(t, "llm-morning_or_night", llmEcho.ID,
		"namespace prefix is the only thing keeping the two from colliding")

	// Seed only the LLM-echo into the buffer.
	seedQuestionBuffer([]daemon.PendingQuestion{llmEcho})

	// In pet state, mark the REAL seed "morning_or_night" as recently
	// answered. If the dedup logic incorrectly conflated the two
	// namespaces, the LLM echo would be filtered too and the buffer would
	// look "empty" for picking purposes.
	pet := stateWithConsentAnswered()
	pet.AnsweredQuestions = append(pet.AnsweredQuestions, daemon.AnsweredQuestion{
		ID:        "morning_or_night",
		Text:      "real seed question",
		Answer:    "morning person",
		Kind:      "choice",
		Timestamp: petTimeNow.Add(-1 * time.Hour),
	})

	qa := defaultQAConfig()
	qa.LLMQuestions = true

	// Over enough iterations, the LLM-echo MUST be picked at least once —
	// it shares the namespace prefix with a fresh ID, so the recently-
	// answered filter doesn't touch it.
	pickedLLMEcho := false
	for i := 0; i < 200 && !pickedLLMEcho; i++ {
		// Re-seed each iteration because picking drains the buffer.
		seedQuestionBuffer([]daemon.PendingQuestion{llmEcho})
		q := PickQuestion(pet, qa, petTimeNow)
		if q != nil && q.ID == "llm-morning_or_night" {
			pickedLLMEcho = true
		}
	}
	assert.True(t, pickedLLMEcho,
		"LLM-echo of a seed id must be pickable; only the prefix keeps the namespaces distinct")
}

// ─── Privacy guard cross-cutting (Phase 3A + 3B) ───────────────────────────

// TestLLM_PrivacyGate_AllFeatures_OptedOutBlocksBoth is THE cross-cutting
// privacy test. The Phase 3 design routes BOTH LLM question generation
// AND LLM distillation through the same pet.QAOptedOut guard. Either
// path leaking past the guard would be a privacy bug. distillation-author's
// own tests cover the distillation side; this test pins that the question
// path is gated by the same flag and that the two stay in lockstep.
func TestLLM_PrivacyGate_AllFeatures_OptedOutBlocksBoth(t *testing.T) {
	withCleanQuestionBuffer(t)
	// Snapshot the distillation cadence sentinel so this test doesn't
	// stomp on other distillation tests' lastLLMDistillation state.
	prevLastDistill := lastLLMDistillation
	t.Cleanup(func() { lastLLMDistillation = prevLastDistill })
	lastLLMDistillation = time.Time{}

	reseedPetQARand(t, 1)
	// Seed a populated LLM buffer so we can prove the picker never reaches it.
	seedQuestionBuffer([]daemon.PendingQuestion{
		llmQ("llm-aaa", "test a", "choice", "a", "b"),
		llmQ("llm-bbb", "test b", "choice", "a", "b"),
		llmQ("llm-ccc", "test c", "choice", "a", "b"),
	})

	// User has opted out: privacy guardrail must dominate the LLM gate.
	pet := stateWithConsentAnswered()
	pet.QAOptedOut = true
	// Give the distillation path something to chew on so it would fire if
	// the gate were misplaced. Free-text answers, all undistilled.
	for i := 0; i < 20; i++ {
		pet.AnsweredQuestions = append(pet.AnsweredQuestions, daemon.AnsweredQuestion{
			ID:        "free_optout_" + string(rune('a'+i)),
			Text:      "free text q",
			Answer:    "user-text",
			Kind:      "free_text",
			Timestamp: petTimeNow.Add(time.Duration(i) * time.Minute),
		})
	}

	qa := defaultQAConfig()
	qa.LLMQuestions = true // <- LLM master switch is ON

	// Path 1: question generation. PickQuestion must return nil and must
	// not consume the buffer (proves the gate fires BEFORE the LLM draw).
	q := PickQuestion(pet, qa, petTimeNow)
	assert.Nil(t, q, "QAOptedOut must block PickQuestion regardless of LLMQuestions=%v", qa.LLMQuestions)
	questionBufferMutex.Lock()
	remaining := len(questionBuffer)
	questionBufferMutex.Unlock()
	assert.Equal(t, 3, remaining,
		"opted-out user → LLM buffer must not be drained (gate runs BEFORE draw)")

	// Path 2: distillation. ShouldRunLLMDistillation must return false even
	// with 20 undistilled answers and the LLM gate on — the privacy
	// guardrail is the same flag.
	should, batch := ShouldRunLLMDistillation(pet, qa, petTimeNow)
	assert.False(t, should,
		"QAOptedOut must block ShouldRunLLMDistillation regardless of LLMQuestions=%v", qa.LLMQuestions)
	assert.Nil(t, batch, "opted-out user → no batch returned for distillation")

	// Sanity: with QAOptedOut cleared, the SAME state DOES trigger
	// distillation. This proves the only thing blocking it was the gate
	// (not e.g. an unrelated missing field).
	pet.QAOptedOut = false
	should2, batch2 := ShouldRunLLMDistillation(pet, qa, petTimeNow)
	assert.True(t, should2, "after clearing QAOptedOut, distillation should fire")
	assert.NotEmpty(t, batch2, "after clearing QAOptedOut, batch must be non-empty")
}

// TestLLM_PrivacyGate_AllFeatures_LLMOffBlocksBoth pins the inverse: with
// QAOptedOut=false (user consented) but LLMQuestions=false (config gate
// off), NEITHER LLM path may run. This catches a regression where one
// side might accidentally read consent as "all LLM features on".
func TestLLM_PrivacyGate_AllFeatures_LLMOffBlocksBoth(t *testing.T) {
	withCleanQuestionBuffer(t)
	prevLastDistill := lastLLMDistillation
	t.Cleanup(func() { lastLLMDistillation = prevLastDistill })
	lastLLMDistillation = time.Time{}

	reseedPetQARand(t, 2)
	seedQuestionBuffer([]daemon.PendingQuestion{
		llmQ("llm-x", "x", "choice", "a", "b"),
	})

	pet := stateWithConsentAnswered()
	pet.QAOptedOut = false // consented
	for i := 0; i < 20; i++ {
		pet.AnsweredQuestions = append(pet.AnsweredQuestions, daemon.AnsweredQuestion{
			ID:        "free_consented_" + string(rune('a'+i)),
			Text:      "free text q",
			Answer:    "user-text",
			Kind:      "free_text",
			Timestamp: petTimeNow.Add(time.Duration(i) * time.Minute),
		})
	}

	qa := defaultQAConfig()
	qa.LLMQuestions = false // <- LLM gate OFF

	// Picker: cannot return any llm-prefixed entry.
	for i := 0; i < 50; i++ {
		q := PickQuestion(pet, qa, petTimeNow)
		if q == nil {
			continue
		}
		assert.NotEqual(t, "llm", q.Source,
			"LLMQuestions=false must block LLM picks even after consent (iter=%d, id=%s)", i, q.ID)
	}

	// Distillation: gate off → no fire.
	should, _ := ShouldRunLLMDistillation(pet, qa, petTimeNow)
	assert.False(t, should, "LLMQuestions=false must block distillation even after consent")
}

// ─── Async safety (Phase 3B) ───────────────────────────────────────────────

// TestLLMDistillation_AsyncRace exercises the production path that's most
// at risk of a data race: applyLLMDistillation is the callback that
// RunLLMDistillationBackground invokes from a goroutine, and it MUST take
// c.stateMu around every mutation. Concurrent readers (which hold the
// read-side of c.stateMu) and the apply callback must not race.
//
// We can't easily trigger the real RunLLMDistillationBackground in a test
// (it short-circuits when llmClient == nil, and the gollm interface is
// non-trivial to stub) so we directly exercise applyLLMDistillation under
// concurrent readers and writers. This validates the SAME locking
// discipline the production goroutine relies on, and the test is
// race-clean under `go test -race`.
func TestLLMDistillation_AsyncRace(t *testing.T) {
	t.Setenv("TABBY_STATE_DIR", t.TempDir()) // best-effort isolation for save

	c := newTestCoordinator(t)
	// Seed the live pet state with a non-trivial Q&A history so the
	// snapshot/apply path has data to copy.
	c.pet.AnsweredQuestions = []daemon.AnsweredQuestion{
		{ID: "free_race_a", Text: "?", Answer: "a", Kind: "free_text",
			Timestamp: petTimeNow},
		{ID: "free_race_b", Text: "?", Answer: "b", Kind: "free_text",
			Timestamp: petTimeNow.Add(time.Minute)},
	}
	c.pet.Traits = []daemon.PersonalityTrait{
		{Text: "initial trait", Source: "free_race_a", Confidence: 0.7,
			AddedAt: petTimeNow},
	}

	var wg sync.WaitGroup
	const iterations = 50

	// Writer goroutines: each one runs applyLLMDistillation with a
	// trivial mutator that touches the Q&A slices. The mutator runs
	// inside the snapshot's frame, so applyLLMDistillation must
	// correctly serialise the snapshot/apply round-trip.
	const writers = 4
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				c.applyLLMDistillation(func(p *daemon.PetState) {
					// Mutate traits and the Distilled flag in a way that
					// would race against a concurrent reader if the lock
					// were missing.
					p.Traits = append([]daemon.PersonalityTrait(nil), p.Traits...)
					if len(p.AnsweredQuestions) > 0 {
						p.AnsweredQuestions[0].Distilled = true
					}
				})
			}
		}(w)
	}

	// Reader goroutines: simulate the rest of the daemon reading pet
	// state under stateMu.RLock(). If applyLLMDistillation forgets to
	// take the write lock, the race detector will fire here.
	const readers = 4
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				c.stateMu.RLock()
				_ = len(c.pet.AnsweredQuestions)
				_ = len(c.pet.Traits)
				if c.pet.PendingQuestion != nil {
					_ = c.pet.PendingQuestion.ID
				}
				c.stateMu.RUnlock()
			}
		}()
	}

	wg.Wait()
	// Sanity: after all goroutines complete, the answered-questions slice
	// is still intact (length unchanged — applyLLMDistillation only
	// mutated existing entries).
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	assert.Equal(t, 2, len(c.pet.AnsweredQuestions),
		"race test must not corrupt or truncate the answered slice")
	// The Distilled flag was flipped many times — assert idempotency.
	assert.True(t, c.pet.AnsweredQuestions[0].Distilled,
		"writer goroutines should have set Distilled=true on entry 0")
}
