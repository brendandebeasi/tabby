package daemon

// Pet Q&A personality-building loop — logic + persistence layer.
//
// This file implements the Phase-1 daemon-side logic for the cat's Q&A loop:
// selecting the next question from the seed bank, applying an answer to pet
// state, distilling traits without an LLM call, and surfacing recent answers
// for the prompt builder. All exported functions operate on the protocol
// PetState (`pkg/daemon`) so the same logic works for the in-process
// coordinator AND for CLI subcommands that round-trip pet.json directly.
//
// IMPORTANT: callers (coordinator, CLI) are responsible for holding the
// state mutex around these calls — the functions themselves are mutex-free
// to keep them composable and testable.
//
// Caps and policy decisions are documented next to the constants below so
// they are easy to find when tuning the feature.

import (
	gocontext "context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync/atomic"
	"time"

	"github.com/brendandebeasi/tabby/pkg/config"
	"github.com/brendandebeasi/tabby/pkg/daemon"
	"github.com/teilomillet/gollm"
)

// Tunable policy constants for the Q&A loop. Centralised here so the rules
// are inspectable from one place rather than scattered through if-checks.
const (
	// answeredQuestionsCap matches the limit documented on
	// daemon.PetState.AnsweredQuestions — when exceeded, oldest entries are
	// dropped so the JSON file stays small and the prompt builder doesn't
	// have to truncate at read time.
	answeredQuestionsCap = 50
	// traitsCap matches the documented daemon.PetState.Traits cap.
	traitsCap = 20
	// freeTextAnswerMax bounds the length of free-text answers folded into
	// trait templates. Keeps prompt budget predictable when 20 traits get
	// concatenated into buildPetContext().
	freeTextAnswerMax = 120
	// stalenessWindow is how long an answered question stays "fresh" before
	// pickQuestion is allowed to surface it again. Six months keeps the
	// bank rotating without re-asking the user the same trivia every week.
	stalenessWindow = 6 * 30 * 24 * time.Hour
	// recentAnswersDefault is the default N for RecentAnswers when callers
	// pass n <= 0. Matches the value documented in the design plan
	// (3 most recent answers in the LLM prompt section).
	recentAnswersDefault = 3
	// consentQuestionID is the special-case ID for the first-ever question
	// the cat asks. SeedQuestions[0] in pet_questions.go must have this ID.
	consentQuestionID = "consent"
	// llmDistillThreshold is how many undistilled free-text answers must
	// accumulate before the Phase-3 LLM consolidation pass fires on count.
	// Tuned so a chatty user triggers it after ~10 questions, but a
	// slow-reply user still gets a pass on the time-based cadence below.
	llmDistillThreshold = 10
	// llmDistillCadenceMinBatch is the smaller threshold that applies
	// on the time-cadence trigger — a 6-hour pass only fires if there
	// are at least this many undistilled answers, so a brand-new install
	// with a single free-text response doesn't burn budget right away.
	llmDistillCadenceMinBatch = 3
	// llmDistillCadence is the minimum gap between LLM distillation passes
	// when the count threshold hasn't been hit yet. Six hours keeps the
	// API budget bounded even if free_text entries trickle in continuously.
	llmDistillCadence = 6 * time.Hour
	// llmDistillMaxProposals caps how many traits the LLM can propose per
	// pass — the cat's trait list is already capped at 20, and an LLM
	// that returns 50 proposals would just churn the eviction policy.
	llmDistillMaxProposals = 8
	// llmDistillTimeout bounds the LLM Generate call. Picked larger than
	// the thought-generation timeout because the prompt is bigger.
	llmDistillTimeout = 30 * time.Second
	// llmDistillTokenBudget is the max_tokens hint passed to gollm for the
	// distillation call. JSON output of 8 short proposals fits comfortably
	// under 800 tokens.
	llmDistillTokenBudget = 800
	// llmDistillTemperature keeps the distillation pass conservative —
	// we want stable, deduplicated traits, not creative reinterpretation.
	llmDistillTemperature = 0.2
	// Consent answer strings — kept here (not in pet_questions.go) because
	// AnswerQuestion needs to act on the literal choice text. If you rename
	// a consent choice you must update this constant too.
	consentAnswerYes        = "Yes, ask away"
	consentAnswerNoFreeText = "Multi-choice only (no free-text)"
	consentAnswerNo         = "No thanks"
)

// petQARand is a package-level random source dedicated to question selection.
// The wider daemon package uses the global math/rand source via rand.Intn;
// we keep a separate Source here so deterministic tests can swap it later
// without disturbing pet animation randomness. Seeded once at package init.
var petQARand = rand.New(rand.NewSource(time.Now().UnixNano())) //nolint:gosec // non-cryptographic

// PickQuestion returns the next question the cat should pose, or nil when
// no question is currently appropriate. Returning nil is the normal "stay
// quiet" path; callers should not treat it as an error.
//
// Skip rules (each returns nil):
//
//   - feature opted out at runtime (pet.QAOptedOut)
//   - feature disabled in config (qa.Disabled)
//   - cooldown still in effect (now.Before(pet.QuestionCooldown))
//   - pet is in a state where bothering the user would feel wrong
//     (currently: "dead", "starving") — conservative list; the plan's
//     "adventuring" gate is enforced by the coordinator since adventure
//     state isn't visible on the protocol PetState
//
// First-time path: when AnsweredQuestions is empty the consent question is
// always returned regardless of any other consideration (other than the
// hard opt-out/config-disabled gates above). This is the privacy
// guardrail described in /Users/b/.claude/plans/wiggly-discovering-starlight.md.
//
// Selection: from the remaining bank, entries already answered within the
// stalenessWindow are filtered out, then free-text entries are filtered
// out if free-text has been opted out by the user or by config. A random
// entry is picked from what remains; nil is returned if nothing is
// eligible.
func PickQuestion(pet *daemon.PetState, qa config.PetWidgetQA, now time.Time) *daemon.PendingQuestion {
	// Defensive: a nil pet should be impossible from real call sites but
	// nil-checking keeps tests and the CLI safer.
	if pet == nil {
		return nil
	}
	if pet.QAOptedOut || qa.Disabled {
		return nil
	}
	if !pet.QuestionCooldown.IsZero() && now.Before(pet.QuestionCooldown) {
		return nil
	}
	// Conservative "is the cat in a bad headspace" gate. Adventure mode
	// activeness is tracked on the internal coordinator petState, not on
	// the protocol PetState, so the caller is expected to skip those.
	switch pet.State {
	case "dead", "starving":
		return nil
	}

	// First-time consent gate. Don't let any other question be picked
	// before the user has explicitly consented (or opted out).
	if len(pet.AnsweredQuestions) == 0 {
		if def, ok := findSeedByID(consentQuestionID); ok {
			return buildPendingQuestion(&def, now, qa)
		}
		// If the consent question is somehow missing from the bank,
		// stay quiet rather than pick a random first question. This is
		// load-bearing for privacy.
		return nil
	}

	// Build the index of recently-answered IDs so we can filter them out.
	// Anything older than stalenessWindow is fair game to re-ask.
	recentlyAnswered := make(map[string]bool, len(pet.AnsweredQuestions))
	for _, a := range pet.AnsweredQuestions {
		if now.Sub(a.Timestamp) < stalenessWindow {
			recentlyAnswered[a.ID] = true
		}
	}

	eligible := make([]*QuestionDef, 0, len(SeedQuestions))
	for i := range SeedQuestions {
		def := &SeedQuestions[i]
		if def.ID == consentQuestionID {
			// Consent is special-cased above and never picked again.
			continue
		}
		if recentlyAnswered[def.ID] {
			continue
		}
		if def.Kind == "free_text" && (pet.QAFreeTextOptedOut || qa.FreeTextDisabled) {
			continue
		}
		eligible = append(eligible, def)
	}

	// Phase-3: layer in LLM-generated questions. Two design decisions worth
	// being explicit about:
	//
	//  1. Pool mixing policy. Each eligible entry (seed OR LLM) gets an
	//     equal weight in the random draw — we don't bias toward one pool
	//     or alternate strictly. This is the cleanest behaviour while the
	//     seed bank still has unanswered entries (the buffer subtly adds
	//     variety) AND it degrades gracefully once the seed bank is
	//     exhausted (the LLM pool then becomes the sole source). The
	//     alternative — "always prefer seed until exhausted" — would mean
	//     users only see LLM questions after ~one month of daily use, by
	//     which point the Phase-3 work is invisible.
	//
	//  2. The buffer refresh is fire-and-forget from here. PickQuestion
	//     returns whatever is currently cached. The trigger only matters
	//     for the NEXT call (or the call after that). This satisfies the
	//     brief's "NEVER call the LLM synchronously inside PickQuestion".
	freeTextAllowed := !(pet.QAFreeTextOptedOut || qa.FreeTextDisabled)
	var llmPool []daemon.PendingQuestion
	if qa.LLMQuestions {
		triggerQuestionGeneration(pet.Traits, RecentAnswers(pet, 0), qa.LLMQuestions)
		llmPool = drawLLMQuestions(recentlyAnswered, freeTextAllowed)
	}

	if len(eligible) == 0 && len(llmPool) == 0 {
		return nil
	}

	// Uniform pick across the combined pool size. We compute the cut-over
	// index so the seed half of the range maps to `eligible` and the LLM
	// half maps to `llmPool` — this avoids materialising a single combined
	// slice (which would force us to convert QuestionDef into
	// daemon.PendingQuestion eagerly even for entries that won't be picked).
	total := len(eligible) + len(llmPool)
	idx := petQARand.Intn(total)
	if idx < len(eligible) {
		return buildPendingQuestion(eligible[idx], now, qa)
	}
	// LLM pick: the buffer entry is mostly ready, we just overlay the
	// configured expiry so the renderer treats it consistently with bank
	// picks.
	picked := llmPool[idx-len(eligible)]
	if qa.ExpireHours > 0 {
		picked.Expires = now.Add(time.Duration(qa.ExpireHours) * time.Hour)
	} else {
		// Defensive: an LLM entry loaded from disk could have a non-zero
		// Expires from a previous run; zero it so the renderer doesn't
		// treat it as already-expired.
		picked.Expires = time.Time{}
	}
	picked.Source = "llm"
	// Remove from the buffer so a follow-up call doesn't re-pick the same
	// question before AnswerQuestion has had a chance to record it.
	removeLLMQuestion(picked.ID)
	pickedCopy := picked
	return &pickedCopy
}

// AnswerQuestion records the user's answer to the currently pending
// question, clears the pending slot, advances the cooldown, and runs trait
// distillation. Returns an error if there is no pending question or the ID
// does not match (so the CLI / popup can fail loudly on stale input).
//
// The cooldown advance uses qa.CooldownHours; if it's zero, the call still
// clears PendingQuestion but does not bump the cooldown (caller's choice).
func AnswerQuestion(pet *daemon.PetState, id, answer string, qa config.PetWidgetQA, now time.Time) error {
	if pet == nil {
		return errors.New("pet state is nil")
	}
	if pet.PendingQuestion == nil {
		return errors.New("no pending question to answer")
	}
	if pet.PendingQuestion.ID != id {
		return fmt.Errorf("pending question id %q does not match answer id %q", pet.PendingQuestion.ID, id)
	}

	pending := pet.PendingQuestion
	trimmedAnswer := strings.TrimSpace(answer)

	// Append to history first (cap-at-N with oldest-dropped policy).
	pet.AnsweredQuestions = append(pet.AnsweredQuestions, daemon.AnsweredQuestion{
		ID:        pending.ID,
		Text:      pending.Text,
		Answer:    trimmedAnswer,
		Kind:      pending.Kind,
		Timestamp: now,
	})
	if len(pet.AnsweredQuestions) > answeredQuestionsCap {
		// Drop the oldest entries to bring length back to the cap.
		excess := len(pet.AnsweredQuestions) - answeredQuestionsCap
		pet.AnsweredQuestions = pet.AnsweredQuestions[excess:]
	}

	// Consent question drives runtime opt-out bits, not the trait list.
	// Anything else goes through normal trait distillation.
	if pending.ID == consentQuestionID {
		applyConsentAnswer(pet, trimmedAnswer)
	} else if def, ok := findSeedByID(pending.ID); ok {
		DistillTrait(pet, def, trimmedAnswer, now)
	}
	// If the question came from somewhere other than the seed bank
	// (e.g. a future Phase-3 LLM-generated question) we silently skip
	// distillation rather than fail. Trait extraction for those will be
	// handled by a different code path.

	// Clear the pending slot and bump the cooldown for the next question.
	pet.PendingQuestion = nil
	pet.LastQuestionShown = now
	if qa.CooldownHours > 0 {
		pet.QuestionCooldown = now.Add(time.Duration(qa.CooldownHours) * time.Hour)
	}
	return nil
}

// ForgetAnswer removes a single answered question by ID and any trait that
// was distilled from it (Trait.Source == id). Returns an error if no
// matching answer is found. Used by `tabby pet forget <id>` so users can
// retract anything they regret sharing.
func ForgetAnswer(pet *daemon.PetState, id string) error {
	if pet == nil {
		return errors.New("pet state is nil")
	}
	if id == "" {
		return errors.New("answer id is required")
	}
	// Walk in-place to preserve order; we expect O(50) entries so a plain
	// linear scan is fine.
	found := false
	filtered := pet.AnsweredQuestions[:0]
	for _, a := range pet.AnsweredQuestions {
		if a.ID == id {
			found = true
			continue
		}
		filtered = append(filtered, a)
	}
	// Defensive copy: filtered shares backing array with the original; if
	// any caller still has a snapshot it should keep working, but we also
	// truncate explicitly so the dropped tail doesn't pin memory.
	for i := len(filtered); i < len(pet.AnsweredQuestions); i++ {
		pet.AnsweredQuestions[i] = daemon.AnsweredQuestion{}
	}
	pet.AnsweredQuestions = filtered

	if !found {
		return fmt.Errorf("no answered question with id %q", id)
	}

	// Also drop any traits whose Source matches this question ID so the
	// cat doesn't keep referencing a thing the user just told it to
	// forget.
	traits := pet.Traits[:0]
	for _, t := range pet.Traits {
		if t.Source == id {
			continue
		}
		traits = append(traits, t)
	}
	for i := len(traits); i < len(pet.Traits); i++ {
		pet.Traits[i] = daemon.PersonalityTrait{}
	}
	pet.Traits = traits
	return nil
}

// DistillTrait converts a question + answer pair into a PersonalityTrait
// using the QuestionDef.TraitFor map. Phase-1 rule-based:
//
//   - Kind == "choice": looks up TraitFor[answer]; missing entries silently
//     skip (e.g. the consent question's empty TraitFor map, or "skip"
//     style choices we don't want to learn from).
//   - Kind == "free_text": looks up TraitFor["*"] and interpolates the
//     single %s with the trimmed/truncated user input. No template? skip.
//
// Confidence is 1.0 for deterministic choice mappings and 0.7 for free
// text (the user might be joking or sarcastic). When pet.Traits would
// exceed traitsCap, the lowest-confidence (oldest as tiebreaker) entry is
// dropped to make room.
func DistillTrait(pet *daemon.PetState, q QuestionDef, answer string, now time.Time) {
	if pet == nil {
		return
	}
	if len(q.TraitFor) == 0 {
		return
	}
	trimmed := strings.TrimSpace(answer)
	var text string
	var confidence float64
	switch q.Kind {
	case "choice":
		tmpl, ok := q.TraitFor[trimmed]
		if !ok || tmpl == "" {
			return
		}
		text = tmpl
		confidence = 1.0
	case "free_text":
		tmpl, ok := q.TraitFor["*"]
		if !ok || tmpl == "" || trimmed == "" {
			return
		}
		text = fmt.Sprintf(tmpl, truncate(trimmed, freeTextAnswerMax))
		confidence = 0.7
	default:
		return
	}

	trait := daemon.PersonalityTrait{
		Text:       text,
		Source:     q.ID,
		Confidence: confidence,
		AddedAt:    now,
	}
	// Replace an existing trait with the same source (re-answering the
	// same question should update, not duplicate). Same Source means we
	// previously distilled from this question.
	for i := range pet.Traits {
		if pet.Traits[i].Source == q.ID {
			pet.Traits[i] = trait
			return
		}
	}

	pet.Traits = append(pet.Traits, trait)
	if len(pet.Traits) > traitsCap {
		evictWeakestTrait(pet)
	}
}

// RecentAnswers returns the most recent n answers from the history, newest
// first. Used by buildPetContext() to inject "recent things they told you"
// into the LLM prompt. Pass n <= 0 to use the default (3).
func RecentAnswers(pet *daemon.PetState, n int) []daemon.AnsweredQuestion {
	if pet == nil {
		return nil
	}
	if n <= 0 {
		n = recentAnswersDefault
	}
	total := len(pet.AnsweredQuestions)
	if total == 0 {
		return nil
	}
	if n > total {
		n = total
	}
	out := make([]daemon.AnsweredQuestion, 0, n)
	// History is appended in chronological order, so newest is at the end.
	for i := total - 1; i >= total-n; i-- {
		out = append(out, pet.AnsweredQuestions[i])
	}
	return out
}

// ---- helpers -------------------------------------------------------------

// findSeedByID looks up a question definition in the seed bank. Returns
// false when no entry matches; callers treat this as "skip silently"
// (e.g. when a stored PendingQuestion came from a future LLM source we
// don't recognise).
func findSeedByID(id string) (QuestionDef, bool) {
	for i := range SeedQuestions {
		if SeedQuestions[i].ID == id {
			return SeedQuestions[i], true
		}
	}
	return QuestionDef{}, false
}

// buildPendingQuestion constructs a PendingQuestion from a seed entry,
// computing the expiry timestamp from qa.ExpireHours. Zero or negative
// ExpireHours leaves Expires as the zero value, which the renderer
// interprets as "no expiry yet".
func buildPendingQuestion(def *QuestionDef, now time.Time, qa config.PetWidgetQA) *daemon.PendingQuestion {
	pq := &daemon.PendingQuestion{
		ID:     def.ID,
		Text:   def.Text,
		Kind:   def.Kind,
		Source: "bank",
	}
	if len(def.Choices) > 0 {
		// Copy to a fresh slice so later mutations to SeedQuestions don't
		// leak into the pet state.
		pq.Choices = append([]string(nil), def.Choices...)
	}
	if qa.ExpireHours > 0 {
		pq.Expires = now.Add(time.Duration(qa.ExpireHours) * time.Hour)
	}
	return pq
}

// applyConsentAnswer maps the consent question's answer to the runtime
// opt-out flags. Unknown answers (typos, future choices, free-text input
// against a choice question) are treated as the conservative default
// "user said yes to everything" — they don't accidentally opt the user
// out. If we want to be stricter we can flip this later.
func applyConsentAnswer(pet *daemon.PetState, answer string) {
	switch strings.TrimSpace(answer) {
	case consentAnswerYes:
		// No flags to change; consent is the implicit default.
	case consentAnswerNoFreeText:
		pet.QAFreeTextOptedOut = true
	case consentAnswerNo:
		pet.QAOptedOut = true
	}
}

// mergeDistilledTraits combines the LLM's consolidated trait list (snap)
// with the live trait list, preserving any traits whose Source was added
// to live AFTER the snapshot was taken (i.e. by a concurrent
// AnswerQuestion call during the LLM round-trip). For each Source the
// LLM produced, snap wins — that's the whole point of distillation.
//
// Caller is responsible for re-applying the traits cap; this function
// preserves all eligible entries.
func mergeDistilledTraits(snap, live []daemon.PersonalityTrait) []daemon.PersonalityTrait {
	snapSources := make(map[string]bool, len(snap))
	for _, t := range snap {
		snapSources[t.Source] = true
	}
	merged := append([]daemon.PersonalityTrait(nil), snap...)
	for _, t := range live {
		if !snapSources[t.Source] {
			merged = append(merged, t)
		}
	}
	return merged
}

// evictWeakestTrait drops the single weakest trait from pet.Traits to make
// room for a new one. "Weakest" = lowest confidence; ties broken by oldest
// AddedAt. Called only when len(Traits) > traitsCap.
func evictWeakestTrait(pet *daemon.PetState) {
	if len(pet.Traits) == 0 {
		return
	}
	worstIdx := 0
	for i := 1; i < len(pet.Traits); i++ {
		ci := pet.Traits[i].Confidence
		cb := pet.Traits[worstIdx].Confidence
		if ci < cb {
			worstIdx = i
			continue
		}
		if ci == cb && pet.Traits[i].AddedAt.Before(pet.Traits[worstIdx].AddedAt) {
			worstIdx = i
		}
	}
	// Remove worstIdx in place, preserving order.
	pet.Traits = append(pet.Traits[:worstIdx], pet.Traits[worstIdx+1:]...)
}

// ───── Phase 3: LLM-powered trait distillation ─────────────────────────────
//
// The rule-based DistillTrait above converts choice answers into traits with
// confidence 1.0 and free-text answers into "user reaches for %s first"
// style strings with confidence 0.7. Those raw free-text strings are
// useful but verbose — when many accumulate the prompt budget bloats and
// they often repeat each other ("user reaches for Go first" + "user uses
// Go daily" -> consolidate to one).
//
// The Phase-3 LLM pass batches up undistilled free-text answers,
// summarises them into stable consolidated traits, and writes those back
// onto pet.Traits — replacing the verbose source-tagged raw traits when
// the LLM's proposal covers them. The pass is opt-in (PetWidgetQA.
// LLMQuestions, the same gate as Phase 3 LLM-generated questions — one
// LLM master switch for Q&A), defensive against bad JSON, and always
// runs on a background goroutine so the daemon tick doesn't block on a
// network call.

// llmTraitProposal is the JSON shape the LLM is asked to emit, one entry
// per consolidated trait. Decoded with permissive json.Unmarshal; missing
// or extra fields are tolerated.
type llmTraitProposal struct {
	TraitText  string   `json:"trait_text"`
	Confidence float64  `json:"confidence"`
	SourceIDs  []string `json:"source_question_ids"`
}

// llmDistillInFlight ensures we never run two distillation passes
// concurrently. The trigger logic spawns a goroutine; the goroutine
// CAS's this flag before calling the LLM and clears it on return.
// math/atomic is enough since contention is at most a few writers.
var llmDistillInFlight atomic.Bool

// lastLLMDistillation tracks the last time the pass actually completed
// (success or skip-after-LLM-call). Read by ShouldRunLLMDistillation to
// enforce the time-based cadence. Goroutine-local accesses serialised by
// the inFlight CAS gate above.
var lastLLMDistillation time.Time

// ShouldRunLLMDistillation returns true when the policy gates allow a
// distillation pass to fire on this tick. Pure — no side effects, safe to
// call under a read lock. Conditions:
//
//	1. Feature gate on (qa.LLMQuestions). Without this every other check
//	   is moot.
//	2. User hasn't opted out (pet.QAOptedOut). Privacy guardrail — if
//	   they said "No thanks" we never call any LLM about them.
//	3. At least one undistilled free-text answer exists. Otherwise there
//	   is literally nothing to distill.
//	4. Either:
//	     - undistilled-count >= llmDistillThreshold, or
//	     - llmDistillCadence has elapsed since the last completed pass.
//	   The cadence is on the AGE of the last completed pass, not the
//	   age of the cat, so a fresh install can fire as soon as there are
//	   undistilled answers (lastLLMDistillation is zero on startup).
//	5. Not already in flight (llmDistillInFlight CAS handled by caller).
//
// Returns the slice of undistilled answers ready to send to the LLM so
// the caller doesn't have to walk the history twice.
func ShouldRunLLMDistillation(pet *daemon.PetState, qa config.PetWidgetQA, now time.Time) (bool, []daemon.AnsweredQuestion) {
	if pet == nil {
		return false, nil
	}
	if !qa.LLMQuestions {
		return false, nil
	}
	if pet.QAOptedOut {
		return false, nil
	}
	undistilled := undistilledFreeTextAnswers(pet)
	if len(undistilled) == 0 {
		return false, nil
	}
	if len(undistilled) >= llmDistillThreshold {
		return true, undistilled
	}
	// Cadence path: a slow trickle of answers still gets distilled
	// eventually, but only when the batch is non-trivial.
	if len(undistilled) >= llmDistillCadenceMinBatch &&
		(lastLLMDistillation.IsZero() || now.Sub(lastLLMDistillation) >= llmDistillCadence) {
		return true, undistilled
	}
	return false, nil
}

// undistilledFreeTextAnswers walks pet.AnsweredQuestions and returns the
// free-text entries whose Distilled flag is still false. Returned in
// chronological (oldest-first) order so the LLM sees the user's
// "evolution" of answers if they re-answered the same theme.
func undistilledFreeTextAnswers(pet *daemon.PetState) []daemon.AnsweredQuestion {
	if pet == nil || len(pet.AnsweredQuestions) == 0 {
		return nil
	}
	out := make([]daemon.AnsweredQuestion, 0, len(pet.AnsweredQuestions))
	for _, a := range pet.AnsweredQuestions {
		if a.Kind != "free_text" {
			continue
		}
		if a.Distilled {
			continue
		}
		if strings.TrimSpace(a.Answer) == "" {
			continue
		}
		out = append(out, a)
	}
	return out
}

// llmGenerator abstracts the gollm.Generate call so unit tests can
// substitute a deterministic stub. Production code passes a closure that
// invokes llmClient.Generate directly.
type llmGenerator func(ctx gocontext.Context, prompt string) (string, error)

// DistillTraitsLLM is the synchronous core of the Phase-3 pass: takes the
// undistilled free-text answers + current traits, calls the LLM, parses
// the JSON proposals, and merges them back into pet.Traits / marks the
// source answers Distilled. Returns the number of proposals applied (0
// on no-op or LLM/parse failure — failures never panic and never lose
// existing traits).
//
// Caller is responsible for:
//   - holding the appropriate write lock around the mutation
//   - choosing whether to call this on the foreground or a goroutine
//   - obtaining the generator (see RunLLMDistillationBackground)
//
// We deliberately do NOT enforce QAOptedOut here — ShouldRunLLMDistillation
// gates that — so a caller with a hand-built generator (e.g. a test) gets
// a predictable code path.
func DistillTraitsLLM(
	ctx gocontext.Context,
	pet *daemon.PetState,
	qa config.PetWidgetQA,
	gen llmGenerator,
	now time.Time,
) int {
	if pet == nil || gen == nil {
		return 0
	}
	undistilled := undistilledFreeTextAnswers(pet)
	if len(undistilled) == 0 {
		return 0
	}

	prompt := buildDistillationPrompt(pet, undistilled)
	resp, err := gen(ctx, prompt)
	if err != nil {
		// Never tear down existing traits on LLM failure — just bail.
		// The next eligible tick will retry.
		return 0
	}

	proposals := parseLLMProposals(resp)
	if len(proposals) == 0 {
		// LLM returned garbage / empty / unparseable JSON. Mark the
		// undistilled answers as Distilled anyway so we don't burn API
		// budget retrying the same batch forever — but ONLY if the LLM
		// gave us SOMETHING back (resp non-empty). On empty response we
		// preserve undistilled state so the next cadence tick can retry.
		if strings.TrimSpace(resp) != "" {
			markAnswersDistilled(pet, undistilled)
		}
		return 0
	}
	if len(proposals) > llmDistillMaxProposals {
		proposals = proposals[:llmDistillMaxProposals]
	}

	applied := applyLLMProposals(pet, proposals, now)
	markAnswersDistilled(pet, undistilled)
	return applied
}

// buildDistillationPrompt assembles the LLM input. We include the current
// traits so the model knows what NOT to duplicate, and we tag each Q&A
// pair with its ID so the model can return source_question_ids that
// callers can match back. Free-text answers are truncated to
// freeTextAnswerMax to keep the prompt budget predictable.
func buildDistillationPrompt(pet *daemon.PetState, undistilled []daemon.AnsweredQuestion) string {
	var b strings.Builder
	b.WriteString("You are summarising what a tmux user has told a virtual pet ")
	b.WriteString("about themselves into stable, prompt-ready personality traits. ")
	b.WriteString("Read the existing traits and the new free-text answers below. ")
	b.WriteString("Produce up to ")
	fmt.Fprintf(&b, "%d", llmDistillMaxProposals)
	b.WriteString(" consolidated traits as a JSON array. ")
	b.WriteString("Each entry must be:\n")
	b.WriteString(`  {"trait_text": "...", "confidence": 0.0-1.0, "source_question_ids": ["id1", ...]}` + "\n")
	b.WriteString("Rules:\n")
	b.WriteString("- Merge answers that overlap (e.g. 'loves Go' + 'uses Go daily' -> one trait).\n")
	b.WriteString("- DO NOT restate existing traits verbatim — only refine or add new ones.\n")
	b.WriteString("- Skip any answer that is sarcastic, empty, or off-topic.\n")
	b.WriteString("- Use third-person 'user ...' phrasing, max 80 chars per trait.\n")
	b.WriteString("- Output ONLY the JSON array — no commentary, no code fences.\n\n")

	b.WriteString("Existing traits:\n")
	if len(pet.Traits) == 0 {
		b.WriteString("  (none)\n")
	} else {
		for _, t := range pet.Traits {
			fmt.Fprintf(&b, "  - %s (confidence=%.2f, source=%s)\n",
				strings.TrimSpace(t.Text), t.Confidence, t.Source)
		}
	}

	b.WriteString("\nNew free-text answers to consider:\n")
	for _, a := range undistilled {
		ans := truncate(strings.TrimSpace(a.Answer), freeTextAnswerMax)
		fmt.Fprintf(&b, "  - id=%s question=%q answer=%q\n",
			a.ID, strings.TrimSpace(a.Text), ans)
	}
	b.WriteString("\nJSON array:\n")
	return b.String()
}

// parseLLMProposals extracts the JSON array from the LLM response. The
// model occasionally wraps output in code fences or prefixes it with a
// preamble ("Here is the JSON: ..."); we tolerate both by scanning for
// the first '[' and last ']'. Returns nil on any parse failure so the
// caller treats it the same as "LLM produced nothing useful".
func parseLLMProposals(resp string) []llmTraitProposal {
	resp = strings.TrimSpace(resp)
	if resp == "" {
		return nil
	}
	// Strip code fences if present.
	resp = strings.TrimPrefix(resp, "```json")
	resp = strings.TrimPrefix(resp, "```")
	resp = strings.TrimSuffix(resp, "```")
	resp = strings.TrimSpace(resp)

	start := strings.Index(resp, "[")
	end := strings.LastIndex(resp, "]")
	if start < 0 || end < 0 || end <= start {
		return nil
	}
	candidate := resp[start : end+1]

	var raw []llmTraitProposal
	if err := json.Unmarshal([]byte(candidate), &raw); err != nil {
		return nil
	}

	// Filter out empty / nonsense entries; clamp confidence.
	out := make([]llmTraitProposal, 0, len(raw))
	for _, p := range raw {
		text := strings.TrimSpace(p.TraitText)
		if text == "" {
			continue
		}
		// Bound runaway lengths the same way truncate() does for free-text.
		text = truncate(text, freeTextAnswerMax)
		if p.Confidence < 0 {
			p.Confidence = 0
		}
		if p.Confidence > 1 {
			p.Confidence = 1
		}
		if p.Confidence == 0 {
			// LLM didn't supply confidence — default to 0.8 (higher than
			// rule-based free-text 0.7 because the LLM consolidated
			// multiple answers, but lower than 1.0 deterministic).
			p.Confidence = 0.8
		}
		out = append(out, llmTraitProposal{
			TraitText:  text,
			Confidence: p.Confidence,
			SourceIDs:  p.SourceIDs,
		})
	}
	return out
}

// applyLLMProposals merges proposals into pet.Traits. The merge rules:
//
//   - For each proposal, build a "covers" set of source IDs.
//   - Remove any existing trait whose Source is in the covers set AND
//     whose existing rule-based template indicates a free-text origin
//     (Confidence < 1.0 — choice traits are deterministic and stay).
//     This is the consolidation step: raw "user reaches for Go first"
//     gets replaced by the LLM's "user is a Go developer".
//   - If a proposal exactly matches the text of an existing trait,
//     bump that trait's confidence up to max(existing, proposal) — this
//     is the "confirmed by multiple answers" rule.
//   - Otherwise append the proposal as a new trait with Source = a
//     synthetic "llm:<first-source-id>" tag so ForgetAnswer can still
//     scrub it when the user retracts a contributing answer.
//   - After all merges, evictWeakestTrait until len <= traitsCap.
//
// Returns the number of new traits added or upgraded.
func applyLLMProposals(pet *daemon.PetState, proposals []llmTraitProposal, now time.Time) int {
	if pet == nil || len(proposals) == 0 {
		return 0
	}
	applied := 0
	for _, p := range proposals {
		covers := make(map[string]bool, len(p.SourceIDs))
		for _, id := range p.SourceIDs {
			covers[strings.TrimSpace(id)] = true
		}

		// Step 1: drop raw free-text traits this proposal covers.
		if len(covers) > 0 {
			kept := pet.Traits[:0]
			for _, t := range pet.Traits {
				if covers[t.Source] && t.Confidence < 1.0 {
					continue // consolidated away
				}
				kept = append(kept, t)
			}
			// Clear pinned tail to avoid memory leak.
			for i := len(kept); i < len(pet.Traits); i++ {
				pet.Traits[i] = daemon.PersonalityTrait{}
			}
			pet.Traits = kept
		}

		// Step 2: upgrade confidence on exact-text match, else append.
		matched := false
		for i := range pet.Traits {
			if strings.EqualFold(strings.TrimSpace(pet.Traits[i].Text), p.TraitText) {
				if p.Confidence > pet.Traits[i].Confidence {
					pet.Traits[i].Confidence = p.Confidence
					applied++
				}
				matched = true
				break
			}
		}
		if matched {
			continue
		}

		// Compose a Source tag so ForgetAnswer still cleans up.
		source := "llm"
		for _, id := range p.SourceIDs {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			source = "llm:" + id
			break
		}
		pet.Traits = append(pet.Traits, daemon.PersonalityTrait{
			Text:       p.TraitText,
			Source:     source,
			Confidence: p.Confidence,
			AddedAt:    now,
		})
		applied++
		// Enforce cap after each append so the cheapest eviction wins.
		for len(pet.Traits) > traitsCap {
			evictWeakestTrait(pet)
		}
	}
	return applied
}

// markAnswersDistilled flips Distilled=true on every free-text answer in
// pet.AnsweredQuestions whose (ID, Timestamp) matches one of the given
// processed entries. Matching on both fields handles the (rare) case
// where the same question ID appears twice in history with different
// answers.
func markAnswersDistilled(pet *daemon.PetState, processed []daemon.AnsweredQuestion) {
	if pet == nil || len(processed) == 0 {
		return
	}
	seen := make(map[string]bool, len(processed))
	for _, p := range processed {
		seen[distilledKey(p.ID, p.Timestamp)] = true
	}
	for i := range pet.AnsweredQuestions {
		a := &pet.AnsweredQuestions[i]
		if a.Kind != "free_text" {
			continue
		}
		if seen[distilledKey(a.ID, a.Timestamp)] {
			a.Distilled = true
		}
	}
}

// distilledKey is the (ID, Timestamp) composite used to match
// undistilled answers back to their history slot.
func distilledKey(id string, ts time.Time) string {
	return id + "@" + ts.UTC().Format(time.RFC3339Nano)
}

// RunLLMDistillationBackground is the production entry point — spawns a
// goroutine that snapshots the Q&A state, calls the LLM, and writes the
// results back under the supplied apply callback (which is expected to
// take c.stateMu). Returns immediately; the bool indicates whether a new
// pass was actually launched (false on gate-off, no-undistilled, or
// already-in-flight).
//
// The snapshot is a deep copy of the Q&A-only fields so the LLM call
// touches no live state. The apply callback is the only place where the
// caller's lock is taken.
func RunLLMDistillationBackground(
	pet *daemon.PetState,
	qa config.PetWidgetQA,
	petName string,
	now time.Time,
	apply func(mutate func(p *daemon.PetState)),
) bool {
	if apply == nil {
		return false
	}
	// Skip up-front when the LLM client never initialised — otherwise we
	// would CAS the in-flight flag, spawn a goroutine, and immediately
	// drop out on every tick. Cheap field-read avoids that churn.
	if llmClient == nil {
		return false
	}
	should, _ := ShouldRunLLMDistillation(pet, qa, now)
	if !should {
		return false
	}
	if !llmDistillInFlight.CompareAndSwap(false, true) {
		return false
	}
	// Snapshot the pet state's Q&A fields so the goroutine works against
	// an immutable copy. We deliberately don't carry over animation
	// fields — DistillTraitsLLM only reads traits + answers + opt-out.
	snap := daemon.PetState{
		AnsweredQuestions:  append([]daemon.AnsweredQuestion(nil), pet.AnsweredQuestions...),
		Traits:             append([]daemon.PersonalityTrait(nil), pet.Traits...),
		QAOptedOut:         pet.QAOptedOut,
		QAFreeTextOptedOut: pet.QAFreeTextOptedOut,
	}
	go func() {
		defer llmDistillInFlight.Store(false)
		if llmClient == nil {
			return
		}
		gen := func(ctx gocontext.Context, prompt string) (string, error) {
			return llmClient.Generate(ctx, gollm.NewPrompt(prompt))
		}
		ctx, cancel := gocontext.WithTimeout(gocontext.Background(), llmDistillTimeout)
		defer cancel()
		applied := DistillTraitsLLM(ctx, &snap, qa, gen, now)
		// Record the pass timestamp regardless of how many proposals
		// applied — a "no proposals" outcome still counts as having
		// run the model on the current batch, and the cadence gate
		// shouldn't let us re-fire immediately. Set BEFORE the apply
		// callback so a panic in the callback doesn't leave us
		// permanently flagged.
		lastLLMDistillation = time.Now()
		_ = petName // reserved for future per-pet logging

		// Apply Q&A field changes back onto the live state. The
		// callback is responsible for any persistence (savePetState
		// etc.) — we just supply the mutation. If applied==0 AND the
		// only mutation was marking answers Distilled (e.g. parse
		// failure path), we still apply so we don't retry the same
		// batch endlessly.
		apply(func(live *daemon.PetState) {
			// Merge snap.Traits (the LLM's authoritative consolidated
			// view) with any traits added to live.Traits during the
			// ~30 s LLM call window — see mergeDistilledTraits below.
			live.Traits = mergeDistilledTraits(snap.Traits, live.Traits)
			// Respect the trait cap: if the merge pushed us over,
			// evict weakest-first using the existing policy.
			for len(live.Traits) > traitsCap {
				evictWeakestTrait(live)
			}
			// Sync Distilled flags by (id, timestamp). The live state
			// may have grown new entries while the LLM call was in
			// flight; we only touch matching ones.
			seen := make(map[string]bool, len(snap.AnsweredQuestions))
			for _, a := range snap.AnsweredQuestions {
				if a.Distilled {
					seen[distilledKey(a.ID, a.Timestamp)] = true
				}
			}
			for i := range live.AnsweredQuestions {
				la := &live.AnsweredQuestions[i]
				if la.Kind != "free_text" {
					continue
				}
				if seen[distilledKey(la.ID, la.Timestamp)] {
					la.Distilled = true
				}
			}
		})
		_ = applied
	}()
	return true
}

// truncate clips s to at most max runes (not bytes) so multi-byte UTF-8
// input doesn't split mid-rune. Appends an ellipsis ("…") when truncation
// happens so downstream prompt text makes it obvious the user said more.
func truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max-1]) + "…"
}
