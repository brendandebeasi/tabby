package daemon

// pet_qa_handler.go wires the `tabby pet` CLI's MsgPetQA socket requests
// into logic-author's PickQuestion / AnswerQuestion / ForgetAnswer /
// RecentAnswers functions in pet_qa.go.
//
// Why the indirection: logic-author's functions operate on the wire-format
// `daemon.PetState` so the same code can be reused by a future popup binary
// that round-trips pet.json directly. The coordinator's in-memory state is
// the local `petState` type with extra animation fields. This file is the
// adapter — it snapshots Q&A fields from c.pet into a daemon.PetState,
// invokes the pure function, copies the mutated Q&A fields back, then
// persists pet.json on the same goroutine. All exclusive access to c.pet
// happens under c.stateMu.
//
// Live state for animation (Pos, Hunger, Adventure, ...) is not touched by
// any Q&A op.

import (
	"fmt"
	"time"

	"github.com/brendandebeasi/tabby/pkg/config"
	"github.com/brendandebeasi/tabby/pkg/daemon"
)

// HandlePetQA is the synchronous entry point invoked by server.OnPetQA. It
// returns a populated *daemon.PetQAResponse for every request; nil is
// reserved for "no handler wired" so the server can distinguish handler
// absence from a deliberate empty reply.
func (c *Coordinator) HandlePetQA(req *daemon.PetQARequest) *daemon.PetQAResponse {
	if req == nil {
		return &daemon.PetQAResponse{OK: false, Error: "nil request"}
	}
	now := time.Now()
	qa := c.config.Widgets.Pet.QA

	switch req.Op {
	case daemon.PetQAOpGetPending:
		return c.handlePetQAGetPending(now, qa)
	case daemon.PetQAOpAnswer:
		return c.handlePetQAAnswer(req.Answer, now, qa)
	case daemon.PetQAOpListTraits:
		return c.handlePetQAListTraits()
	case daemon.PetQAOpForget:
		return c.handlePetQAForget(req.ID)
	}
	return &daemon.PetQAResponse{OK: false, Error: "unknown pet Q&A op: " + string(req.Op)}
}

// handlePetQAGetPending returns the currently pending question, generating
// one via PickQuestion if the cooldown has elapsed. Generating on read is
// load-bearing for Phase 1: until the coordinator tick is wired to call
// PickQuestion itself (a Phase 2 task), the CLI is the only thing nudging
// the cat into asking. Returning nil is the normal "stay quiet" reply.
func (c *Coordinator) handlePetQAGetPending(now time.Time, qa config.PetWidgetQA) *daemon.PetQAResponse {
	c.stateMu.Lock()
	wire := snapshotPetQAForWire(&c.pet)
	if wire.PendingQuestion == nil {
		// PickQuestion is pure; it inspects state + cooldown and either
		// returns a freshly built PendingQuestion or nil. We persist the
		// pick back onto c.pet so a follow-up GetPending sees the same
		// question and an Answer can match its ID.
		if picked := PickQuestion(&wire, qa, now); picked != nil {
			wire.PendingQuestion = picked
			applyPetQAFromWire(&c.pet, &wire)
			snap := c.pet
			c.stateMu.Unlock()
			savePetStateData(snap)
			return &daemon.PetQAResponse{OK: true, Pending: picked}
		}
	}
	c.stateMu.Unlock()
	return &daemon.PetQAResponse{OK: true, Pending: wire.PendingQuestion}
}

// handlePetQAAnswer validates that there is a pending question, then
// forwards to logic-author's AnswerQuestion. For choice-kind questions we
// require an exact-match answer up-front so the error message is sharper
// than "id mismatch"; free-text accepts any non-empty input. The CLI also
// pre-validates these, but the daemon enforces them as the authoritative
// boundary.
func (c *Coordinator) handlePetQAAnswer(answer string, now time.Time, qa config.PetWidgetQA) *daemon.PetQAResponse {
	if answer == "" {
		return &daemon.PetQAResponse{OK: false, Error: "answer is empty"}
	}
	// Cap raw answer length to bound pet.json growth. The 120-char
	// truncation downstream only applies in trait templates and LLM
	// prompts — the raw AnsweredQuestion.Answer is stored uncapped
	// without this. 4 KB is generous for a free-text reply and small
	// enough that a malicious local client can't bloat pet.json.
	const maxAnswerBytes = 4096
	if len(answer) > maxAnswerBytes {
		return &daemon.PetQAResponse{OK: false, Error: fmt.Sprintf("answer too long (%d bytes; max %d)", len(answer), maxAnswerBytes)}
	}
	c.stateMu.Lock()
	wire := snapshotPetQAForWire(&c.pet)
	if wire.PendingQuestion == nil {
		c.stateMu.Unlock()
		return &daemon.PetQAResponse{OK: false, Error: "no pending question to answer"}
	}
	pending := wire.PendingQuestion
	if pending.Kind == "choice" {
		matched := false
		for _, choice := range pending.Choices {
			if choice == answer {
				matched = true
				break
			}
		}
		if !matched {
			c.stateMu.Unlock()
			return &daemon.PetQAResponse{OK: false, Error: "answer does not match any choice"}
		}
	}
	pendingID := pending.ID
	// Snapshot trait sources BEFORE answering so we can detect which trait
	// (if any) AnswerQuestion produced for the response payload.
	priorTraitSources := make(map[string]bool, len(wire.Traits))
	for _, t := range wire.Traits {
		priorTraitSources[t.Source] = true
	}
	if err := AnswerQuestion(&wire, pendingID, answer, qa, now); err != nil {
		c.stateMu.Unlock()
		return &daemon.PetQAResponse{OK: false, Error: err.Error()}
	}
	// Find any new trait sourced from this question id.
	var newTrait *daemon.PersonalityTrait
	for i := range wire.Traits {
		t := wire.Traits[i]
		if t.Source == pendingID && !priorTraitSources[t.Source] {
			cp := t
			newTrait = &cp
			break
		}
	}
	applyPetQAFromWire(&c.pet, &wire)
	snap := c.pet
	c.stateMu.Unlock()
	savePetStateData(snap)
	return &daemon.PetQAResponse{OK: true, NewTrait: newTrait}
}

// handlePetQAListTraits returns a snapshot of the current trait list and
// the most-recent answers. Both slices are returned by value so the caller
// can format them without touching daemon state.
func (c *Coordinator) handlePetQAListTraits() *daemon.PetQAResponse {
	c.stateMu.RLock()
	wire := snapshotPetQAForWire(&c.pet)
	c.stateMu.RUnlock()
	traits := append([]daemon.PersonalityTrait(nil), wire.Traits...)
	recent := RecentAnswers(&wire, 0)
	return &daemon.PetQAResponse{OK: true, Traits: traits, RecentAnswers: recent}
}

// handlePetQAForget removes an answer by id (and any trait derived from
// it). Returns Removed=true on success; OK=false with an error string when
// the id doesn't match anything.
func (c *Coordinator) handlePetQAForget(id string) *daemon.PetQAResponse {
	if id == "" {
		return &daemon.PetQAResponse{OK: false, Error: "id is required"}
	}
	c.stateMu.Lock()
	wire := snapshotPetQAForWire(&c.pet)
	if err := ForgetAnswer(&wire, id); err != nil {
		c.stateMu.Unlock()
		return &daemon.PetQAResponse{OK: false, Error: err.Error()}
	}
	applyPetQAFromWire(&c.pet, &wire)
	snap := c.pet
	c.stateMu.Unlock()
	savePetStateData(snap)
	return &daemon.PetQAResponse{OK: true, Removed: true}
}

// snapshotPetQAForWire builds a daemon.PetState carrying just enough fields
// for logic-author's functions to make sensible decisions. The State and
// the Q&A fields are the inputs logic-author actually reads; everything
// else stays zero-valued because the caller will copy results back only
// onto the Q&A fields.
//
// Caller must hold c.stateMu (read lock is enough; the wire copy is local
// and never escapes the goroutine until we apply it back).
func snapshotPetQAForWire(p *petState) daemon.PetState {
	return daemon.PetState{
		State:              p.State,
		PendingQuestion:    p.PendingQuestion,
		AnsweredQuestions:  append([]daemon.AnsweredQuestion(nil), p.AnsweredQuestions...),
		Traits:             append([]daemon.PersonalityTrait(nil), p.Traits...),
		QuestionCooldown:   p.QuestionCooldown,
		LastQuestionShown:  p.LastQuestionShown,
		QAOptedOut:         p.QAOptedOut,
		QAFreeTextOptedOut: p.QAFreeTextOptedOut,
	}
}

// applyPetQAFromWire writes the Q&A fields from a mutated daemon.PetState
// back onto the in-memory petState. Only Q&A fields are written so the
// animation/lifecycle fields are never disturbed by a CLI op.
//
// Caller must hold c.stateMu (write lock).
func applyPetQAFromWire(p *petState, w *daemon.PetState) {
	p.PendingQuestion = w.PendingQuestion
	p.AnsweredQuestions = append([]daemon.AnsweredQuestion(nil), w.AnsweredQuestions...)
	p.Traits = append([]daemon.PersonalityTrait(nil), w.Traits...)
	p.QuestionCooldown = w.QuestionCooldown
	p.LastQuestionShown = w.LastQuestionShown
	p.QAOptedOut = w.QAOptedOut
	p.QAFreeTextOptedOut = w.QAFreeTextOptedOut
}

// applyLLMDistillation is the callback passed to
// RunLLMDistillationBackground. It acquires c.stateMu, lifts the live
// petState into a wire snapshot, runs the supplied mutator against that
// snapshot, copies the mutation back, then persists pet.json on the same
// goroutine. This isolates the background goroutine from c.pet — it only
// ever sees the wire-format intermediate.
//
// Defensive: a nil mutate is a no-op so the LLM goroutine never panics
// the daemon.
func (c *Coordinator) applyLLMDistillation(mutate func(p *daemon.PetState)) {
	if mutate == nil {
		return
	}
	c.stateMu.Lock()
	wire := snapshotPetQAForWire(&c.pet)
	mutate(&wire)
	applyPetQAFromWire(&c.pet, &wire)
	snap := c.pet
	c.stateMu.Unlock()
	savePetStateData(snap)
}
