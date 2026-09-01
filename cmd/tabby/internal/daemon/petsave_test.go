package daemon

import (
	"encoding/json"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brendandebeasi/tabby/pkg/daemon"
)

// petSaveHarness points the state dir at a temp dir, allows writes, and puts
// the throttle back to a clean slate. Returns the pet.json path.
func petSaveHarness(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	prev := atomic.LoadInt32(&petWriteAllowed)
	atomic.StoreInt32(&petWriteAllowed, 1)

	reset := func() {
		petSaveMu.Lock()
		defer petSaveMu.Unlock()
		if petSaveTimer != nil {
			petSaveTimer.Stop()
			petSaveTimer = nil
		}
		petSavePending = nil
		petSaveLastAt = time.Time{}
	}
	reset()

	// paths resolves the state dir itself, so the tests share one pet.json.
	// Start every case from no file at all.
	path := petStatePath()
	os.Remove(path)

	t.Cleanup(func() {
		reset()
		atomic.StoreInt32(&petWriteAllowed, prev)
	})
	return path
}

func readPet(t *testing.T, path string) petState {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var pet petState
	if err := json.Unmarshal(data, &pet); err != nil {
		t.Fatalf("unmarshalling %s: %v", path, err)
	}
	return pet
}

// A feed or a click must reach disk at once, not a second later.
func TestSavePetStateDataWritesTheFirstSaveImmediately(t *testing.T) {
	path := petSaveHarness(t)
	savePetStateData(petState{TotalFeedings: 7})
	if got := readPet(t, path).TotalFeedings; got != 7 {
		t.Fatalf("TotalFeedings on disk = %d, want 7", got)
	}
}

// The animation ticks several times a second and each tick used to marshal
// and rewrite the whole file. Everything behind the leading write has to
// collapse into one trailing write.
func TestSavePetStateDataCoalescesABurst(t *testing.T) {
	path := petSaveHarness(t)
	savePetStateData(petState{TotalFeedings: 1})
	for i := 2; i <= 50; i++ {
		savePetStateData(petState{TotalFeedings: i})
	}
	if got := readPet(t, path).TotalFeedings; got != 1 {
		t.Fatalf("TotalFeedings on disk = %d during the burst, want the leading write's 1", got)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if readPet(t, path).TotalFeedings == 50 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the burst's last value never reached disk; TotalFeedings = %d", readPet(t, path).TotalFeedings)
}

// The trailing write happens up to a second after the snapshot was taken, and
// the live pet keeps mutating in the meantime, so the parked copy must own
// every slice and map it holds.
func TestClonePetStateOwnsItsReferences(t *testing.T) {
	pet := petState{
		PoopPositions:     []int{1, 2},
		FloatingItems:     []floatingItem{{}},
		Presents:          []petPresent{{}},
		AnsweredQuestions: []daemon.AnsweredQuestion{{}},
		Traits:            []daemon.PersonalityTrait{{}},
		PendingQuestion:   &daemon.PendingQuestion{ID: "q", Choices: []string{"a"}},
	}
	pet.Stats.TimeByStateSec = map[string]int64{"idle": 3}
	pet.Adventure.Wildlife = &wildlifeEncounter{Type: "mouse"}

	clone := clonePetState(pet)

	pet.PoopPositions[0] = 99
	pet.Stats.TimeByStateSec["idle"] = 99
	pet.PendingQuestion.Choices[0] = "z"
	pet.PendingQuestion.ID = "mutated"
	pet.Adventure.Wildlife.Type = "mutated"

	if clone.PoopPositions[0] != 1 {
		t.Errorf("PoopPositions aliased the live pet: %v", clone.PoopPositions)
	}
	if clone.Stats.TimeByStateSec["idle"] != 3 {
		t.Errorf("TimeByStateSec aliased the live pet: %v", clone.Stats.TimeByStateSec)
	}
	if clone.PendingQuestion.Choices[0] != "a" || clone.PendingQuestion.ID != "q" {
		t.Errorf("PendingQuestion aliased the live pet: %+v", clone.PendingQuestion)
	}
	if clone.Adventure.Wildlife.Type != "mouse" {
		t.Errorf("Adventure.Wildlife aliased the live pet: %+v", clone.Adventure.Wildlife)
	}
}

// A daemon that does not own the pet must not touch the file at all, throttle
// or no throttle.
func TestSavePetStateDataStaysOutWhenItDoesNotOwnThePet(t *testing.T) {
	path := petSaveHarness(t)
	atomic.StoreInt32(&petWriteAllowed, 0)
	savePetStateData(petState{TotalFeedings: 7})
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("a non-owning daemon wrote %s (stat err %v)", path, err)
	}
}
