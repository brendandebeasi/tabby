// Package daemon — Phase 1 unit tests for the pet Q&A personality-building
// loop. Companion to:
//
//   - pet_questions.go   (SeedQuestions bank + QuestionDef shape)
//   - pet_qa.go          (PickQuestion / AnswerQuestion / ForgetAnswer /
//     DistillTrait / RecentAnswers logic)
//   - llm.go             (buildPetContext / petPersonalitySection)
//   - protocol.go        (daemon.PetState Q&A field additions)
//   - coordinator.go     (local petState Q&A field additions; same JSON tags)
//
// These tests exercise the logic in isolation against the wire-format
// daemon.PetState type because that's what logic-author's functions
// accept. The JSON round-trip block covers BOTH the wire-format PetState
// and the local-only petState (the daemon serialises the latter to
// pet.json, so both shapes must accept old pet.json files without errors).
//
// Tests deliberately avoid touching the live tmux socket — the CLI's
// network path is intentionally not exercised here; only the in-process
// pure logic is.
package daemon

import (
	gocontext "context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/brendandebeasi/tabby/cmd/tabby/internal/pet"
	"github.com/brendandebeasi/tabby/pkg/config"
	"github.com/brendandebeasi/tabby/pkg/daemon"
	"github.com/stretchr/testify/assert"
)

// mustOK is a thin shim that mimics testify/require semantics on top of
// testify/assert. The vendor/ tree in this repo ships assert but not
// require, so we cannot import require directly. Callers pass the bool
// result of an assert.* invocation; if it's false we call t.FailNow so
// the test stops at that line rather than dereferencing nil pointers or
// indexing into empty slices in the lines below.
func mustOK(t *testing.T, ok bool) {
	t.Helper()
	if !ok {
		t.FailNow()
	}
}

// ─── helpers ───────────────────────────────────────────────────────────────

// defaultQAConfig returns the qa config the production defaulter
// (pkg/config/loader.go) produces for a fresh pet.json. Tests reach for
// this when they need realistic cooldown/expiry windows instead of
// zero-values.
func defaultQAConfig() config.PetWidgetQA {
	return config.PetWidgetQA{
		Disabled:             false,
		CooldownHours:        24,
		ExpireHours:          48,
		TeaserEveryNThoughts: 3,
		FreeTextDisabled:     false,
		LLMQuestions:         false,
	}
}

// petTimeNow is a fixed timestamp used by deterministic tests. Picked far
// from any natural boundaries so cooldown / staleness arithmetic always
// stays inside a single day/year.
var petTimeNow = time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)

// freshState returns a daemon.PetState with no Q&A history. Used by the
// "first-time" path tests.
func freshState() *daemon.PetState {
	return &daemon.PetState{State: "idle"}
}

// stateWithDummyAnswer returns a state whose AnsweredQuestions has one
// non-consent entry so the first-time consent gate is bypassed. The
// dummy answer is a real bank ID so any "skip already answered" logic
// can act on it. The timestamp is chosen so the entry is fresh (well
// inside the 6-month staleness window).
func stateWithDummyAnswer(id string) *daemon.PetState {
	return &daemon.PetState{
		State: "idle",
		AnsweredQuestions: []daemon.AnsweredQuestion{
			{
				ID:        id,
				Text:      "dummy",
				Answer:    "dummy",
				Kind:      "choice",
				Timestamp: petTimeNow.Add(-1 * time.Hour),
			},
		},
	}
}

// ─── PickQuestion ──────────────────────────────────────────────────────────

func TestPickQuestion_FirstCallReturnsConsent(t *testing.T) {
	// First-time path: regardless of which way the bank is iterated, an
	// empty AnsweredQuestions slice must surface the consent question. The
	// implementation special-cases this via findSeedByID("consent") so the
	// returned ID is stable even when the rest of the bank is shuffled.
	pet := freshState()
	q := PickQuestion(pet, defaultQAConfig(), petTimeNow)
	mustOK(t, assert.NotNil(t, q, "consent question should be returned on first call"))
	assert.Equal(t, "consent", q.ID)
	assert.Equal(t, "choice", q.Kind)
	assert.NotEmpty(t, q.Choices, "consent has multi-choice answers")
	// Source is set to "bank" so the daemon can distinguish bank-sourced
	// from future LLM-sourced questions.
	assert.Equal(t, "bank", q.Source)
}

func TestPickQuestion_ReturnsNilWhenQAOptedOut(t *testing.T) {
	// Runtime opt-out (the user picked "No thanks" to the consent question)
	// must short-circuit before even the consent gate. Otherwise the cat
	// would re-ask consent after the user said no.
	pet := freshState()
	pet.QAOptedOut = true
	q := PickQuestion(pet, defaultQAConfig(), petTimeNow)
	assert.Nil(t, q)
}

func TestPickQuestion_ReturnsNilWhenConfigDisabled(t *testing.T) {
	// Config disable is the kill-switch — feature stays off regardless of
	// runtime state. Mirrors qa.Disabled in pkg/config/config.go.
	pet := freshState()
	qa := defaultQAConfig()
	qa.Disabled = true
	q := PickQuestion(pet, qa, petTimeNow)
	assert.Nil(t, q)
}

func TestPickQuestion_ReturnsNilDuringCooldown(t *testing.T) {
	// QuestionCooldown set to "future" must suppress all questions. Tests
	// pass `now` explicitly so this case is deterministic — no real clock.
	pet := stateWithDummyAnswer("morning_or_night")
	pet.QuestionCooldown = petTimeNow.Add(1 * time.Hour) // future
	q := PickQuestion(pet, defaultQAConfig(), petTimeNow)
	assert.Nil(t, q)
}

func TestPickQuestion_AllowsAfterCooldownElapses(t *testing.T) {
	// Mirror test: when cooldown is in the past, picking proceeds. This
	// catches off-by-one errors in the time comparison.
	pet := stateWithDummyAnswer("morning_or_night")
	pet.QuestionCooldown = petTimeNow.Add(-1 * time.Hour) // past
	q := PickQuestion(pet, defaultQAConfig(), petTimeNow)
	mustOK(t, assert.NotNil(t, q, "cooldown elapsed should allow a pick"))
	assert.NotEqual(t, "consent", q.ID, "consent only on first call")
}

func TestPickQuestion_SkipsRecentlyAnswered(t *testing.T) {
	// Build a state where every non-consent bank ID is "freshly answered"
	// inside the 6-month staleness window. The picker should return nil
	// because nothing is eligible. (We exclude consent — that's
	// special-cased and never picked again once answered.)
	pet := &daemon.PetState{State: "idle"}
	for _, q := range SeedQuestions {
		if q.ID == "consent" {
			continue
		}
		pet.AnsweredQuestions = append(pet.AnsweredQuestions, daemon.AnsweredQuestion{
			ID:        q.ID,
			Text:      q.Text,
			Answer:    "x",
			Kind:      q.Kind,
			Timestamp: petTimeNow.Add(-7 * 24 * time.Hour), // 1 week old, well inside 6mo
		})
	}
	// Also seed the consent answer so the first-time gate is satisfied.
	pet.AnsweredQuestions = append(pet.AnsweredQuestions, daemon.AnsweredQuestion{
		ID: "consent", Text: "consent", Answer: "Yes, ask away", Kind: "choice",
		Timestamp: petTimeNow.Add(-30 * 24 * time.Hour),
	})

	q := PickQuestion(pet, defaultQAConfig(), petTimeNow)
	assert.Nil(t, q, "everything fresh → nothing eligible")
}

func TestPickQuestion_RecyclesAfterStalenessWindow(t *testing.T) {
	// Answers older than the 6-month staleness window become eligible
	// again. Set every non-consent answer to ~7 months ago and verify a
	// pick happens. (The exact ID is random; assert it's a non-consent
	// bank entry rather than the specific one.)
	pet := &daemon.PetState{State: "idle"}
	for _, q := range SeedQuestions {
		if q.ID == "consent" {
			continue
		}
		pet.AnsweredQuestions = append(pet.AnsweredQuestions, daemon.AnsweredQuestion{
			ID:        q.ID,
			Text:      q.Text,
			Answer:    "x",
			Kind:      q.Kind,
			Timestamp: petTimeNow.Add(-7 * 30 * 24 * time.Hour), // ~7 months old
		})
	}
	// First-time gate: any non-empty AnsweredQuestions list bypasses it.
	picked := PickQuestion(pet, defaultQAConfig(), petTimeNow)
	mustOK(t, assert.NotNil(t, picked, "stale answers should be recyclable"))
	assert.NotEqual(t, "consent", picked.ID)
}

func TestPickQuestion_FiltersFreeTextWhenOptedOut(t *testing.T) {
	// QAFreeTextOptedOut filters out free_text bank entries. After we mark
	// every choice question as freshly answered, only free_text entries
	// remain. With the opt-out set, the picker should have nothing left
	// and return nil. This is the precise "filter works" assertion.
	pet := stateWithDummyAnswer("consent") // bypass first-time gate
	pet.QAFreeTextOptedOut = true
	// Mark every choice question (non-consent) as freshly answered.
	for _, q := range SeedQuestions {
		if q.ID == "consent" || q.Kind != "choice" {
			continue
		}
		pet.AnsweredQuestions = append(pet.AnsweredQuestions, daemon.AnsweredQuestion{
			ID: q.ID, Text: q.Text, Answer: "x", Kind: q.Kind,
			Timestamp: petTimeNow.Add(-1 * time.Hour),
		})
	}
	got := PickQuestion(pet, defaultQAConfig(), petTimeNow)
	assert.Nil(t, got, "all choice answered + free_text opted out → nil")

	// Same scenario WITHOUT the opt-out: a free_text entry must be picked.
	pet.QAFreeTextOptedOut = false
	got2 := PickQuestion(pet, defaultQAConfig(), petTimeNow)
	mustOK(t, assert.NotNil(t, got2, "free_text entries should be eligible when opt-out is off"))
	assert.Equal(t, "free_text", got2.Kind)
}

func TestPickQuestion_FiltersFreeTextWhenConfigDisabled(t *testing.T) {
	// FreeTextDisabled on the config side mirrors the runtime opt-out and
	// must be enforced separately.
	pet := stateWithDummyAnswer("consent")
	for _, q := range SeedQuestions {
		if q.ID == "consent" || q.Kind != "choice" {
			continue
		}
		pet.AnsweredQuestions = append(pet.AnsweredQuestions, daemon.AnsweredQuestion{
			ID: q.ID, Text: q.Text, Answer: "x", Kind: q.Kind,
			Timestamp: petTimeNow.Add(-1 * time.Hour),
		})
	}
	qa := defaultQAConfig()
	qa.FreeTextDisabled = true
	got := PickQuestion(pet, qa, petTimeNow)
	assert.Nil(t, got, "config FreeTextDisabled should also filter")
}

func TestPickQuestion_SetsExpiresFromConfig(t *testing.T) {
	// ExpireHours must drive the Expires field. Verify with two different
	// config values and a zero-value (which should leave Expires zero).
	pet := freshState()

	qa := defaultQAConfig()
	qa.ExpireHours = 48
	q := PickQuestion(pet, qa, petTimeNow)
	mustOK(t, assert.NotNil(t, q))
	assert.True(t, q.Expires.Equal(petTimeNow.Add(48*time.Hour)),
		"expires should equal now + ExpireHours; got %v, expected %v",
		q.Expires, petTimeNow.Add(48*time.Hour))

	qa.ExpireHours = 12
	q2 := PickQuestion(pet, qa, petTimeNow)
	mustOK(t, assert.NotNil(t, q2))
	assert.True(t, q2.Expires.Equal(petTimeNow.Add(12*time.Hour)))

	// Zero ExpireHours: Expires stays zero so the renderer treats it as
	// "no expiry yet". This is the documented behaviour in pet_qa.go.
	qa.ExpireHours = 0
	q3 := PickQuestion(pet, qa, petTimeNow)
	mustOK(t, assert.NotNil(t, q3))
	assert.True(t, q3.Expires.IsZero(), "zero ExpireHours leaves Expires zero")
}

func TestPickQuestion_SuppressedDuringBadHeadspace(t *testing.T) {
	// pet_qa.go conservatively skips picks when the cat is in a "bad
	// headspace" state ("dead" / "starving"). Adventure mode is gated by
	// the coordinator, not here, so we don't test it from this layer.
	for _, badState := range []string{"dead", "starving"} {
		t.Run(badState, func(t *testing.T) {
			pet := freshState()
			pet.State = badState
			assert.Nil(t, PickQuestion(pet, defaultQAConfig(), petTimeNow))
		})
	}
}

func TestPickQuestion_NilPetIsSafe(t *testing.T) {
	// Defensive: a nil pet state should not panic. Real call sites always
	// pass non-nil, but tests and the CLI's adapter layer benefit from
	// this guarantee.
	assert.NotPanics(t, func() { PickQuestion(nil, defaultQAConfig(), petTimeNow) })
}

// ─── AnswerQuestion ────────────────────────────────────────────────────────

func TestAnswerQuestion_RejectsWhenNoPending(t *testing.T) {
	// AnswerQuestion must fail loudly if there's no pending question — the
	// CLI uses this to detect stale state.
	pet := freshState()
	err := AnswerQuestion(pet, "morning_or_night", "Night owl", defaultQAConfig(), petTimeNow)
	mustOK(t, assert.Error(t, err))
	assert.Contains(t, err.Error(), "no pending question")
}

func TestAnswerQuestion_RejectsMismatchedID(t *testing.T) {
	// Pending has ID "consent" but caller submits an answer for
	// "morning_or_night". This guards against the CLI sending stale state
	// after the daemon has rotated the question.
	pet := freshState()
	pet.PendingQuestion = &daemon.PendingQuestion{ID: "consent", Text: "?", Kind: "choice"}
	err := AnswerQuestion(pet, "morning_or_night", "Night owl", defaultQAConfig(), petTimeNow)
	mustOK(t, assert.Error(t, err))
	assert.Contains(t, err.Error(), "does not match")
}

func TestAnswerQuestion_ChoiceWithoutTemplateProducesNoTrait(t *testing.T) {
	// Choice answers whose text isn't in TraitFor (e.g. an answer that
	// doesn't match any choice, or a placeholder we don't want to learn
	// from) must silently skip distillation. The history entry should
	// still be recorded so ForgetAnswer can later remove it.
	pet := freshState()
	pet.PendingQuestion = &daemon.PendingQuestion{
		ID: "morning_or_night", Text: "?", Kind: "choice",
		Choices: []string{"Morning person", "Night owl", "Somewhere in between"},
	}
	err := AnswerQuestion(pet, "morning_or_night", "Banana", defaultQAConfig(), petTimeNow)
	mustOK(t, assert.NoError(t, err, "non-matching choice text is recorded but no trait distilled"))
	assert.Len(t, pet.Traits, 0, "unknown choice → no trait")
	mustOK(t, assert.Len(t, pet.AnsweredQuestions, 1))
	assert.Equal(t, "Banana", pet.AnsweredQuestions[0].Answer)
}

func TestAnswerQuestion_ConsentYesSetsNoOptOut(t *testing.T) {
	// "Yes, ask away" must leave both opt-out flags false (the default).
	pet := freshState()
	pet.PendingQuestion = &daemon.PendingQuestion{
		ID: "consent", Text: "?", Kind: "choice",
		Choices: []string{"Yes, ask away", "Multi-choice only (no free-text)", "No thanks"},
	}
	mustOK(t, assert.NoError(t, AnswerQuestion(pet, "consent", "Yes, ask away", defaultQAConfig(), petTimeNow)))
	assert.False(t, pet.QAOptedOut, "yes-consent leaves QAOptedOut false")
	assert.False(t, pet.QAFreeTextOptedOut, "yes-consent leaves free-text opt-out false")
	assert.Nil(t, pet.PendingQuestion, "pending cleared")
	mustOK(t, assert.Len(t, pet.AnsweredQuestions, 1, "answer recorded in history"))
	// Consent never distills a trait — TraitFor is empty in the bank.
	assert.Len(t, pet.Traits, 0)
}

func TestAnswerQuestion_ConsentMultiChoiceOnlySetsFreeTextOptOut(t *testing.T) {
	pet := freshState()
	pet.PendingQuestion = &daemon.PendingQuestion{
		ID: "consent", Text: "?", Kind: "choice",
		Choices: []string{"Yes, ask away", "Multi-choice only (no free-text)", "No thanks"},
	}
	mustOK(t, assert.NoError(t,
		AnswerQuestion(pet, "consent", "Multi-choice only (no free-text)", defaultQAConfig(), petTimeNow)))
	assert.False(t, pet.QAOptedOut)
	assert.True(t, pet.QAFreeTextOptedOut, "multi-choice-only sets free-text opt-out")
}

func TestAnswerQuestion_ConsentNoThanksSetsFullOptOut(t *testing.T) {
	pet := freshState()
	pet.PendingQuestion = &daemon.PendingQuestion{
		ID: "consent", Text: "?", Kind: "choice",
		Choices: []string{"Yes, ask away", "Multi-choice only (no free-text)", "No thanks"},
	}
	mustOK(t, assert.NoError(t, AnswerQuestion(pet, "consent", "No thanks", defaultQAConfig(), petTimeNow)))
	assert.True(t, pet.QAOptedOut, "no-thanks sets full opt-out")
	assert.False(t, pet.QAFreeTextOptedOut, "no-thanks doesn't set the free-text-only flag")

	// Verify the picker now refuses to pick anything.
	pet.PendingQuestion = nil
	assert.Nil(t, PickQuestion(pet, defaultQAConfig(), petTimeNow))
}

func TestAnswerQuestion_CapsAnsweredQuestionsAt50(t *testing.T) {
	// Seed 50 entries, then add a 51st via AnswerQuestion. The result
	// should still be 50 entries, with the oldest (entry #0) dropped and
	// the newest at the end.
	pet := freshState()
	for i := 0; i < 50; i++ {
		pet.AnsweredQuestions = append(pet.AnsweredQuestions, daemon.AnsweredQuestion{
			ID:        fmt.Sprintf("filler-%02d", i),
			Text:      "old",
			Answer:    fmt.Sprintf("old-%02d", i),
			Kind:      "choice",
			Timestamp: petTimeNow.Add(time.Duration(i-50) * time.Minute),
		})
	}
	mustOK(t, assert.Len(t, pet.AnsweredQuestions, 50))
	oldestBefore := pet.AnsweredQuestions[0].ID

	pet.PendingQuestion = &daemon.PendingQuestion{
		ID: "morning_or_night", Text: "?", Kind: "choice",
		Choices: []string{"Morning person", "Night owl", "Somewhere in between"},
	}
	mustOK(t, assert.NoError(t, AnswerQuestion(pet, "morning_or_night", "Night owl", defaultQAConfig(), petTimeNow)))

	assert.Len(t, pet.AnsweredQuestions, 50, "cap should hold at 50")
	assert.NotEqual(t, oldestBefore, pet.AnsweredQuestions[0].ID, "oldest dropped")
	assert.Equal(t, "morning_or_night", pet.AnsweredQuestions[49].ID, "newest at end")
}

func TestAnswerQuestion_AdvancesCooldown(t *testing.T) {
	// CooldownHours should bump QuestionCooldown to now + N hours. Also
	// verify the LastQuestionShown timestamp is set.
	pet := freshState()
	pet.PendingQuestion = &daemon.PendingQuestion{
		ID: "morning_or_night", Text: "?", Kind: "choice",
		Choices: []string{"Morning person", "Night owl", "Somewhere in between"},
	}
	qa := defaultQAConfig()
	qa.CooldownHours = 6
	mustOK(t, assert.NoError(t, AnswerQuestion(pet, "morning_or_night", "Night owl", qa, petTimeNow)))
	assert.True(t, pet.QuestionCooldown.Equal(petTimeNow.Add(6*time.Hour)),
		"QuestionCooldown should advance by CooldownHours; got %v", pet.QuestionCooldown)
	assert.True(t, pet.LastQuestionShown.Equal(petTimeNow), "LastQuestionShown set to now")
}

func TestAnswerQuestion_ZeroCooldownHoursLeavesCooldownAlone(t *testing.T) {
	// Documented behaviour: CooldownHours == 0 means "don't bump
	// cooldown" — used in tests and by callers that want to control the
	// cooldown manually.
	pet := freshState()
	pet.PendingQuestion = &daemon.PendingQuestion{
		ID: "morning_or_night", Text: "?", Kind: "choice",
		Choices: []string{"Morning person", "Night owl", "Somewhere in between"},
	}
	qa := defaultQAConfig()
	qa.CooldownHours = 0
	mustOK(t, assert.NoError(t, AnswerQuestion(pet, "morning_or_night", "Night owl", qa, petTimeNow)))
	assert.True(t, pet.QuestionCooldown.IsZero(), "zero cooldown leaves the timestamp unset")
}

// ─── DistillTrait ──────────────────────────────────────────────────────────

func TestDistillTrait_ChoiceUsesTemplate(t *testing.T) {
	// A choice whose answer is in TraitFor produces a trait with the
	// template text verbatim and confidence 1.0.
	pet := freshState()
	q := QuestionDef{
		ID: "morning_or_night", Kind: "choice",
		Choices: []string{"Morning person", "Night owl"},
		TraitFor: map[string]string{
			"Morning person": "user is a morning person",
			"Night owl":      "user is a night owl",
		},
	}
	DistillTrait(pet, q, "Night owl", petTimeNow)
	mustOK(t, assert.Len(t, pet.Traits, 1))
	assert.Equal(t, "user is a night owl", pet.Traits[0].Text)
	assert.Equal(t, "morning_or_night", pet.Traits[0].Source)
	assert.Equal(t, 1.0, pet.Traits[0].Confidence)
	assert.True(t, pet.Traits[0].AddedAt.Equal(petTimeNow))
}

func TestDistillTrait_FreeTextSubstitutesAndTruncates(t *testing.T) {
	// Free-text uses the "*" template and interpolates the user's input.
	// Long inputs are truncated to 120 runes (with a trailing "…" when
	// cut) before substitution so the prompt stays bounded.
	pet := freshState()
	q := QuestionDef{
		ID: "favourite_language", Kind: "free_text",
		TraitFor: map[string]string{"*": "user reaches for %s first"},
	}
	// Short input round-trips intact.
	DistillTrait(pet, q, "  Go  ", petTimeNow)
	mustOK(t, assert.Len(t, pet.Traits, 1))
	assert.Equal(t, "user reaches for Go first", pet.Traits[0].Text)
	assert.InDelta(t, 0.7, pet.Traits[0].Confidence, 0.0001)

	// Long input is truncated. The truncate helper keeps max-1 runes and
	// appends "…", so a 200-rune input becomes 120 runes total in the
	// %s slot (119 + "…"). Anywhere "long_a..." appears the test should
	// see at most 120 runes plus the template prefix.
	pet2 := freshState()
	longAnswer := strings.Repeat("a", 200)
	DistillTrait(pet2, q, longAnswer, petTimeNow)
	mustOK(t, assert.Len(t, pet2.Traits, 1))
	assert.True(t, strings.Contains(pet2.Traits[0].Text, "…"),
		"long free-text should be ellipsised; got %q", pet2.Traits[0].Text)
	// The %s slot should be ≤120 runes — we can check the trait length is
	// bounded by template + 120.
	const tmpl = "user reaches for  first" // template with empty %s
	maxLen := utf8.RuneCountInString(tmpl) + 120
	assert.LessOrEqual(t, utf8.RuneCountInString(pet2.Traits[0].Text), maxLen,
		"trait text length is bounded by template + max free-text length")
}

func TestDistillTrait_MissingTemplateSkipsSilently(t *testing.T) {
	// Three "no trait" paths to verify:
	//   1. TraitFor empty (consent-like)
	//   2. choice answer not in TraitFor
	//   3. free_text TraitFor has no "*" entry
	pet := freshState()
	DistillTrait(pet, QuestionDef{ID: "x", Kind: "choice", TraitFor: map[string]string{}}, "anything", petTimeNow)
	DistillTrait(pet, QuestionDef{ID: "x", Kind: "choice",
		TraitFor: map[string]string{"yes": "user agreed"}}, "no-such-choice", petTimeNow)
	DistillTrait(pet, QuestionDef{ID: "x", Kind: "free_text",
		TraitFor: map[string]string{"different_key": "wrong"}}, "answer", petTimeNow)
	assert.Len(t, pet.Traits, 0, "missing/unmatched templates should not produce traits")
}

func TestDistillTrait_ReanswerReplacesInPlace(t *testing.T) {
	// Asking the same question twice should UPDATE the existing trait
	// (matched by Source), not append a duplicate. This is so users who
	// re-answer (via popup) see their previous answer overwritten.
	pet := freshState()
	q := QuestionDef{
		ID: "morning_or_night", Kind: "choice",
		Choices:  []string{"Morning person", "Night owl"},
		TraitFor: map[string]string{"Morning person": "user is a morning person", "Night owl": "user is a night owl"},
	}
	DistillTrait(pet, q, "Morning person", petTimeNow)
	DistillTrait(pet, q, "Night owl", petTimeNow.Add(1*time.Hour))
	mustOK(t, assert.Len(t, pet.Traits, 1, "re-answer should replace, not duplicate"))
	assert.Equal(t, "user is a night owl", pet.Traits[0].Text)
}

func TestDistillTrait_CapsAt20WithWeakestEvicted(t *testing.T) {
	// Append 20 distinct choice-traits (confidence 1.0) so we're at the
	// cap, then add a weaker (0.7) free-text trait. Because the new
	// entry is weaker than every existing one, eviction targets it.
	pet := freshState()
	for i := 0; i < 20; i++ {
		pet.Traits = append(pet.Traits, daemon.PersonalityTrait{
			Text:       fmt.Sprintf("trait %d", i),
			Source:     fmt.Sprintf("src-%02d", i),
			Confidence: 1.0,
			AddedAt:    petTimeNow.Add(time.Duration(i) * time.Minute),
		})
	}
	// Add a 21st trait via DistillTrait with confidence 0.7 (free-text).
	q := QuestionDef{ID: "weak", Kind: "free_text", TraitFor: map[string]string{"*": "weak fact: %s"}}
	DistillTrait(pet, q, "x", petTimeNow.Add(20*time.Minute))
	assert.Len(t, pet.Traits, 20, "cap holds at 20")
	for _, tr := range pet.Traits {
		assert.NotEqual(t, "weak", tr.Source, "the weakest (just-added 0.7) should be evicted")
	}

	// Tiebreaker check: two 0.7s at different ages, then add a third 0.7
	// via DistillTrait. The OLDEST of the lowest-confidence tier should
	// be evicted ("lowest confidence wins; oldest as tiebreaker").
	pet2 := freshState()
	for i := 0; i < 18; i++ {
		pet2.Traits = append(pet2.Traits, daemon.PersonalityTrait{
			Text: fmt.Sprintf("strong %d", i), Source: fmt.Sprintf("s-%02d", i),
			Confidence: 1.0, AddedAt: petTimeNow.Add(time.Duration(i) * time.Minute),
		})
	}
	pet2.Traits = append(pet2.Traits,
		daemon.PersonalityTrait{Text: "older weak", Source: "older-weak", Confidence: 0.7,
			AddedAt: petTimeNow.Add(-2 * time.Hour)},
		daemon.PersonalityTrait{Text: "newer weak", Source: "newer-weak", Confidence: 0.7,
			AddedAt: petTimeNow.Add(-1 * time.Hour)},
	)
	mustOK(t, assert.Len(t, pet2.Traits, 20))
	q2 := QuestionDef{ID: "another-weak", Kind: "free_text", TraitFor: map[string]string{"*": "weak: %s"}}
	DistillTrait(pet2, q2, "y", petTimeNow)
	assert.Len(t, pet2.Traits, 20)
	sources := make(map[string]bool, len(pet2.Traits))
	for _, tr := range pet2.Traits {
		sources[tr.Source] = true
	}
	assert.False(t, sources["older-weak"], "oldest-of-the-weakest should be evicted; got sources=%v", sources)
	assert.True(t, sources["newer-weak"], "newer weak entry should survive")
	assert.True(t, sources["another-weak"], "newly-added entry should survive")
}

// ─── ForgetAnswer ──────────────────────────────────────────────────────────

func TestForgetAnswer_RemovesAnswerAndTrait(t *testing.T) {
	pet := freshState()
	pet.AnsweredQuestions = []daemon.AnsweredQuestion{
		{ID: "morning_or_night", Text: "q", Answer: "Night owl", Kind: "choice", Timestamp: petTimeNow},
		{ID: "tabs_or_spaces", Text: "q2", Answer: "Tabs", Kind: "choice", Timestamp: petTimeNow},
	}
	pet.Traits = []daemon.PersonalityTrait{
		{Text: "user is a night owl", Source: "morning_or_night", Confidence: 1.0, AddedAt: petTimeNow},
		{Text: "user prefers tabs", Source: "tabs_or_spaces", Confidence: 1.0, AddedAt: petTimeNow},
	}
	mustOK(t, assert.NoError(t, ForgetAnswer(pet, "morning_or_night")))
	assert.Len(t, pet.AnsweredQuestions, 1)
	assert.Equal(t, "tabs_or_spaces", pet.AnsweredQuestions[0].ID)
	assert.Len(t, pet.Traits, 1)
	assert.Equal(t, "tabs_or_spaces", pet.Traits[0].Source)
}

func TestForgetAnswer_UnknownIDReturnsError(t *testing.T) {
	pet := freshState()
	pet.AnsweredQuestions = []daemon.AnsweredQuestion{
		{ID: "morning_or_night", Text: "q", Answer: "Night owl", Kind: "choice", Timestamp: petTimeNow},
	}
	err := ForgetAnswer(pet, "nope")
	mustOK(t, assert.Error(t, err))
	assert.Contains(t, err.Error(), "nope")
}

func TestForgetAnswer_EmptyIDReturnsError(t *testing.T) {
	pet := freshState()
	err := ForgetAnswer(pet, "")
	mustOK(t, assert.Error(t, err))
}

// ─── RecentAnswers ─────────────────────────────────────────────────────────

func TestRecentAnswers_ReturnsNewestFirst(t *testing.T) {
	pet := freshState()
	for i := 0; i < 5; i++ {
		pet.AnsweredQuestions = append(pet.AnsweredQuestions, daemon.AnsweredQuestion{
			ID:        fmt.Sprintf("q%d", i),
			Text:      "?",
			Answer:    fmt.Sprintf("a%d", i),
			Kind:      "choice",
			Timestamp: petTimeNow.Add(time.Duration(i) * time.Minute),
		})
	}
	got := RecentAnswers(pet, 3)
	mustOK(t, assert.Len(t, got, 3))
	assert.Equal(t, "q4", got[0].ID, "newest first")
	assert.Equal(t, "q3", got[1].ID)
	assert.Equal(t, "q2", got[2].ID)
}

func TestRecentAnswers_DefaultNIs3(t *testing.T) {
	pet := freshState()
	for i := 0; i < 5; i++ {
		pet.AnsweredQuestions = append(pet.AnsweredQuestions, daemon.AnsweredQuestion{
			ID: fmt.Sprintf("q%d", i), Timestamp: petTimeNow.Add(time.Duration(i) * time.Minute),
		})
	}
	got := RecentAnswers(pet, 0)
	assert.Len(t, got, 3, "n<=0 falls back to default 3")
}

// ─── JSON round-trip backward compatibility ────────────────────────────────

// oldPetJSONWire is what a pre-Phase-1 pet.json contains for the wire-format
// daemon.PetState. None of the Q&A keys appear; existing keys are
// zero-valued where reasonable so we exercise unmarshal more than just
// "all zeros". The Hunger/Happiness values are not magical — they're just
// realistic.
const oldPetJSONWire = `{
  "hunger": 80,
  "happiness": 70,
  "last_fed": "2026-05-20T12:00:00Z",
  "last_pet": "2026-05-20T11:00:00Z",
  "total_pets": 3,
  "total_feedings": 5,
  "total_poops_cleaned": 2,
  "total_yarn_plays": 0,
  "state": "idle",
  "last_thought": "I love this human.",
  "pos_x": 10,
  "yarn_pos_x": 0,
  "poop_positions": []
}`

func TestPetStateJSONCompat_OldFileUnmarshalsWithoutQAFields(t *testing.T) {
	var ps daemon.PetState
	mustOK(t, assert.NoError(t, json.Unmarshal([]byte(oldPetJSONWire), &ps)))
	// Existing fields preserved.
	assert.Equal(t, 80, ps.Hunger)
	assert.Equal(t, "idle", ps.State)
	// Q&A fields all zero-valued.
	assert.Nil(t, ps.PendingQuestion, "pointer is nil for absent field")
	assert.Nil(t, ps.AnsweredQuestions, "slice is nil for absent field")
	assert.Nil(t, ps.Traits, "slice is nil for absent field")
	assert.True(t, ps.QuestionCooldown.IsZero(), "time is zero for absent field")
	assert.True(t, ps.LastQuestionShown.IsZero())
	assert.False(t, ps.QAOptedOut)
	assert.False(t, ps.QAFreeTextOptedOut)
}

func TestPetStateJSONCompat_ZeroValuedReMarshalOmitsQAKeys(t *testing.T) {
	// Marshal a freshly-loaded old pet.json. The omitempty tags should
	// suppress the pointer / slice / bool Q&A fields. NOTE: Go's encoding/json
	// does NOT omit zero time.Time even with omitempty, so question_cooldown
	// and last_question_shown WILL appear as "0001-01-01T00:00:00Z". That's
	// not a bug introduced by this change — it's a known Go quirk — so the
	// assertion is "the truly-optional Q&A keys are absent" rather than
	// "every Q&A key is absent". A future Go release with `omitzero` would
	// let us tighten this.
	var ps daemon.PetState
	mustOK(t, assert.NoError(t, json.Unmarshal([]byte(oldPetJSONWire), &ps)))
	out, err := json.Marshal(&ps)
	mustOK(t, assert.NoError(t, err))
	s := string(out)
	for _, key := range []string{
		`"pending_question"`,
		`"answered_questions"`,
		`"traits"`,
		`"qa_opted_out"`,
		`"qa_free_text_opted_out"`,
	} {
		assert.NotContains(t, s, key,
			"%s must be omitted from marshalled zero-valued PetState", key)
	}
}

func TestPetStateJSONCompat_RoundTripWithQAFields(t *testing.T) {
	// Construct a PetState with Q&A fields populated, marshal, unmarshal,
	// and assert equality. This catches accidental field rename / tag
	// changes that would break pet.json round-trips across restarts.
	in := daemon.PetState{
		Hunger:    50,
		Happiness: 50,
		State:     "idle",
		PendingQuestion: &daemon.PendingQuestion{
			ID: "morning_or_night", Text: "?", Kind: "choice",
			Choices: []string{"Morning person", "Night owl"},
			Expires: petTimeNow.Add(48 * time.Hour),
			Source:  "bank",
		},
		AnsweredQuestions: []daemon.AnsweredQuestion{
			{ID: "tabs_or_spaces", Text: "?", Answer: "Tabs", Kind: "choice", Timestamp: petTimeNow},
		},
		Traits: []daemon.PersonalityTrait{
			{Text: "user prefers tabs", Source: "tabs_or_spaces", Confidence: 1.0, AddedAt: petTimeNow},
		},
		QuestionCooldown:   petTimeNow.Add(24 * time.Hour),
		LastQuestionShown:  petTimeNow,
		QAOptedOut:         false,
		QAFreeTextOptedOut: true,
	}
	data, err := json.Marshal(&in)
	mustOK(t, assert.NoError(t, err))

	var out daemon.PetState
	mustOK(t, assert.NoError(t, json.Unmarshal(data, &out)))

	// time.Time round-trip via JSON loses monotonic clock info; compare
	// via Equal not reflect.DeepEqual on whole struct. We deep-compare
	// everything else and then check each time field explicitly.
	mustOK(t, assert.NotNil(t, out.PendingQuestion))
	assert.Equal(t, in.PendingQuestion.ID, out.PendingQuestion.ID)
	assert.Equal(t, in.PendingQuestion.Choices, out.PendingQuestion.Choices)
	assert.True(t, in.PendingQuestion.Expires.Equal(out.PendingQuestion.Expires))

	mustOK(t, assert.Len(t, out.AnsweredQuestions, 1))
	assert.Equal(t, in.AnsweredQuestions[0].ID, out.AnsweredQuestions[0].ID)
	assert.True(t, in.AnsweredQuestions[0].Timestamp.Equal(out.AnsweredQuestions[0].Timestamp))

	mustOK(t, assert.Len(t, out.Traits, 1))
	assert.Equal(t, in.Traits[0].Text, out.Traits[0].Text)
	assert.True(t, in.Traits[0].AddedAt.Equal(out.Traits[0].AddedAt))

	assert.True(t, in.QuestionCooldown.Equal(out.QuestionCooldown))
	assert.True(t, in.LastQuestionShown.Equal(out.LastQuestionShown))
	assert.Equal(t, in.QAFreeTextOptedOut, out.QAFreeTextOptedOut)
	assert.Equal(t, in.QAOptedOut, out.QAOptedOut)
}

// ── Local petState round-trip ─────────────────────────────────────────────
//
// The daemon serialises the LOCAL petState (cmd/tabby/internal/daemon)
// to pet.json. The local type carries the same JSON tags for the Q&A
// fields as the wire daemon.PetState, so an OLD pet.json must deserialise
// into the local type with all Q&A fields zero-valued, and round-trips
// of populated Q&A data must preserve everything.

// oldPetJSONLocal is what a pre-Phase-1 pet.json containing the FULL
// local petState looks like. It has more fields than the wire variant —
// adventure state, mouse state, etc. — but still no Q&A keys.
const oldPetJSONLocal = `{
  "hunger": 80,
  "happiness": 70,
  "last_fed": "2026-05-20T12:00:00Z",
  "last_pet": "2026-05-20T11:00:00Z",
  "total_pets": 3,
  "total_feedings": 5,
  "total_poops_cleaned": 2,
  "total_yarn_plays": 0,
  "state": "idle",
  "last_thought": "I love this human.",
  "poop_positions": []
}`

func TestPetStateJSONCompat_LocalUnmarshalsWithoutQAFields(t *testing.T) {
	var ps petState
	mustOK(t, assert.NoError(t, json.Unmarshal([]byte(oldPetJSONLocal), &ps)))
	assert.Equal(t, 80, ps.Hunger)
	assert.Equal(t, "idle", ps.State)
	// Q&A fields zero-valued — same assertions as the wire variant.
	assert.Nil(t, ps.PendingQuestion)
	assert.Nil(t, ps.AnsweredQuestions)
	assert.Nil(t, ps.Traits)
	assert.True(t, ps.QuestionCooldown.IsZero())
	assert.True(t, ps.LastQuestionShown.IsZero())
	assert.False(t, ps.QAOptedOut)
	assert.False(t, ps.QAFreeTextOptedOut)
}

func TestPetStateJSONCompat_LocalMarshalOmitsAbsentQAKeys(t *testing.T) {
	// Same omitempty assertion as the wire variant, against the local
	// petState. The local type has more "always present" fields (mouse,
	// adventure, etc.) so we only assert the Q&A-specific ones are absent.
	var ps petState
	mustOK(t, assert.NoError(t, json.Unmarshal([]byte(oldPetJSONLocal), &ps)))
	out, err := json.Marshal(&ps)
	mustOK(t, assert.NoError(t, err))
	s := string(out)
	for _, key := range []string{
		`"pending_question"`,
		`"answered_questions"`,
		`"traits"`,
		`"qa_opted_out"`,
		`"qa_free_text_opted_out"`,
	} {
		assert.NotContains(t, s, key, "%s must be omitted from marshalled zero-valued local petState", key)
	}
}

// ─── LLM context — petPersonalitySection / buildPetContext ─────────────────

// localPetWithQA returns a local petState with Q&A populated for prompt-
// builder tests. We use the LOCAL type because buildPetContext takes
// *petState, and the llm.go reflection code reads the Q&A fields off
// either variant transparently.
func localPetWithQA(traits []daemon.PersonalityTrait, answers []daemon.AnsweredQuestion) *petState {
	return &petState{
		State:             "idle",
		Hunger:            50,
		Happiness:         50,
		MousePos:          pos2D{X: -1, Y: 0}, // -1 means "no mouse"
		Traits:            traits,
		AnsweredQuestions: answers,
	}
}

func TestBuildPetContext_PersonalityFolding_AbsentWhenNoQA(t *testing.T) {
	pet := localPetWithQA(nil, nil)
	got := buildPetContext(pet)
	// The personality header should be absent entirely so we don't emit
	// an empty section to the LLM. The Hunger/Happiness lines remain.
	assert.NotContains(t, got, "What you know about your human:")
	assert.NotContains(t, got, "Recent things they told you:")
}

func TestBuildPetContext_PersonalityFolding_PresentWithBoth(t *testing.T) {
	// 3 traits with different confidences + 2 answers. Verify:
	//   - the section header appears
	//   - traits are ordered by confidence desc
	//   - both recent answers appear (newest first, by descending timestamp)
	traits := []daemon.PersonalityTrait{
		{Text: "low confidence trait", Source: "src-low", Confidence: 0.5, AddedAt: petTimeNow},
		{Text: "high confidence trait", Source: "src-high", Confidence: 1.0, AddedAt: petTimeNow},
		{Text: "mid confidence trait", Source: "src-mid", Confidence: 0.7, AddedAt: petTimeNow},
	}
	answers := []daemon.AnsweredQuestion{
		{ID: "older", Text: "older question", Answer: "older answer", Kind: "choice", Timestamp: petTimeNow.Add(-1 * time.Hour)},
		{ID: "newer", Text: "newer question", Answer: "newer answer", Kind: "choice", Timestamp: petTimeNow},
	}
	got := buildPetContext(localPetWithQA(traits, answers))
	assert.Contains(t, got, "What you know about your human:")
	assert.Contains(t, got, "Recent things they told you:")

	// Trait ordering: high > mid > low. We compare positions of each
	// trait's text in the output and assert the high one appears before
	// the mid one which appears before the low one.
	highIdx := strings.Index(got, "high confidence trait")
	midIdx := strings.Index(got, "mid confidence trait")
	lowIdx := strings.Index(got, "low confidence trait")
	mustOK(t, assert.True(t, highIdx >= 0 && midIdx >= 0 && lowIdx >= 0,
		"all traits should appear in the output"))
	assert.Less(t, highIdx, midIdx, "high-confidence trait first")
	assert.Less(t, midIdx, lowIdx, "mid before low")

	// Both recent answers appear; newest first.
	newerIdx := strings.Index(got, "newer answer")
	olderIdx := strings.Index(got, "older answer")
	mustOK(t, assert.True(t, newerIdx >= 0 && olderIdx >= 0, "both answers should appear"))
	assert.Less(t, newerIdx, olderIdx, "newer answer listed first")
}

func TestBuildPetContext_PersonalityFolding_TruncatesLongFreeText(t *testing.T) {
	// Free-text answer over 120 chars should appear truncated with "…"
	// in the prompt. The llm.go truncateAnswer uses runes[:n] + "…" so a
	// 121-rune input becomes 120 runes + "…" = 121 visible characters.
	longAnswer := strings.Repeat("x", 200)
	answers := []daemon.AnsweredQuestion{
		{ID: "favourite_language", Text: "What language?", Answer: longAnswer, Kind: "free_text", Timestamp: petTimeNow},
	}
	got := buildPetContext(localPetWithQA(nil, answers))
	// The answer line is rendered as: - "What language?" → "<truncated>"
	// We look for the ellipsis in the answer slot.
	assert.Contains(t, got, "…", "long free-text answer should be truncated with ellipsis")
	// And the raw 200-x string should NOT appear in full.
	assert.NotContains(t, got, longAnswer, "full long answer must not appear")
}

func TestBuildPetContext_PersonalityFolding_OmitsConsentAnswer(t *testing.T) {
	// The consent question is privacy-sensitive — it must never appear
	// in the LLM-bound "recent things they told you" list. Even when
	// other recent answers are present, the consent line is filtered.
	answers := []daemon.AnsweredQuestion{
		{ID: "consent", Text: "Are you okay with that?", Answer: "Yes, ask away", Kind: "choice",
			Timestamp: petTimeNow},
		{ID: "morning_or_night", Text: "Morning or night?", Answer: "Night owl", Kind: "choice",
			Timestamp: petTimeNow.Add(-1 * time.Hour)},
	}
	got := buildPetContext(localPetWithQA(nil, answers))
	assert.NotContains(t, got, "Are you okay with that?", "consent question text must not appear in prompt")
	assert.NotContains(t, got, "Yes, ask away", "consent answer must not appear in prompt")
	assert.Contains(t, got, "Night owl", "non-consent answer still appears")
}

// ─── CLI argument parsing ──────────────────────────────────────────────────
//
// The CLI's socket path (request()) requires a live tmux + daemon and is
// disproportionately hard to mock from here — pet.Run's transport layer
// dials a unix socket and reads a JSON message. We instead test the
// argument-parsing surface that can fail without the network: usage
// dispatch on unknown subcommand, empty args, and the `tabby pet forget`
// no-arg variant. The pet package's printPending is unexported so we
// don't exercise output formatting here.

func TestPetCLI_NoArgsPrintsUsage(t *testing.T) {
	rc := pet.Run(nil)
	assert.Equal(t, 0, rc, "no args is allowed and prints usage")
}

func TestPetCLI_HelpReturnsZero(t *testing.T) {
	for _, flag := range []string{"-h", "--help", "help"} {
		t.Run(flag, func(t *testing.T) {
			rc := pet.Run([]string{flag})
			assert.Equal(t, 0, rc)
		})
	}
}

func TestPetCLI_UnknownSubcommandReturns2(t *testing.T) {
	rc := pet.Run([]string{"barf"})
	assert.Equal(t, 2, rc, "unknown subcommand should return usage exit code")
}

func TestPetCLI_ForgetWithoutIDReturns2(t *testing.T) {
	rc := pet.Run([]string{"forget"})
	assert.Equal(t, 2, rc, "forget requires exactly one id argument")
}

func TestPetCLI_TraitsExtraArgsReturns2(t *testing.T) {
	rc := pet.Run([]string{"traits", "unexpected"})
	assert.Equal(t, 2, rc, "traits takes no positional args")
}

// ─── seed bank sanity ──────────────────────────────────────────────────────
//
// Cross-checks against pet_questions.go that the bank still satisfies the
// invariants pet_qa.go relies on. If a bank refactor accidentally breaks
// these, the failure here is the cheapest signal.

func TestSeedBank_ConsentIsFirstAndHasExpectedChoices(t *testing.T) {
	mustOK(t, assert.NotEmpty(t, SeedQuestions, "bank must have at least the consent question"))
	first := SeedQuestions[0]
	assert.Equal(t, "consent", first.ID, "consent must be index 0")
	assert.Equal(t, "choice", first.Kind)
	// The three consent answers drive pet_qa.go's applyConsentAnswer
	// switch; if their literal text changes here, that switch needs the
	// same change. Asserting them here catches drift.
	assert.Contains(t, first.Choices, "Yes, ask away")
	assert.Contains(t, first.Choices, "Multi-choice only (no free-text)")
	assert.Contains(t, first.Choices, "No thanks")
}

func TestSeedBank_AllFreeTextHaveStarTemplate(t *testing.T) {
	for _, q := range SeedQuestions {
		if q.Kind != "free_text" {
			continue
		}
		_, ok := q.TraitFor["*"]
		assert.True(t, ok, "free_text question %q must have a \"*\" TraitFor template", q.ID)
	}
}

// ─── Phase 3 LLM distillation ─────────────────────────────────────────────

// llmQAConfigOn returns the default Q&A config with the LLM gate enabled.
// Distillation tests reach for this so they don't have to remember which
// flag controls the Phase-3 pass.
func llmQAConfigOn() config.PetWidgetQA {
	qa := defaultQAConfig()
	qa.LLMQuestions = true
	return qa
}

// stubGenerator returns a llmGenerator that always returns the given
// response/error pair. Lets us drive DistillTraitsLLM deterministically
// without touching the real gollm client.
func stubGenerator(resp string, err error) func(ctx gocontext.Context, prompt string) (string, error) {
	return func(_ gocontext.Context, _ string) (string, error) {
		return resp, err
	}
}

// petWithFreeTextAnswers returns a daemon.PetState with n free-text
// answers, none distilled. Used to drive the trigger logic.
func petWithFreeTextAnswers(n int, baseTime time.Time) *daemon.PetState {
	p := &daemon.PetState{State: "idle"}
	for i := 0; i < n; i++ {
		p.AnsweredQuestions = append(p.AnsweredQuestions, daemon.AnsweredQuestion{
			ID:        fmt.Sprintf("free-%02d", i),
			Text:      fmt.Sprintf("free-text question %d?", i),
			Answer:    fmt.Sprintf("answer-%02d", i),
			Kind:      "free_text",
			Timestamp: baseTime.Add(time.Duration(i) * time.Minute),
		})
		// Each free-text answer already produced a raw trait via the
		// Phase-1 path; tag the source so we can verify consolidation
		// replaces it.
		p.Traits = append(p.Traits, daemon.PersonalityTrait{
			Text:       fmt.Sprintf("user reaches for thing-%02d first", i),
			Source:     fmt.Sprintf("free-%02d", i),
			Confidence: 0.7,
			AddedAt:    baseTime.Add(time.Duration(i) * time.Minute),
		})
	}
	return p
}

func TestShouldRunLLMDistillation_GateOff(t *testing.T) {
	// LLMQuestions=false must short-circuit before any work is done.
	p := petWithFreeTextAnswers(20, petTimeNow)
	should, batch := ShouldRunLLMDistillation(p, defaultQAConfig(), petTimeNow)
	assert.False(t, should, "gate off should never fire")
	assert.Nil(t, batch)
}

func TestShouldRunLLMDistillation_OptedOutBlocks(t *testing.T) {
	// Even with the gate on and 20 undistilled answers, QAOptedOut is
	// the privacy guardrail and must prevent any LLM call.
	p := petWithFreeTextAnswers(20, petTimeNow)
	p.QAOptedOut = true
	should, _ := ShouldRunLLMDistillation(p, llmQAConfigOn(), petTimeNow)
	assert.False(t, should, "QAOptedOut must always block")
}

func TestShouldRunLLMDistillation_CountThreshold(t *testing.T) {
	// 9 free-text answers, fresh cadence (last==zero), with low batch
	// of 3 the cadence path WILL fire. So to test the count path we
	// need to manually advance lastLLMDistillation past 'now' so the
	// cadence is suppressed and ONLY the count threshold decides.
	saved := lastLLMDistillation
	defer func() { lastLLMDistillation = saved }()
	lastLLMDistillation = petTimeNow.Add(-1 * time.Hour) // <6h, cadence suppressed

	p := petWithFreeTextAnswers(9, petTimeNow)
	should, _ := ShouldRunLLMDistillation(p, llmQAConfigOn(), petTimeNow)
	assert.False(t, should, "below count threshold + cadence not elapsed → no fire")

	p2 := petWithFreeTextAnswers(10, petTimeNow)
	should2, batch2 := ShouldRunLLMDistillation(p2, llmQAConfigOn(), petTimeNow)
	assert.True(t, should2, "at count threshold should fire")
	assert.Len(t, batch2, 10)
}

func TestShouldRunLLMDistillation_CadencePath(t *testing.T) {
	// With 3+ undistilled answers and cadence elapsed (or never run),
	// the time-based path triggers.
	saved := lastLLMDistillation
	defer func() { lastLLMDistillation = saved }()
	lastLLMDistillation = time.Time{} // never run

	p := petWithFreeTextAnswers(3, petTimeNow)
	should, _ := ShouldRunLLMDistillation(p, llmQAConfigOn(), petTimeNow)
	assert.True(t, should, "fresh install with 3+ free-text answers fires on cadence")
}

func TestShouldRunLLMDistillation_OnlyFreeTextCounts(t *testing.T) {
	// 20 choice answers must NOT trigger the pass — only free-text
	// answers feed the distillation queue.
	p := &daemon.PetState{State: "idle"}
	for i := 0; i < 20; i++ {
		p.AnsweredQuestions = append(p.AnsweredQuestions, daemon.AnsweredQuestion{
			ID:        fmt.Sprintf("choice-%02d", i),
			Text:      "?",
			Answer:    "x",
			Kind:      "choice",
			Timestamp: petTimeNow.Add(time.Duration(i) * time.Minute),
		})
	}
	should, _ := ShouldRunLLMDistillation(p, llmQAConfigOn(), petTimeNow)
	assert.False(t, should, "choice answers should not feed the LLM queue")
}

func TestDistillTraitsLLM_AppliesProposalsAndReplacesRawTraits(t *testing.T) {
	// Three undistilled free-text answers; LLM returns one consolidated
	// proposal that covers all three. Expect: the three raw "reaches
	// for thing-XX first" traits are removed, one new "user is a
	// polyglot" trait is appended, and all three answers are marked
	// Distilled.
	p := petWithFreeTextAnswers(3, petTimeNow)
	resp := `[{"trait_text":"user is a polyglot developer","confidence":0.9,"source_question_ids":["free-00","free-01","free-02"]}]`
	applied := DistillTraitsLLM(gocontext.Background(), p, llmQAConfigOn(),
		stubGenerator(resp, nil), petTimeNow.Add(time.Hour))
	assert.Equal(t, 1, applied, "one proposal applied")

	// Raw source traits should be gone.
	for _, tr := range p.Traits {
		assert.False(t, strings.HasPrefix(tr.Source, "free-"),
			"raw free-text traits should be consolidated away; got source=%s", tr.Source)
	}
	// New consolidated trait present.
	var found *daemon.PersonalityTrait
	for i := range p.Traits {
		if strings.Contains(p.Traits[i].Text, "polyglot") {
			found = &p.Traits[i]
			break
		}
	}
	if assert.NotNil(t, found, "consolidated trait must be appended") {
		assert.Equal(t, "llm:free-00", found.Source)
		assert.InDelta(t, 0.9, found.Confidence, 0.001)
	}
	// All three source answers marked Distilled.
	for _, a := range p.AnsweredQuestions {
		assert.True(t, a.Distilled, "answer %s should be marked Distilled", a.ID)
	}
}

func TestDistillTraitsLLM_LLMErrorPreservesExistingTraits(t *testing.T) {
	// If gen returns an error, we must NOT lose existing traits and
	// must NOT mark any answer as Distilled (the next pass should
	// retry).
	p := petWithFreeTextAnswers(3, petTimeNow)
	traitsBefore := append([]daemon.PersonalityTrait(nil), p.Traits...)
	applied := DistillTraitsLLM(gocontext.Background(), p, llmQAConfigOn(),
		stubGenerator("", errors.New("network down")), petTimeNow)
	assert.Equal(t, 0, applied)
	assert.Equal(t, len(traitsBefore), len(p.Traits), "no traits lost on LLM error")
	for _, a := range p.AnsweredQuestions {
		assert.False(t, a.Distilled, "should not mark Distilled on error")
	}
}

func TestDistillTraitsLLM_GarbageJSONMarksDistilledToBreakLoop(t *testing.T) {
	// Defensive: when the LLM returns non-JSON garbage, we mark the
	// batch Distilled so we don't burn budget retrying it forever.
	// Existing traits remain untouched.
	p := petWithFreeTextAnswers(3, petTimeNow)
	traitsBefore := append([]daemon.PersonalityTrait(nil), p.Traits...)
	applied := DistillTraitsLLM(gocontext.Background(), p, llmQAConfigOn(),
		stubGenerator("Sure! I'll think about it.", nil), petTimeNow)
	assert.Equal(t, 0, applied, "no proposals parsed from garbage")
	assert.Equal(t, len(traitsBefore), len(p.Traits), "no traits altered by garbage response")
	for _, a := range p.AnsweredQuestions {
		assert.True(t, a.Distilled, "answers marked to break retry loop")
	}
}

func TestDistillTraitsLLM_TolerantOfCodeFences(t *testing.T) {
	// Real LLMs sometimes wrap JSON in code fences. parseLLMProposals
	// strips them; this end-to-end test confirms the pipeline tolerates
	// the same.
	p := petWithFreeTextAnswers(2, petTimeNow)
	resp := "```json\n" + `[{"trait_text":"user likes terminals","confidence":0.85,"source_question_ids":["free-00"]}]` + "\n```"
	applied := DistillTraitsLLM(gocontext.Background(), p, llmQAConfigOn(),
		stubGenerator(resp, nil), petTimeNow)
	assert.Equal(t, 1, applied)
}

// TestMergeDistilledTraits_PreservesConcurrent verifies the regression
// the integration reviewer flagged: when a trait is added to the live
// state during the ~30 s LLM distillation call (e.g. by a concurrent
// AnswerQuestion → DistillTrait path), the post-LLM apply callback
// preserves it instead of clobbering with the stale snapshot.
//
// Before the fix, mergeDistilledTraits did not exist and the apply
// callback did `live.Traits = append(nil, snap.Traits...)`. That
// wholesale replacement dropped any trait whose Source the LLM didn't
// know about.
func TestMergeDistilledTraits_PreservesConcurrent(t *testing.T) {
	now := petTimeNow
	// snap = the LLM's view at distillation time: consolidated trait
	// from sources A and B.
	snap := []daemon.PersonalityTrait{
		{Text: "user codes at night", Source: "morning_or_night", Confidence: 0.9, AddedAt: now},
		{Text: "user prefers Go over Python", Source: "fav_language", Confidence: 0.8, AddedAt: now},
	}
	// live = what's actually in state when the apply callback runs.
	// Includes the original raw trait for Source "morning_or_night"
	// (about to be replaced by snap's consolidated version) AND a
	// brand-new trait for Source "fav_food" that was added DURING the
	// LLM call window.
	live := []daemon.PersonalityTrait{
		{Text: "user mentioned: late nights debugging", Source: "morning_or_night", Confidence: 0.7, AddedAt: now.Add(-1 * time.Hour)},
		{Text: "cat's favourite food is tuna", Source: "fav_food", Confidence: 1.0, AddedAt: now},
	}
	merged := mergeDistilledTraits(snap, live)
	// Expect: snap's two entries (replacing the raw "morning_or_night"
	// trait) PLUS the live-only "fav_food" trait.
	assert.Equal(t, 3, len(merged))
	bySource := make(map[string]daemon.PersonalityTrait, len(merged))
	for _, tr := range merged {
		bySource[tr.Source] = tr
	}
	// LLM's consolidated trait wins for the Source it covered.
	assert.Equal(t, "user codes at night", bySource["morning_or_night"].Text)
	assert.Equal(t, 0.9, bySource["morning_or_night"].Confidence)
	// Concurrent trait survives untouched.
	assert.Equal(t, "cat's favourite food is tuna", bySource["fav_food"].Text)
	// LLM's other consolidated entry is present.
	assert.Equal(t, "user prefers Go over Python", bySource["fav_language"].Text)
}

// TestMergeDistilledTraits_EmptyInputs covers the boundary cases so the
// merge helper doesn't accidentally produce nil-vs-empty surprises that
// downstream code might trip on.
func TestMergeDistilledTraits_EmptyInputs(t *testing.T) {
	now := petTimeNow
	t.Run("empty snap returns live unchanged", func(t *testing.T) {
		live := []daemon.PersonalityTrait{
			{Text: "trait A", Source: "a", Confidence: 0.5, AddedAt: now},
		}
		merged := mergeDistilledTraits(nil, live)
		assert.Equal(t, 1, len(merged))
		assert.Equal(t, "trait A", merged[0].Text)
	})
	t.Run("empty live returns snap unchanged", func(t *testing.T) {
		snap := []daemon.PersonalityTrait{
			{Text: "trait B", Source: "b", Confidence: 0.5, AddedAt: now},
		}
		merged := mergeDistilledTraits(snap, nil)
		assert.Equal(t, 1, len(merged))
		assert.Equal(t, "trait B", merged[0].Text)
	})
	t.Run("both empty returns empty slice", func(t *testing.T) {
		merged := mergeDistilledTraits(nil, nil)
		assert.Equal(t, 0, len(merged))
	})
}

// TestSeedBank_TraitForKeysMatchChoices ensures every choice-kind
// TraitFor key is a real choice — typos here would silently produce no
// trait at runtime. This is the cheapest defence against bank drift.
func TestSeedBank_TraitForKeysMatchChoices(t *testing.T) {
	for _, q := range SeedQuestions {
		if q.Kind != "choice" {
			continue
		}
		choiceSet := make(map[string]bool, len(q.Choices))
		for _, c := range q.Choices {
			choiceSet[c] = true
		}
		for key := range q.TraitFor {
			if !choiceSet[key] {
				t.Errorf("question %q: TraitFor key %q is not in Choices %v",
					q.ID, key, q.Choices)
			}
		}
	}
}
