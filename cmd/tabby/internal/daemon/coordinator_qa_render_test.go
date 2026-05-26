// Phase 2 — sidebar consent UX tests.
//
// Companion to:
//
//   - coordinator.go : renderPetWidget teaser substitution, click branch
//     for QuestionPromptLine, launchQuestionPopup, UpdatePetState tick
//     wiring that drives PickQuestion + expiry.
//   - pet_qa.go      : PickQuestion / AnswerQuestion (Phase 1, exercised
//     indirectly through the tick).
//   - petqapopup     : separate test file in the popup package covers the
//     TUI smoke path.
//
// These tests pin the four contracts the lead asked for:
//
//  1. Teaser appears only when {PendingQuestion != nil, !QAOptedOut,
//     TeaserEveryNThoughts > 0} AND the AnimFrame block aligns with the
//     cadence. QuestionPromptLine matches the row when shown; otherwise -1.
//  2. handlePetWidgetClick routes a click on QuestionPromptLine to the
//     popup launcher (verified indirectly: returns true AND FoodItem is
//     unchanged, proving FeedLine was NOT triggered).
//  3. UpdatePetState picks a new question once the cooldown has elapsed
//     (and pet is not adventuring / dead), and clears expired pendings.
//  4. Pre-existing click routes (FeedLine, etc.) still work — regression
//     guard against the new branch swallowing every click.
//
// Tests do NOT spawn tmux; the launchQuestionPopup goroutine attempts an
// exec.Command("tmux", "display-popup", ...) that will simply fail and
// exit on a system without a daemon-attached session. The tests assert on
// the synchronous "should this click be handled" return value, which is
// independent of the goroutine outcome.
package daemon

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/brendandebeasi/tabby/pkg/config"
	"github.com/brendandebeasi/tabby/pkg/daemon"
	"github.com/stretchr/testify/assert"
)

// teaserSubstring is a fragment present in every non-fallback teaser
// variant ("🤔 mind if I ask? (click)", "🤔 ask me? (click)",
// "🤔 ask? click", "🤔 ask?"). The render width these tests use puts the
// rendered line on one of the larger variants, so checking for "🤔 ask"
// reliably distinguishes the teaser from the normal thought line without
// the test having to know which width-conditional variant was picked.
const teaserSubstring = "🤔 ask"

// teaserBlockFrames mirrors the unexported constant in coordinator.go.
// Each "block" is this many AnimFrame ticks; the teaser fires when the
// block index is divisible by TeaserEveryNThoughts.
const teaserBlockFrames = 50

// newPetRenderCoordinator returns a coordinator with the pet widget
// enabled and a default-ish QA config. Tests that need a different N pin
// it explicitly; tests that need a pending question seed c.pet.PendingQuestion
// directly under stateMu. Pet.Enabled = true is required for renderPetWidget
// to produce non-empty output.
func newPetRenderCoordinator(t *testing.T) *Coordinator {
	t.Helper()
	// Isolate state directory so savePetStateData (called by UpdatePetState
	// + click handlers) never writes into the real ~/.local/state/tabby.
	t.Setenv("TABBY_STATE_DIR", t.TempDir())

	c := newRenderCoordinator(t)
	c.config.Widgets.Pet.Enabled = true
	c.config.Widgets.Pet.Style = "emoji"
	c.config.Widgets.Pet.QA = defaultQAConfig()
	c.lastWidth = 30
	return c
}

// pendingChoice constructs a PendingQuestion suitable for seeding a pet
// in tests. Uses a real seed-bank id so any code path that looks the
// question up by id (e.g. Phase-3 trait distillation) keeps working.
func pendingChoice(now time.Time) *daemon.PendingQuestion {
	return &daemon.PendingQuestion{
		ID:      "morning_or_night",
		Text:    "test question",
		Kind:    "choice",
		Choices: []string{"morning person", "night owl"},
		Expires: now.Add(24 * time.Hour),
	}
}

// ─── Teaser rendering ──────────────────────────────────────────────────────

// TestRenderPetWidget_QuestionTeaser_AbsentWhenNoPending pins the most
// important guarantee: with no pending question, the teaser must never
// appear in the output and QuestionPromptLine must be the sentinel -1.
func TestRenderPetWidget_QuestionTeaser_AbsentWhenNoPending(t *testing.T) {
	c := newPetRenderCoordinator(t)
	c.pet.PendingQuestion = nil
	// Walk a few AnimFrame values to make sure cadence math can't trigger
	// the teaser without a pending question.
	for _, frame := range []int{0, 50, 100, 150} {
		c.pet.AnimFrame = frame
		out := c.renderPetWidget(30, true)
		assert.NotContains(t, out, teaserSubstring,
			"frame=%d should not show teaser when PendingQuestion==nil", frame)
		assert.Equal(t, -1, c.petLayout.QuestionPromptLine,
			"frame=%d QuestionPromptLine should be -1 when teaser absent", frame)
	}
}

// TestRenderPetWidget_QuestionTeaser_AppearsOnCadence walks the AnimFrame
// across two full teaser-cycle periods and asserts the teaser fires
// exactly on the blocks where (block % N) == 0. With N=3 and 50-frame
// blocks, frames 0, 150, 300 are on; 50, 100, 200, 250 are off.
func TestRenderPetWidget_QuestionTeaser_AppearsOnCadence(t *testing.T) {
	c := newPetRenderCoordinator(t)
	c.config.Widgets.Pet.QA.TeaserEveryNThoughts = 3
	c.pet.PendingQuestion = pendingChoice(petTimeNow)

	cases := []struct {
		frame   int
		wantOn  bool
		comment string
	}{
		{frame: 0, wantOn: true, comment: "block 0 % 3 == 0"},
		{frame: 49, wantOn: true, comment: "still in block 0"},
		{frame: 50, wantOn: false, comment: "block 1 % 3 == 1"},
		{frame: 100, wantOn: false, comment: "block 2 % 3 == 2"},
		{frame: 150, wantOn: true, comment: "block 3 % 3 == 0"},
		{frame: 200, wantOn: false, comment: "block 4 % 3 == 1"},
		{frame: 250, wantOn: false, comment: "block 5 % 3 == 2"},
		{frame: 300, wantOn: true, comment: "block 6 % 3 == 0"},
	}
	for _, tc := range cases {
		c.pet.AnimFrame = tc.frame
		out := c.renderPetWidget(30, true)
		if tc.wantOn {
			assert.Contains(t, out, teaserSubstring,
				"frame=%d (%s) expected teaser ON", tc.frame, tc.comment)
			assert.GreaterOrEqual(t, c.petLayout.QuestionPromptLine, 0,
				"frame=%d (%s) QuestionPromptLine should be set", tc.frame, tc.comment)
		} else {
			assert.NotContains(t, out, teaserSubstring,
				"frame=%d (%s) expected teaser OFF", tc.frame, tc.comment)
			assert.Equal(t, -1, c.petLayout.QuestionPromptLine,
				"frame=%d (%s) QuestionPromptLine should be -1", tc.frame, tc.comment)
		}
	}
}

// TestRenderPetWidget_QuestionTeaser_OptedOutSuppresses is the defensive
// double-gate test. Even with a pending question and a cadence-aligned
// frame, QAOptedOut MUST suppress the teaser. Loss of this gate would
// re-surface the prompt to a user who already said "no thanks".
func TestRenderPetWidget_QuestionTeaser_OptedOutSuppresses(t *testing.T) {
	c := newPetRenderCoordinator(t)
	c.config.Widgets.Pet.QA.TeaserEveryNThoughts = 3
	c.pet.PendingQuestion = pendingChoice(petTimeNow)
	c.pet.QAOptedOut = true
	c.pet.AnimFrame = 0 // would normally fire

	out := c.renderPetWidget(30, true)
	assert.NotContains(t, out, teaserSubstring,
		"teaser must not render when QAOptedOut=true")
	assert.Equal(t, -1, c.petLayout.QuestionPromptLine,
		"QuestionPromptLine should remain -1 when QAOptedOut suppresses teaser")
}

// TestRenderPetWidget_QuestionTeaser_DisabledByConfigN0 covers the
// TeaserEveryNThoughts==0 config-disabled path. The teaser should NEVER
// fire when N==0 regardless of pending state or AnimFrame.
func TestRenderPetWidget_QuestionTeaser_DisabledByConfigN0(t *testing.T) {
	c := newPetRenderCoordinator(t)
	c.config.Widgets.Pet.QA.TeaserEveryNThoughts = 0
	c.pet.PendingQuestion = pendingChoice(petTimeNow)

	for _, frame := range []int{0, 50, 100, 150, 200, 300} {
		c.pet.AnimFrame = frame
		out := c.renderPetWidget(30, true)
		assert.NotContains(t, out, teaserSubstring,
			"frame=%d should not show teaser when TeaserEveryNThoughts==0", frame)
		assert.Equal(t, -1, c.petLayout.QuestionPromptLine,
			"frame=%d QuestionPromptLine should be -1 when N==0", frame)
	}
}

// TestRenderPetWidget_QuestionTeaser_PromptLineMatchesRow verifies the
// click-target contract: when the teaser fires, QuestionPromptLine MUST
// point at the row that actually contains the teaser text in the
// rendered output. The click handler relies on this row index to route
// taps on the teaser to launchQuestionPopup.
func TestRenderPetWidget_QuestionTeaser_PromptLineMatchesRow(t *testing.T) {
	c := newPetRenderCoordinator(t)
	c.config.Widgets.Pet.QA.TeaserEveryNThoughts = 3
	c.pet.PendingQuestion = pendingChoice(petTimeNow)
	c.pet.AnimFrame = 0 // teaser fires

	out := c.renderPetWidget(30, true)
	mustOK(t, assert.Contains(t, out, teaserSubstring))

	promptLine := c.petLayout.QuestionPromptLine
	mustOK(t, assert.GreaterOrEqual(t, promptLine, 0, "QuestionPromptLine must be non-negative when teaser shown"))

	lines := strings.Split(out, "\n")
	mustOK(t, assert.Greater(t, len(lines), promptLine,
		"rendered output must have at least QuestionPromptLine+1 lines"))
	assert.Contains(t, lines[promptLine], teaserSubstring,
		"line at QuestionPromptLine (%d) should contain the teaser; got %q",
		promptLine, lines[promptLine])
}

// ─── Click handler ─────────────────────────────────────────────────────────

// fireClick is a tiny helper that resets the click debounce and dispatches
// a left-press at (x, y) using the supplied layout's ContentStartLine as
// the origin so callers can pass widget-relative Y values.
func fireClick(c *Coordinator, x, y int) bool {
	c.lastPetClick = time.Time{}
	input := &daemon.InputPayload{
		Type:           "mouse",
		Button:         "left",
		Action:         "press",
		MouseX:         x,
		MouseY:         y + c.petLayout.ContentStartLine,
		ViewportOffset: 0,
	}
	return c.handlePetWidgetClick("test-client", input)
}

// TestHandlePetWidgetClick_QuestionPrompt_LaunchesPopup verifies the new
// click branch fires before FeedLine when:
//
//   - PendingQuestion != nil
//   - QuestionPromptLine >= 0 (teaser is on this frame)
//   - the click lands on QuestionPromptLine
//
// We don't have a test seam on launchQuestionPopup; instead we verify
// the indirect effect:
//
//   - the click is absorbed (returns true)
//   - FoodItem is NOT set, proving FeedLine wasn't hit
//   - LastThought is NOT mutated to "food!"
//
// If the branch ordering ever regressed and FeedLine fired first, both of
// those assertions would fail loudly.
func TestHandlePetWidgetClick_QuestionPrompt_LaunchesPopup(t *testing.T) {
	c := newPetRenderCoordinator(t)
	c.pet.FoodItem = pos2D{X: -1, Y: 0}
	c.pet.LastThought = "before-click"
	c.pet.PendingQuestion = pendingChoice(petTimeNow)

	// Simulate a render that placed the teaser at a known row, ensuring
	// QuestionPromptLine != FeedLine so a hit-test on QuestionPromptLine
	// can't accidentally fall through to FeedLine.
	c.petLayout = petWidgetLayout{
		ContentStartLine:   0,
		FeedLine:           1,
		HighAirLine:        9,
		LowAirLine:         10,
		GroundLine:         11,
		PlayWidth:          29,
		WidgetHeight:       15,
		QuestionPromptLine: 4,
	}

	handled := fireClick(c, 5, c.petLayout.QuestionPromptLine)
	assert.True(t, handled, "click on QuestionPromptLine should be absorbed")
	assert.Equal(t, -1, c.pet.FoodItem.X,
		"FoodItem.X must remain -1; if FeedLine fired this would be a positive drop position")
	assert.Equal(t, "before-click", c.pet.LastThought,
		"LastThought should be untouched by Q&A click; FeedLine handler would have overwritten it")
}

// TestHandlePetWidgetClick_QuestionPrompt_NoPendingFallsThrough confirms
// the Q&A branch is gated on PendingQuestion != nil. With a nil pending,
// a click on the same row index must NOT route to the popup launcher;
// since the row index in our setup also matches no other interactive
// line, the click should fall through and return false.
func TestHandlePetWidgetClick_QuestionPrompt_NoPendingFallsThrough(t *testing.T) {
	c := newPetRenderCoordinator(t)
	c.pet.PendingQuestion = nil

	// Layout with QuestionPromptLine deliberately set to a row that is
	// NOT any other interactive line. With PendingQuestion nil, the
	// click handler must NOT enter the Q&A branch even though the row
	// index matches.
	c.petLayout = petWidgetLayout{
		ContentStartLine:   0,
		FeedLine:           1,
		HighAirLine:        9,
		LowAirLine:         10,
		GroundLine:         11,
		PlayWidth:          29,
		WidgetHeight:       15,
		QuestionPromptLine: 4,
	}

	handled := fireClick(c, 5, 4)
	assert.False(t, handled,
		"click on QuestionPromptLine with PendingQuestion=nil should NOT be absorbed by the Q&A branch")
}

// TestHandlePetWidgetClick_QuestionPrompt_PromptLineNegativeFallsThrough
// covers the "teaser not on this frame" case: even with a pending
// question, when QuestionPromptLine == -1 (sentinel) the Q&A branch must
// not fire. This prevents a click in the wrong row absorbing input just
// because a question happens to be pending.
func TestHandlePetWidgetClick_QuestionPrompt_PromptLineNegativeFallsThrough(t *testing.T) {
	c := newPetRenderCoordinator(t)
	c.pet.PendingQuestion = pendingChoice(petTimeNow)

	c.petLayout = petWidgetLayout{
		ContentStartLine:   0,
		FeedLine:           1,
		HighAirLine:        9,
		LowAirLine:         10,
		GroundLine:         11,
		PlayWidth:          29,
		WidgetHeight:       15,
		QuestionPromptLine: -1, // teaser not rendered this frame
	}

	// Click on row 4 (where the teaser WOULD have been). With
	// QuestionPromptLine -1 the Q&A branch is skipped and the click
	// drops to other dispatchers — row 4 isn't an interactive line in
	// this layout so the call returns false.
	handled := fireClick(c, 5, 4)
	assert.False(t, handled,
		"with QuestionPromptLine=-1 click should not be absorbed by Q&A branch")
}

// TestHandlePetWidgetClick_FeedLineStillWorks is the regression guard:
// adding the Q&A branch must not break any existing dispatch. We click
// FeedLine and confirm the food-drop side-effect still fires.
func TestHandlePetWidgetClick_FeedLineStillWorks(t *testing.T) {
	c := newPetRenderCoordinator(t)
	c.pet.FoodItem = pos2D{X: -1, Y: 0}
	c.pet.LastThought = "before-click"
	c.pet.PendingQuestion = pendingChoice(petTimeNow)
	// Give the client a stable width via the per-client map so
	// safeRandRange has a sensible range.
	c.clientWidths["test-client"] = 30

	c.petLayout = petWidgetLayout{
		ContentStartLine:   0,
		FeedLine:           1,
		HighAirLine:        9,
		LowAirLine:         10,
		GroundLine:         11,
		PlayWidth:          29,
		WidgetHeight:       15,
		QuestionPromptLine: 4,
	}

	handled := fireClick(c, 0, c.petLayout.FeedLine)
	assert.True(t, handled, "click on FeedLine should still be handled")
	assert.GreaterOrEqual(t, c.pet.FoodItem.X, 0,
		"FeedLine should have dropped food (X should be positive after drop)")
	assert.Equal(t, "food!", c.pet.LastThought,
		"FeedLine should have set LastThought to 'food!'")
}

// ─── UpdatePetState tick wiring ────────────────────────────────────────────

// TestUpdatePetState_QAPick_PicksWhenIdleAndCooldownElapsed verifies the
// happy path: no pending question, cooldown elapsed (zero-value
// QuestionCooldown is in the past relative to time.Now()), pet idle,
// no adventure. After a tick, PendingQuestion should be populated.
//
// Because the pet has no AnsweredQuestions, PickQuestion's first-time
// path returns the consent question — so the test asserts on the consent
// id rather than a random seed pick.
func TestUpdatePetState_QAPick_PicksWhenIdleAndCooldownElapsed(t *testing.T) {
	c := newPetRenderCoordinator(t)
	c.pet.State = "idle"
	c.pet.Hunger = 100
	c.pet.Happiness = 100
	// Belt-and-suspenders: explicitly clear cooldown so it's strictly in
	// the past — PickQuestion treats zero-value cooldown as "elapsed".
	c.pet.QuestionCooldown = time.Time{}
	c.pet.PendingQuestion = nil

	_ = c.UpdatePetState()

	mustOK(t, assert.NotNil(t, c.pet.PendingQuestion,
		"UpdatePetState should have picked a question on an idle, cooldown-elapsed tick"))
	assert.Equal(t, "consent", c.pet.PendingQuestion.ID,
		"first-time pick should be the consent question")
}

// TestUpdatePetState_QAPick_SkipsDuringCooldown verifies the cooldown
// gate: with QuestionCooldown set well in the future, the tick must
// NOT call PickQuestion (or rather, PickQuestion is called but returns
// nil because of the cooldown). PendingQuestion stays nil.
func TestUpdatePetState_QAPick_SkipsDuringCooldown(t *testing.T) {
	c := newPetRenderCoordinator(t)
	c.pet.State = "idle"
	c.pet.Hunger = 100
	c.pet.Happiness = 100
	// Bypass the first-time consent gate so the test exercises the
	// general cooldown path rather than the first-call special case.
	c.pet.AnsweredQuestions = []daemon.AnsweredQuestion{
		{ID: "consent", Answer: "Yes, ask away", Timestamp: time.Now().Add(-1 * time.Hour)},
	}
	c.pet.QuestionCooldown = time.Now().Add(24 * time.Hour) // cooldown still active
	c.pet.PendingQuestion = nil

	_ = c.UpdatePetState()

	assert.Nil(t, c.pet.PendingQuestion,
		"UpdatePetState must not pick a question while QuestionCooldown is in the future")
}

// TestUpdatePetState_QAPick_SkipsDuringAdventure verifies the adventure-
// mode gate. The plan calls out that adventure-mode skip lives in the
// coordinator (the wire-format PetState doesn't expose Adventure), so
// the coordinator's tick is the load-bearing site. When Adventure.Active
// is true the early-return path runs and never even reaches the Q&A
// block, but we still assert the user-visible contract: no PendingQuestion
// gets set during an adventure.
func TestUpdatePetState_QAPick_SkipsDuringAdventure(t *testing.T) {
	c := newPetRenderCoordinator(t)
	c.pet.State = "walking"
	c.pet.Hunger = 100
	c.pet.Happiness = 100
	c.pet.QuestionCooldown = time.Time{}
	c.pet.PendingQuestion = nil
	c.pet.Adventure = adventureState{
		Active:        true,
		Phase:         advPhaseExploring,
		PhaseStart:    time.Now(),
		PhaseDuration: 24 * time.Hour, // never advances during the test
		Biome:         "meadow",
		ManuallyTriggered: true, // avoids the "adventure disabled in config" reset path
	}

	_ = c.UpdatePetState()

	assert.Nil(t, c.pet.PendingQuestion,
		"UpdatePetState must not pick a question while the pet is on an adventure")
}

// TestUpdatePetState_QAPick_ExpiresStalePending verifies the expiry path:
// when PendingQuestion.Expires is in the past, the tick clears the
// pending slot. With the cooldown also elapsed and no opt-out, the same
// tick should then rotate in a fresh question — the test asserts on the
// "cleared and replaced" property by checking either a different id was
// chosen, or at minimum the expired question no longer has its old
// Expires timestamp.
func TestUpdatePetState_QAPick_ExpiresStalePending(t *testing.T) {
	c := newPetRenderCoordinator(t)
	c.pet.State = "idle"
	c.pet.Hunger = 100
	c.pet.Happiness = 100
	c.pet.QuestionCooldown = time.Time{}
	expiredOld := time.Now().Add(-1 * time.Hour) // Expires in the past
	c.pet.PendingQuestion = &daemon.PendingQuestion{
		ID:      "morning_or_night",
		Text:    "old question",
		Kind:    "choice",
		Choices: []string{"morning person", "night owl"},
		Expires: expiredOld,
	}
	// Seed answered history so the "first-time consent" path doesn't
	// dominate — we want the test to exercise the real expire->repick
	// transition with a normal bank question.
	c.pet.AnsweredQuestions = []daemon.AnsweredQuestion{
		{ID: "consent", Answer: "Yes, ask away", Timestamp: time.Now().Add(-30 * 24 * time.Hour)},
	}

	_ = c.UpdatePetState()

	// Either a fresh question came in OR the slot was cleared. Both
	// branches are acceptable; what we MUST NOT see is the stale
	// expired question still sitting on the state.
	if c.pet.PendingQuestion != nil {
		assert.True(t, c.pet.PendingQuestion.Expires.After(expiredOld),
			"if a new question was picked, its Expires must be after the cleared one")
	}
	// Belt-and-suspenders: re-issue the tick to confirm the expiry path
	// is idempotent (no panic if the slot was already cleared) and that
	// the next tick still produces something pickable.
	c.pet.PendingQuestion = nil
	_ = c.UpdatePetState()
	// With cooldown elapsed and the bank not exhausted, the second
	// tick must yield a non-nil PendingQuestion. Without this assertion
	// a regression where the tick silently stops picking would slip
	// through the previous assertion.
	assert.NotNil(t, c.pet.PendingQuestion,
		"a follow-up tick (no pending, cooldown elapsed) must produce a question")
}

// TestUpdatePetState_QAPick_SkipsWhenDead is a small safety test: the
// "bad headspace" gate should keep the cat quiet when it's dead. The
// dead pet shouldn't talk.
func TestUpdatePetState_QAPick_SkipsWhenDead(t *testing.T) {
	c := newPetRenderCoordinator(t)
	c.pet.IsDead = true
	c.pet.State = "dead"
	c.pet.QuestionCooldown = time.Time{}
	c.pet.PendingQuestion = nil

	_ = c.UpdatePetState()

	assert.Nil(t, c.pet.PendingQuestion,
		"UpdatePetState must not pick a question while the pet is dead")
}

// ─── Sanity guard on the helpers ───────────────────────────────────────────

// TestPhase2_DefaultQAConfig_NotZero pins down the shared assumption
// that defaultQAConfig (defined alongside the Phase 1 tests) yields a
// cadence that produces visible teasers in a default tabby install. If
// someone bumps the default to 0 the cat goes silent and nothing in
// this test file would catch it without this assertion.
func TestPhase2_DefaultQAConfig_NotZero(t *testing.T) {
	qa := defaultQAConfig()
	var _ config.PetWidgetQA = qa // assert type for readability
	assert.Greater(t, qa.TeaserEveryNThoughts, 0,
		"defaultQAConfig().TeaserEveryNThoughts must be > 0 — a 0 here silences the teaser")
}

// ─── Debug bar [q?] row + Q&A menu builder ─────────────────────────────────

// TestRenderDebugBar_ThreeLinesWithQuestionButton confirms the debug bar
// now renders 3 lines and the third contains the [q?] button. Catches the
// regression where the [q?] click target disappears (either because line 3
// stops rendering or because the literal "[q?]" token gets renamed).
func TestRenderDebugBar_ThreeLinesWithQuestionButton(t *testing.T) {
	c := newPetRenderCoordinator(t)
	lines := c.renderDebugBar(40)
	assert.Len(t, lines, 3, "debug bar must render 3 lines so the [q?] click handler has a row to map to DebugLine3")
	assert.Contains(t, lines[2], "[q?]",
		"line 3 must contain the [q?] token; the click handler greps for this literal")
}

// TestRenderPetWidget_DebugBar_LayoutAssignsDebugLine3 walks the full
// render path with debug_bar enabled and verifies the layout records a
// non-zero DebugLine3. handleDebugBarClick's dispatch gate uses
// (layout.DebugLine1 > 0 && clickY == layout.DebugLine3) — if DebugLine3
// reverts to 0 the [q?] click silently no-ops.
func TestRenderPetWidget_DebugBar_LayoutAssignsDebugLine3(t *testing.T) {
	c := newPetRenderCoordinator(t)
	c.config.Widgets.Pet.DebugBar = true

	_ = c.renderPetWidget(40, false)

	assert.Greater(t, c.petLayout.DebugLine1, 0, "DebugLine1 should be set when debug_bar is enabled")
	assert.Greater(t, c.petLayout.DebugLine2, c.petLayout.DebugLine1, "DebugLine2 must follow DebugLine1")
	assert.Greater(t, c.petLayout.DebugLine3, c.petLayout.DebugLine2, "DebugLine3 must follow DebugLine2")
}

// TestBuildQuestionMenuArgs_ChoiceQuestion pins the shape of the
// display-menu argv: title flag, position flags, Cancel row, separator,
// then one row per choice with sequential 1..9 hotkeys and a
// `run-shell -b "<exe> pet ask --quiet --answer '<choice>'"` command. The
// --quiet flag is load-bearing: tmux's run-shell dumps any stdout into
// the focused pane / view-mode buffer, which previously blanked the user's
// active shell with "[0]" until they pressed Esc.
func TestBuildQuestionMenuArgs_ChoiceQuestion(t *testing.T) {
	pending := &daemon.PendingQuestion{
		ID:      "editor_loyalty",
		Text:    "Which editor have you spent the most hours in this year?",
		Kind:    "choice",
		Choices: []string{"Neovim/Vim", "VS Code", "JetBrains", "Emacs", "Something else"},
	}
	args := buildQuestionMenuArgs(pending, "/usr/local/bin/tabby")

	// Header: display-menu -T <title> -x C -y C
	assert.Equal(t, "display-menu", args[0])
	assert.Equal(t, "-T", args[1])
	assert.Equal(t, pending.Text, args[2])
	assert.Equal(t, "-x", args[3])
	assert.Equal(t, "C", args[4])
	assert.Equal(t, "-y", args[5])
	assert.Equal(t, "C", args[6])

	// Cancel row + separator
	assert.Equal(t, "Cancel", args[7])
	assert.Equal(t, "q", args[8])
	assert.Equal(t, "", args[9])
	assert.Equal(t, "", args[10], "separator name must be empty")
	assert.Equal(t, "", args[11], "separator key must be empty")
	assert.Equal(t, "", args[12], "separator command must be empty")

	// Choice rows: (name, key, command) per choice
	choiceArgs := args[13:]
	assert.Len(t, choiceArgs, len(pending.Choices)*3,
		"each choice should contribute 3 args (name, key, command)")

	for i, choice := range pending.Choices {
		base := i * 3
		assert.Equal(t, choice, choiceArgs[base], "choice %d name", i)
		assert.Equal(t, fmt.Sprintf("%d", i+1), choiceArgs[base+1], "choice %d hotkey", i)
		// Command must include --quiet (silences confirmation stdout) and
		// the choice text inside single quotes.
		cmd := choiceArgs[base+2]
		assert.Contains(t, cmd, "run-shell -b", "command must use run-shell -b for background execution")
		assert.Contains(t, cmd, "--quiet", "command MUST pass --quiet so success stdout doesn't bleed into the focused pane")
		assert.Contains(t, cmd, "2>/dev/null", "command MUST redirect stderr — error messages still pop tmux's view-mode buffer otherwise")
		assert.Contains(t, cmd, "/usr/local/bin/tabby pet ask")
		assert.Contains(t, cmd, "--answer '"+choice+"'")
	}
}

// TestBuildQuestionMenuArgs_EscapesSingleQuotes guards against shell
// injection / parse failure when a choice contains a single quote (e.g.
// "don't know" or an LLM-generated choice with an apostrophe). The
// embedded choice text is wrapped in single quotes, so a literal ' must
// be escaped via the '\'' close-reopen trick.
func TestBuildQuestionMenuArgs_EscapesSingleQuotes(t *testing.T) {
	pending := &daemon.PendingQuestion{
		ID:      "test",
		Text:    "question?",
		Kind:    "choice",
		Choices: []string{"don't know", "I'm sure"},
	}
	args := buildQuestionMenuArgs(pending, "tabby")

	// Find the command args (third positional in each (name,key,cmd) trio
	// after the 13-arg header).
	for i, choice := range pending.Choices {
		cmd := args[13+i*3+2]
		// The choice should be wrapped in single quotes with '\'' for
		// the embedded apostrophe — never a bare apostrophe inside the
		// quoted region (which would terminate the quoting and break /bin/sh
		// parsing).
		want := strings.ReplaceAll(choice, "'", `'\''`)
		assert.Contains(t, cmd, "--answer '"+want+"'",
			"choice %q must be single-quote-escaped in the run-shell command", choice)
	}
}

// TestBuildQuestionMenuArgs_TitleTruncation pins the long-title trimming
// behaviour. display-menu titles render in tmux's status-line font and
// wrap awkwardly past ~60 chars, so the builder truncates to 57 chars
// plus "..." (60 total).
func TestBuildQuestionMenuArgs_TitleTruncation(t *testing.T) {
	longText := strings.Repeat("x", 100)
	pending := &daemon.PendingQuestion{
		ID:      "test",
		Text:    longText,
		Kind:    "choice",
		Choices: []string{"a", "b"},
	}
	args := buildQuestionMenuArgs(pending, "tabby")
	title := args[2]
	assert.Equal(t, 60, len(title), "long titles should be truncated to 60 chars")
	assert.True(t, strings.HasSuffix(title, "..."), "truncated titles should end with ellipsis")
}

// TestBuildQuestionMenuArgs_HotkeysCapAtNine confirms the 10th and later
// choices get an empty key (still clickable, just no number shortcut)
// rather than a two-digit hotkey that tmux can't bind cleanly.
func TestBuildQuestionMenuArgs_HotkeysCapAtNine(t *testing.T) {
	choices := make([]string, 12)
	for i := range choices {
		choices[i] = fmt.Sprintf("choice%d", i)
	}
	pending := &daemon.PendingQuestion{
		ID:      "test",
		Text:    "question?",
		Kind:    "choice",
		Choices: choices,
	}
	args := buildQuestionMenuArgs(pending, "tabby")

	// Choices 0-8 get keys "1".."9"; choices 9-11 get empty keys.
	for i := 0; i < 9; i++ {
		assert.Equal(t, fmt.Sprintf("%d", i+1), args[13+i*3+1],
			"choice %d should get hotkey %d", i, i+1)
	}
	for i := 9; i < 12; i++ {
		assert.Equal(t, "", args[13+i*3+1],
			"choice %d should have an empty hotkey (no double-digit binding)", i)
	}
}

// ─── forceQuestionTeaser ───────────────────────────────────────────────────

// TestForceQuestionTeaser_ZeroesAnimFrame_AndSetsPending pins the
// [q?] debug-button behaviour: clears any cooldown, populates
// PendingQuestion via PickQuestion if absent, and resets AnimFrame so the
// teaser cadence (block = AnimFrame/50, fires when block % N == 0) lands
// on the on-block immediately rather than mid-cycle.
func TestForceQuestionTeaser_ZeroesAnimFrame_AndSetsPending(t *testing.T) {
	c := newPetRenderCoordinator(t)
	// Past consent so PickQuestion picks from the seed bank rather than
	// returning the consent question.
	c.pet.AnsweredQuestions = []daemon.AnsweredQuestion{
		{ID: "consent", Answer: "Yes, ask away", Timestamp: petTimeNow.Add(-time.Hour)},
	}
	c.pet.AnimFrame = 9999
	c.pet.QuestionCooldown = petTimeNow.Add(48 * time.Hour) // would normally block PickQuestion
	c.pet.PendingQuestion = nil

	c.forceQuestionTeaser()

	assert.Equal(t, 0, c.pet.AnimFrame,
		"AnimFrame must be reset to 0 so block 0 % N == 0 → teaser fires immediately")
	assert.True(t, c.pet.QuestionCooldown.IsZero(),
		"QuestionCooldown must be cleared so PickQuestion isn't gated by a stale cooldown")
	assert.NotNil(t, c.pet.PendingQuestion,
		"PendingQuestion must be populated when PickQuestion can produce one")
}

// TestForceQuestionTeaser_PreservesExistingPending confirms the helper
// doesn't replace a PendingQuestion that's already set — the user might
// be mid-answer and we just want to nudge the teaser back on screen.
func TestForceQuestionTeaser_PreservesExistingPending(t *testing.T) {
	c := newPetRenderCoordinator(t)
	existing := pendingChoice(petTimeNow)
	c.pet.PendingQuestion = existing
	c.pet.AnimFrame = 500

	c.forceQuestionTeaser()

	assert.Equal(t, 0, c.pet.AnimFrame, "AnimFrame still gets zeroed")
	assert.Same(t, existing, c.pet.PendingQuestion,
		"PendingQuestion must not be replaced when one is already pending")
}
