// Package petqapopup — TUI smoke tests for the pet Q&A popup model.
//
// These tests stay inside the package so they can drive `model` directly
// (it's unexported). They DON'T spin up a real bubbletea Program or a
// real daemon socket — instead they feed messages into Update() and
// assert on the next-phase / cmd transitions. This is the same approach
// the bubbletea project itself recommends for unit-testing TUIs.
//
// Coverage:
//
//   - initial state is phaseLoading and Init returns the loader cmd
//   - pendingLoadedMsg(nil) routes to phaseNoPending + tea.Quit
//   - pendingLoadedMsg(err) routes to phaseError + exitCode=1
//   - choice navigation: up/down/j/k clamp at bounds; Enter submits the
//     highlighted choice; "1".."9" jump shortcuts move selection
//   - free_text: typed runes accumulate; Enter submits; backspace pops
//     a rune (UTF-8 safe); Esc cancels with exit 0
//   - Esc always exits without submitting (exit 0)
//   - Ctrl-C is treated as Esc (exit 0)
//   - answerSubmittedMsg(error) routes to phaseError + exit 1
//   - answerSubmittedMsg(ok) routes to phaseConfirming and returns a
//     timer cmd
//   - the prompt View renders the question text for both kinds
//
// What is NOT covered here (and would require a real daemon socket /
// tea.Program harness):
//
//   - The "submitted answer is actually sent over the wire" path —
//     submitAnswerCmd dials a unix socket, which would need a fake
//     listener. Run()'s synchronous probe path is also not exercised.
package petqapopup

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"

	"github.com/brendandebeasi/tabby/pkg/daemon"
)

// ─── helpers ───────────────────────────────────────────────────────────────

// promptingChoice returns a model already past the loading phase with a
// choice-kind question. Most key tests start from here.
func promptingChoice() model {
	m := initialModel("test-session")
	m.pending = &daemon.PendingQuestion{
		ID:      "morning_or_night",
		Text:    "are you a morning person or a night owl?",
		Kind:    "choice",
		Choices: []string{"morning person", "night owl", "depends"},
	}
	m.phase = phasePrompting
	return m
}

// promptingFreeText returns a model already past loading with a
// free-text question. The input field starts empty.
func promptingFreeText() model {
	m := initialModel("test-session")
	m.pending = &daemon.PendingQuestion{
		ID:   "what_shipped_today",
		Text: "what did you ship today?",
		Kind: "free_text",
	}
	m.phase = phasePrompting
	return m
}

// keyMsgFor builds a tea.KeyMsg approximating what bubbletea would emit
// for a named key. The named keys we care about all have well-known
// tea.KeyType constants; printable runes use tea.KeyRunes with Runes set.
func keyMsgFor(name string) tea.KeyMsg {
	switch name {
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	}
	// Single rune fallback (e.g. "k", "j", "1", "a").
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(name)}
}

// updateKey drives the model Update with a synthesised key event and
// returns the new model + the emitted cmd.
func updateKey(m model, key string) (model, tea.Cmd) {
	next, cmd := m.Update(keyMsgFor(key))
	return next.(model), cmd
}

// updateMsg drives the model Update with an arbitrary message.
func updateMsg(m model, msg tea.Msg) (model, tea.Cmd) {
	next, cmd := m.Update(msg)
	return next.(model), cmd
}

// runeInput synthesises a printable-rune KeyMsg. bubbletea's free-text
// handler reads msg.Runes, so we have to populate that explicitly rather
// than relying on Type alone.
func runeInput(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// isQuitCmd is true when cmd, when executed, returns tea.QuitMsg{}. We
// can't compare cmds by identity (they're closures), but we can call
// them and inspect the resulting message. cmds returned by tea.Quit are
// safe to call from any goroutine.
func isQuitCmd(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	msg := cmd()
	_, ok := msg.(tea.QuitMsg)
	return ok
}

// ─── construction / initial state ──────────────────────────────────────────

// TestPetQAPopup_InitialModel_IsLoading documents the starting state and
// pins the phase machine entry point. If someone later changes the
// initial phase, every key handler test depends on this assumption so
// the failure should originate here.
func TestPetQAPopup_InitialModel_IsLoading(t *testing.T) {
	m := initialModel("test-session")
	assert.Equal(t, phaseLoading, m.phase)
	assert.Equal(t, "test-session", m.sessionID)
	assert.Equal(t, 0, m.exitCode, "exit code should start at 0")
	assert.Nil(t, m.pending, "no pending question until loader resolves")
	assert.Equal(t, 0, m.selected, "selection starts at the first choice")
	assert.Empty(t, m.input, "free-text input starts empty")
}

// TestPetQAPopup_Init_ReturnsLoaderCmd verifies that Init() returns a
// non-nil cmd when the model is in phaseLoading. The cmd would dial the
// daemon; we don't execute it (that would fail without a daemon), only
// confirm one was scheduled.
func TestPetQAPopup_Init_ReturnsLoaderCmd(t *testing.T) {
	m := initialModel("test-session")
	cmd := m.Init()
	assert.NotNil(t, cmd, "Init from phaseLoading should schedule the loader cmd")
}

// TestPetQAPopup_Init_NoCmdAfterPreload covers Run()'s pre-loaded path:
// when the model is seeded directly into phasePrompting (as Run() does
// after its synchronous daemon probe), Init() must NOT re-fire the
// loader or we'd hit the daemon twice on every popup open.
func TestPetQAPopup_Init_NoCmdAfterPreload(t *testing.T) {
	m := promptingChoice()
	cmd := m.Init()
	assert.Nil(t, cmd, "Init from phasePrompting should not schedule a second loader cmd")
}

// ─── pendingLoadedMsg routing ──────────────────────────────────────────────

// TestPetQAPopup_PendingLoaded_NilEntersNoPending pins the
// "no question to ask" branch: model goes to phaseNoPending and emits a
// tea.Quit cmd so the popup exits cleanly. Per popup-author's note,
// this branch is exercised most often from Run()'s synchronous probe
// path; the Update-based branch is the fallback.
func TestPetQAPopup_PendingLoaded_NilEntersNoPending(t *testing.T) {
	m := initialModel("test-session")
	m, cmd := updateMsg(m, pendingLoadedMsg{pending: nil})
	assert.Equal(t, phaseNoPending, m.phase)
	assert.True(t, isQuitCmd(cmd), "no-pending branch must schedule tea.Quit")
}

// TestPetQAPopup_PendingLoaded_ErrorEntersErrorPhase pins the loader-
// error branch. Exit code propagates to 1 so the outer Run() can return
// a non-zero exit and the user sees the error frame.
func TestPetQAPopup_PendingLoaded_ErrorEntersErrorPhase(t *testing.T) {
	m := initialModel("test-session")
	m, cmd := updateMsg(m, pendingLoadedMsg{err: errors.New("daemon unreachable")})
	assert.Equal(t, phaseError, m.phase)
	assert.Equal(t, 1, m.exitCode, "loader errors should set exit code 1")
	assert.Contains(t, m.errText, "daemon unreachable")
	assert.Nil(t, cmd, "error path should not schedule any further cmd")
}

// TestPetQAPopup_PendingLoaded_SuccessEntersPrompting covers the happy
// loader path: model receives a question and transitions to
// phasePrompting without quitting.
func TestPetQAPopup_PendingLoaded_SuccessEntersPrompting(t *testing.T) {
	m := initialModel("test-session")
	pq := &daemon.PendingQuestion{
		ID:      "morning_or_night",
		Text:    "morning or night?",
		Kind:    "choice",
		Choices: []string{"morning", "night"},
	}
	m, cmd := updateMsg(m, pendingLoadedMsg{pending: pq})
	assert.Equal(t, phasePrompting, m.phase)
	assert.NotNil(t, m.pending)
	assert.Equal(t, "morning_or_night", m.pending.ID)
	assert.Nil(t, cmd, "transition to prompting should not schedule any cmd")
}

// ─── choice navigation ─────────────────────────────────────────────────────

// TestPetQAPopup_Choice_DownArrowMovesSelection walks the highlight down
// through the choices and asserts the index follows. The clamp at the
// end of the list is also covered: pressing "down" past the last entry
// should NOT wrap or go out of range.
func TestPetQAPopup_Choice_DownArrowMovesSelection(t *testing.T) {
	m := promptingChoice() // 3 choices
	assert.Equal(t, 0, m.selected)

	m, _ = updateKey(m, "down")
	assert.Equal(t, 1, m.selected, "down arrow should advance selection")

	m, _ = updateKey(m, "down")
	assert.Equal(t, 2, m.selected)

	// At the last entry — should clamp, not wrap.
	m, _ = updateKey(m, "down")
	assert.Equal(t, 2, m.selected, "down arrow at last choice must clamp")
}

// TestPetQAPopup_Choice_UpArrowMovesSelection mirrors the down test.
// Clamps at 0 — does NOT wrap to the last entry.
func TestPetQAPopup_Choice_UpArrowMovesSelection(t *testing.T) {
	m := promptingChoice()
	m.selected = 2 // start at the end

	m, _ = updateKey(m, "up")
	assert.Equal(t, 1, m.selected)

	m, _ = updateKey(m, "up")
	assert.Equal(t, 0, m.selected)

	m, _ = updateKey(m, "up")
	assert.Equal(t, 0, m.selected, "up arrow at first choice must clamp")
}

// TestPetQAPopup_Choice_JKMoveSelection covers vim-style nav.
func TestPetQAPopup_Choice_JKMoveSelection(t *testing.T) {
	m := promptingChoice()

	m, _ = updateKey(m, "j")
	assert.Equal(t, 1, m.selected, "'j' should move down")

	m, _ = updateKey(m, "k")
	assert.Equal(t, 0, m.selected, "'k' should move up")
}

// TestPetQAPopup_Choice_NumberShortcutJumps verifies that pressing a
// digit "1".."9" jumps to that 1-indexed choice (when in range). This
// also doubles as a regression guard that out-of-range digits don't
// crash or set an invalid selection.
func TestPetQAPopup_Choice_NumberShortcutJumps(t *testing.T) {
	m := promptingChoice() // 3 choices

	m, _ = updateKey(m, "2")
	assert.Equal(t, 1, m.selected, "'2' should jump to index 1")

	m, _ = updateKey(m, "3")
	assert.Equal(t, 2, m.selected, "'3' should jump to last choice")

	// Out-of-range digit — selection should be unchanged.
	m, _ = updateKey(m, "9")
	assert.Equal(t, 2, m.selected, "'9' (out of range) should leave selection unchanged")
}

// TestPetQAPopup_Choice_EnterSubmits verifies Enter transitions to
// phaseSubmitting and returns a non-nil cmd (which would dispatch
// submitAnswerCmd against the daemon — we don't execute it, only assert
// it's scheduled). The user is now committed to the highlighted choice.
func TestPetQAPopup_Choice_EnterSubmits(t *testing.T) {
	m := promptingChoice()
	m.selected = 1 // "night owl"

	m, cmd := updateKey(m, "enter")
	assert.Equal(t, phaseSubmitting, m.phase, "Enter should transition to phaseSubmitting")
	assert.NotNil(t, cmd, "Enter on a choice should schedule submitAnswerCmd")
}

// TestPetQAPopup_Choice_EscCancelsExitsZero verifies Esc terminates the
// popup without submitting and propagates exit 0 (user declined to
// answer; no error condition).
func TestPetQAPopup_Choice_EscCancelsExitsZero(t *testing.T) {
	m := promptingChoice()
	m, cmd := updateKey(m, "esc")
	assert.Equal(t, 0, m.exitCode, "Esc cancel must exit 0")
	assert.True(t, isQuitCmd(cmd), "Esc must schedule tea.Quit")
}

// TestPetQAPopup_Choice_CtrlCCancelsExitsZero pins the tmux ergonomic
// note: Ctrl-C behaves like Esc inside the popup (exit 0, not 130).
func TestPetQAPopup_Choice_CtrlCCancelsExitsZero(t *testing.T) {
	m := promptingChoice()
	m, cmd := updateKey(m, "ctrl+c")
	assert.Equal(t, 0, m.exitCode, "Ctrl-C must exit 0 just like Esc")
	assert.True(t, isQuitCmd(cmd), "Ctrl-C must schedule tea.Quit")
}

// ─── free-text input ───────────────────────────────────────────────────────

// TestPetQAPopup_FreeText_TypingAccumulates verifies printable runes
// land in the input buffer in order. Drives a multi-character word so a
// regression that dropped or reordered runes would be visible.
func TestPetQAPopup_FreeText_TypingAccumulates(t *testing.T) {
	m := promptingFreeText()

	// Send characters one by one — the handler reads msg.Runes per call.
	for _, ch := range "tuna" {
		m, _ = updateMsg(m, runeInput(string(ch)))
	}
	assert.Equal(t, "tuna", m.input, "typed characters should accumulate in order")
}

// TestPetQAPopup_FreeText_BackspaceRemovesLastRune verifies the runes-
// not-bytes guarantee called out in the comment on handleFreeTextKey:
// backspace must remove a full rune, not just a byte.
func TestPetQAPopup_FreeText_BackspaceRemovesLastRune(t *testing.T) {
	m := promptingFreeText()
	m.input = "héllo" // 'é' is multi-byte in UTF-8

	m, _ = updateKey(m, "backspace")
	assert.Equal(t, "héll", m.input)

	m, _ = updateKey(m, "backspace")
	m, _ = updateKey(m, "backspace")
	m, _ = updateKey(m, "backspace")
	assert.Equal(t, "h", m.input)

	m, _ = updateKey(m, "backspace")
	assert.Equal(t, "", m.input)

	// Backspace on empty input must not panic or under-flow.
	m, _ = updateKey(m, "backspace")
	assert.Equal(t, "", m.input, "backspace on empty input is a no-op")
}

// TestPetQAPopup_FreeText_EnterSubmitsNonEmpty verifies Enter only
// commits when there's actually input. Blank submissions are rejected
// at the popup layer (the daemon also rejects them) so the user sees
// a hint rather than a round-trip error.
func TestPetQAPopup_FreeText_EnterSubmitsNonEmpty(t *testing.T) {
	m := promptingFreeText()

	// Blank: Enter should NOT advance the phase.
	m, cmd := updateKey(m, "enter")
	assert.Equal(t, phasePrompting, m.phase, "blank input must not submit")
	assert.Nil(t, cmd)

	// Type and submit.
	m.input = "fixed the sidebar drag bug"
	m, cmd = updateKey(m, "enter")
	assert.Equal(t, phaseSubmitting, m.phase, "non-empty input + Enter must submit")
	assert.NotNil(t, cmd, "non-empty submission must schedule submitAnswerCmd")
}

// TestPetQAPopup_FreeText_EscCancelsExitsZero — same contract as choice.
func TestPetQAPopup_FreeText_EscCancelsExitsZero(t *testing.T) {
	m := promptingFreeText()
	m.input = "draft answer about to be cancelled"

	m, cmd := updateKey(m, "esc")
	assert.Equal(t, 0, m.exitCode, "Esc cancel from free-text must exit 0")
	assert.True(t, isQuitCmd(cmd), "Esc must schedule tea.Quit")
}

// ─── post-submit transitions ───────────────────────────────────────────────

// TestPetQAPopup_AnswerSubmitted_OKEntersConfirming verifies the happy
// post-submit transition: model receives a daemon OK response and goes
// to phaseConfirming with a delayed Quit cmd. The trait (if any) is
// captured so renderConfirmation can show it.
func TestPetQAPopup_AnswerSubmitted_OKEntersConfirming(t *testing.T) {
	m := promptingChoice()
	m.phase = phaseSubmitting
	trait := &daemon.PersonalityTrait{Text: "user is a night owl", Source: "morning_or_night", Confidence: 1.0}

	m, cmd := updateMsg(m, answerSubmittedMsg{resp: &daemon.PetQAResponse{OK: true, NewTrait: trait}})
	assert.Equal(t, phaseConfirming, m.phase, "OK response should enter confirming")
	assert.NotNil(t, m.newTrait, "trait from the response should be captured for display")
	assert.NotNil(t, cmd, "confirming should schedule a delayed exit tick")
}

// TestPetQAPopup_AnswerSubmitted_ErrorEntersErrorPhase verifies the
// failed-submit transition routes to phaseError with exit code 1 so
// callers see a clear failure.
func TestPetQAPopup_AnswerSubmitted_ErrorEntersErrorPhase(t *testing.T) {
	m := promptingChoice()
	m.phase = phaseSubmitting

	m, _ = updateMsg(m, answerSubmittedMsg{err: errors.New("network down")})
	assert.Equal(t, phaseError, m.phase)
	assert.Equal(t, 1, m.exitCode)
	assert.Contains(t, m.errText, "network down")
}

// TestPetQAPopup_AnswerSubmitted_DaemonRefuses verifies the "OK=false"
// branch — the daemon validated and refused (e.g. answer doesn't match
// any choice). Same exit-1 contract as a transport error.
func TestPetQAPopup_AnswerSubmitted_DaemonRefuses(t *testing.T) {
	m := promptingChoice()
	m.phase = phaseSubmitting
	m, _ = updateMsg(m, answerSubmittedMsg{resp: &daemon.PetQAResponse{OK: false, Error: "no pending question to answer"}})
	assert.Equal(t, phaseError, m.phase)
	assert.Equal(t, 1, m.exitCode)
	assert.Contains(t, m.errText, "no pending question")
}

// TestPetQAPopup_ExitTickMsg_Quits verifies the confirmation timer's
// follow-up tick triggers a clean exit.
func TestPetQAPopup_ExitTickMsg_Quits(t *testing.T) {
	m := promptingChoice()
	m.phase = phaseConfirming
	_, cmd := updateMsg(m, exitTickMsg{})
	assert.True(t, isQuitCmd(cmd), "exitTickMsg must schedule tea.Quit")
}

// TestPetQAPopup_Confirming_EnterShortCircuits verifies the user can
// skip the 600ms dwell by pressing Enter — useful on slow tmux popups
// where you don't want to wait.
func TestPetQAPopup_Confirming_EnterShortCircuits(t *testing.T) {
	m := promptingChoice()
	m.phase = phaseConfirming
	_, cmd := updateKey(m, "enter")
	assert.True(t, isQuitCmd(cmd), "Enter during confirmation should skip the dwell and quit")
}

// TestPetQAPopup_Confirming_EscShortCircuits — same, but with Esc.
func TestPetQAPopup_Confirming_EscShortCircuits(t *testing.T) {
	m := promptingChoice()
	m.phase = phaseConfirming
	_, cmd := updateKey(m, "esc")
	assert.True(t, isQuitCmd(cmd), "Esc during confirmation should skip the dwell and quit")
}

// TestPetQAPopup_Submitting_IgnoresKeys verifies the "no double-submit"
// guard: while the answer is in flight, keypresses must be ignored.
func TestPetQAPopup_Submitting_IgnoresKeys(t *testing.T) {
	m := promptingChoice()
	m.phase = phaseSubmitting

	m, cmd := updateKey(m, "enter")
	assert.Equal(t, phaseSubmitting, m.phase, "Enter while submitting must not change phase")
	assert.Nil(t, cmd, "Enter while submitting must not schedule another cmd")

	m, cmd = updateKey(m, "esc")
	assert.Equal(t, phaseSubmitting, m.phase, "Esc while submitting must also be ignored (no surprise cancel)")
	assert.Nil(t, cmd)
}

// TestPetQAPopup_ErrorPhase_AnyKeyQuits documents the error-phase
// dismissal contract: ANY key teardowns the TUI. We test two different
// keys to make sure neither special-cases out of the contract.
func TestPetQAPopup_ErrorPhase_AnyKeyQuits(t *testing.T) {
	m := promptingChoice()
	m.phase = phaseError
	m.errText = "boom"

	_, cmd := updateKey(m, "enter")
	assert.True(t, isQuitCmd(cmd), "Enter from error phase should quit")

	m.phase = phaseError
	_, cmd = updateKey(m, "x")
	assert.True(t, isQuitCmd(cmd), "any random key from error phase should quit")
}

// ─── view smoke ────────────────────────────────────────────────────────────

// TestPetQAPopup_View_PromptShowsQuestionText is a light snapshot check
// — when the model is prompting, the rendered View MUST contain the
// question text so the user knows what's being asked. lipgloss adds
// styling escape sequences around the text so we use Contains rather
// than equality.
func TestPetQAPopup_View_PromptShowsQuestionText(t *testing.T) {
	m := promptingChoice()
	out := m.View()
	assert.Contains(t, out, "are you a morning person",
		"prompting view must render the question text")
	// All three numbered choices should be present.
	for i, choice := range m.pending.Choices {
		assert.Contains(t, out, choice, "choice %d should appear in view", i)
	}
}

// TestPetQAPopup_View_FreeTextShowsHint verifies the free-text prompt
// shows the input hint so users know what to do.
func TestPetQAPopup_View_FreeTextShowsHint(t *testing.T) {
	m := promptingFreeText()
	m.input = "hello world"
	out := m.View()
	assert.Contains(t, out, "what did you ship today?", "question text must be shown")
	assert.True(t,
		strings.Contains(out, "type your answer") || strings.Contains(out, "Enter"),
		"free-text view should show some kind of input hint")
}

// TestPetQAPopup_View_NoPendingIsEmpty pins the design choice from
// popup-author's note: phaseNoPending renders empty because Run()
// prints the friendly message on stdout AFTER the TUI tears down.
// A non-empty View here would briefly flash on screen before exit.
func TestPetQAPopup_View_NoPendingIsEmpty(t *testing.T) {
	m := initialModel("test-session")
	m.phase = phaseNoPending
	out := m.View()
	assert.Empty(t, out, "no-pending phase must render an empty view")
}

// TestPetQAPopup_View_ErrorShowsMessage verifies the error frame
// actually surfaces the error text to the user.
func TestPetQAPopup_View_ErrorShowsMessage(t *testing.T) {
	m := initialModel("test-session")
	m.phase = phaseError
	m.errText = "daemon down"
	out := m.View()
	assert.Contains(t, out, "daemon down", "error view must include the error message")
}

// ─── timer cmd ─────────────────────────────────────────────────────────────

// TestPetQAPopup_ConfirmDelay_BetweenZeroAndTwoSeconds is a small sanity
// check that the dwell delay is finite and reasonable. A regression
// that bumped this to e.g. 10 minutes would block users staring at the
// success screen.
func TestPetQAPopup_ConfirmDelay_BetweenZeroAndTwoSeconds(t *testing.T) {
	assert.Greater(t, confirmDelay, time.Duration(0),
		"confirmDelay must be positive — 0 would close before the user can read the trait")
	assert.Less(t, confirmDelay, 2*time.Second,
		"confirmDelay must be sub-2s — anything longer feels broken")
}
