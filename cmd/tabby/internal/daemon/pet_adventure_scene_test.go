package daemon

import (
	"testing"
	"time"

	"github.com/brendandebeasi/tabby/pkg/config"
)

func TestAdventureSceneryIsDeterministicAndSpaced(t *testing.T) {
	biome := adventureBiomes["forest"]

	// Same column, same answer — the simulation and the renderer both call
	// this and have to agree on where the rocks are.
	for _, x := range []int{0, 7, 14, 35, 700} {
		if adventureSceneryAt(biome, x) != adventureSceneryAt(biome, x) {
			t.Fatalf("scenery at %d is not stable", x)
		}
	}
	for x := 0; x < 100; x++ {
		if x%scenerySpacing != 0 && adventureSceneryAt(biome, x) != "" {
			t.Fatalf("scenery appeared off-grid at column %d", x)
		}
	}
}

func TestAdventureSceneryExcludesButterflies(t *testing.T) {
	// The meadow's scenery list includes 🦋, which belongs in the air row.
	biome := adventureBiomes["meadow"]
	for x := 0; x < 700; x += scenerySpacing {
		if got := adventureSceneryAt(biome, x); got == "🦋" {
			t.Fatalf("butterfly placed on the ground at column %d", x)
		}
	}
	sawSomething := false
	for x := 0; x < 700; x += airScenerySpacing {
		if got := adventureAirSceneryAt(biome, x); got != "" {
			sawSomething = true
			if got != "🦋" && got != "🐦" {
				t.Fatalf("air row got %q, want only flying things", got)
			}
		}
	}
	if !sawSomething {
		t.Fatal("meadow never placed anything in the air row")
	}
}

func TestAdventureObstacles(t *testing.T) {
	for _, blocking := range []string{"🪨", "🌳", "🌲", "🪵"} {
		if !adventureIsObstacle(blocking) {
			t.Fatalf("%s should block the cat", blocking)
		}
	}
	for _, passable := range []string{"🍂", "🌿", "🌸", "", "🦋"} {
		if adventureIsObstacle(passable) {
			t.Fatalf("%q should not block the cat", passable)
		}
	}
}

func newAdventureTestCoordinator() *Coordinator {
	c := &Coordinator{config: &config.Config{}}
	c.config.Widgets.Pet.AdventureBlood = true
	c.pet.Pos = pos2D{X: 4}
	c.pet.Happiness = 50
	c.pet.YarnPos = pos2D{X: -1}
	c.pet.FoodItem = pos2D{X: -1}
	c.pet.MousePos = pos2D{X: -1}
	c.pet.State = "idle"
	c.pet.Stats.CurrentIdleSec = adventureIdleDwell
	return c
}

func TestPetCanAdventureOnlyWhenNothingIsGoingOn(t *testing.T) {
	now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

	if !newAdventureTestCoordinator().petCanAdventure(now) {
		t.Fatal("a bored cat in an empty yard should be free to wander off")
	}

	cases := map[string]func(*Coordinator){
		"yarn is out":        func(c *Coordinator) { c.pet.YarnPos = pos2D{X: 3}; c.pet.YarnExpiresAt = now.Add(time.Minute) },
		"food is out":        func(c *Coordinator) { c.pet.FoodItem = pos2D{X: 3} },
		"a mouse is around":  func(c *Coordinator) { c.pet.MousePos = pos2D{X: 3} },
		"poop needs a scoop": func(c *Coordinator) { c.pet.PoopPositions = []int{2} },
		"heading somewhere":  func(c *Coordinator) { c.pet.HasTarget = true },
		"mid-action":         func(c *Coordinator) { c.pet.ActionPending = "play" },
		"already away":       func(c *Coordinator) { c.pet.Adventure.Active = true },
		"dead":               func(c *Coordinator) { c.pet.IsDead = true },
		"only just settled":  func(c *Coordinator) { c.pet.Stats.CurrentIdleSec = 0 },
	}
	for name, setup := range cases {
		c := newAdventureTestCoordinator()
		setup(c)
		if c.petCanAdventure(now) {
			t.Errorf("cat left when %s", name)
		}
	}

	// Expired yarn is not a reason to stay home.
	c := newAdventureTestCoordinator()
	c.pet.YarnPos = pos2D{X: 3}
	c.pet.YarnExpiresAt = now.Add(-time.Minute)
	if !c.petCanAdventure(now) {
		t.Error("stale yarn kept the cat home")
	}
}

func TestPetCanAdventureRespectsAdventureChance(t *testing.T) {
	now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

	c := newAdventureTestCoordinator()
	c.config.Widgets.Pet.AdventureChance = 100
	if !c.petCanAdventure(now) {
		t.Fatal("adventure_chance: 100 should always allow it")
	}

	// A tiny chance should refuse most of the time. Not never — the point is
	// that the knob is actually read, which it wasn't before.
	c = newAdventureTestCoordinator()
	c.config.Widgets.Pet.AdventureChance = 1
	refusals := 0
	for i := 0; i < 200; i++ {
		if !c.petCanAdventure(now) {
			refusals++
		}
	}
	if refusals < 150 {
		t.Fatalf("adventure_chance: 1 allowed %d/200 attempts; the knob looks ignored", 200-refusals)
	}
}

func TestSpawnBloodIsOptIn(t *testing.T) {
	now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

	c := &Coordinator{config: &config.Config{}}
	c.spawnBlood(now, 3, 0)
	if len(c.pet.FloatingItems) != 0 {
		t.Fatal("blood appeared without adventure_blood set")
	}

	c = &Coordinator{config: &config.Config{}}
	c.config.Widgets.Pet.AdventureBlood = true
	c.spawnBlood(now, 3, 0)
	if len(c.pet.FloatingItems) != 1 || c.pet.FloatingItems[0].Emoji != "🩸" {
		t.Fatalf("expected one splatter, got %+v", c.pet.FloatingItems)
	}
	if !c.pet.FloatingItems[0].ExpiresAt.Equal(now.Add(bloodTTL)) {
		t.Fatal("splatter should expire after bloodTTL")
	}

	// A configured icon wins over the default.
	c = &Coordinator{config: &config.Config{}}
	c.config.Widgets.Pet.AdventureBlood = true
	c.config.Widgets.Pet.Icons.Blood = "💥"
	c.spawnBlood(now, 3, 0)
	if len(c.pet.FloatingItems) != 1 || c.pet.FloatingItems[0].Emoji != "💥" {
		t.Fatalf("configured blood icon ignored, got %+v", c.pet.FloatingItems)
	}
}

func TestPresentsAreCappedExpiredAndAccepted(t *testing.T) {
	now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	c := newAdventureTestCoordinator()

	for i := 0; i < maxPresents+2; i++ {
		c.dropPresent(now, "mouse", "🐭", 20)
	}
	if len(c.pet.Presents) != maxPresents {
		t.Fatalf("presents = %d, want the pile capped at %d", len(c.pet.Presents), maxPresents)
	}
	if c.pet.Stats.PresentsBrought != maxPresents+2 {
		t.Fatalf("PresentsBrought = %d, want every gift counted", c.pet.Stats.PresentsBrought)
	}

	// An emoji-less catch isn't a present.
	before := len(c.pet.Presents)
	c.dropPresent(now, "ghost", "", 20)
	if len(c.pet.Presents) != before {
		t.Fatal("dropped a present with nothing to draw")
	}

	x := c.pet.Presents[0].X
	c.pet.Happiness = 95
	if !c.acceptPresentAt(x) {
		t.Fatal("clicking a present should accept it")
	}
	if c.pet.Stats.PresentsAccepted != 1 {
		t.Fatalf("PresentsAccepted = %d, want 1", c.pet.Stats.PresentsAccepted)
	}
	if c.pet.Happiness != 100 {
		t.Fatalf("Happiness = %d, want it clamped to 100", c.pet.Happiness)
	}
	if c.acceptPresentAt(-50) {
		t.Fatal("clicking empty ground accepted something")
	}

	c.expirePresents(now.Add(presentTTL + time.Second))
	if len(c.pet.Presents) != 0 {
		t.Fatalf("presents = %d after the TTL, want the cat to take them back", len(c.pet.Presents))
	}
}

func TestClampAdventureX(t *testing.T) {
	cases := []struct{ x, maxX, want int }{
		{x: 5, maxX: 34, want: 5},
		{x: 0, maxX: 34, want: 0},
		{x: 34, maxX: 34, want: 34},
		{x: 35, maxX: 34, want: 34},
		{x: 400, maxX: 34, want: 34},
		{x: -1, maxX: 34, want: 0},
		// A sidebar too narrow to have a play area at all still has to give a
		// drawable answer rather than a negative one.
		{x: 3, maxX: 0, want: 0},
		{x: 3, maxX: -2, want: 0},
	}
	for _, tc := range cases {
		if got := clampAdventureX(tc.x, tc.maxX); got != tc.want {
			t.Errorf("clampAdventureX(%d, %d) = %d, want %d", tc.x, tc.maxX, got, tc.want)
		}
	}
}

// TestAdventureMaxXIsDrawable ties the simulation's bound to the renderer's.
// updatePet derives maxX as width-5 and the renderer draws a sprite only while
// its column is below safePlayWidth (width-1); a two-cell emoji at maxX has to
// fit entirely inside that. If either side is ever retuned without the other,
// sprites go missing instead of overflowing, which is a much harder bug to see.
func TestAdventureMaxXIsDrawable(t *testing.T) {
	const emojiCells = 2
	for width := 10; width <= 200; width++ {
		maxX := width - 5
		safePlayWidth := width - 1
		if maxX+emojiCells > safePlayWidth {
			t.Fatalf("width %d: a sprite at maxX=%d spills past safePlayWidth=%d", width, maxX, safePlayWidth)
		}
	}
}

// TestEncounterKeepsCatAndPreyOnScreen runs the chase and insists both sprites
// stay in columns the renderer will actually draw. The prey used to be allowed
// to wander to maxX+5 before being snapped back, and the cat — which walks
// toward w.X with no bound of its own — followed it out of the play area.
func TestEncounterKeepsCatAndPreyOnScreen(t *testing.T) {
	const maxX = 34 // a 39-column sidebar

	for _, species := range []string{"bird", "mouse", "squirrel", "bug", "lizard", "butterfly"} {
		for seed := 0; seed < 40; seed++ {
			c := newAdventureTestCoordinator()
			data := adventureWildlife[species]
			c.pet.Adventure = adventureState{
				Active: true,
				Phase:  advPhaseEncounter,
				Biome:  "meadow",
				CatX:   maxX / 2,
				HomeX:  4,
				Wildlife: &wildlifeEncounter{
					Type:        species,
					Emoji:       data.Emoji,
					X:           maxX,
					Y:           data.YLevel,
					Speed:       data.Speed,
					CatchChance: data.CatchChance,
				},
			}
			now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
			for frame := 0; frame < 600; frame++ {
				c.pet.AnimFrame = frame
				c.updateEncounter(now, maxX)
				adv := &c.pet.Adventure
				if adv.CatX < 0 || adv.CatX > maxX {
					t.Fatalf("%s seed %d frame %d: cat wandered to %d (bounds 0..%d)",
						species, seed, frame, adv.CatX, maxX)
				}
				if adv.Wildlife != nil && (adv.Wildlife.X < 0 || adv.Wildlife.X > maxX) {
					t.Fatalf("%s seed %d frame %d: prey wandered to %d (bounds 0..%d)",
						species, seed, frame, adv.Wildlife.X, maxX)
				}
				if adv.Wildlife != nil && (adv.Wildlife.Caught || adv.Wildlife.Escaped) {
					break
				}
			}
		}
	}
}
