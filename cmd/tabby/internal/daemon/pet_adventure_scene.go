package daemon

import (
	"math/rand"
	"time"
)

// Adventure scenery is generated from adv.SceneOffset rather than stored, so
// the simulation (does the cat have to hop over something?) and the renderer
// (what do we draw?) have to agree exactly. These helpers are the one place
// that decision lives; both callers go through them.

// scenerySpacing is how many world columns apart ground scenery is placed.
const scenerySpacing = 7

// airScenerySpacing is the (sparser) spacing for the flying decorations.
const airScenerySpacing = 11

// adventureSceneryAt returns the ground scenery emoji at a world column, or ""
// when that column is bare. Flying decorations belong in the air row, so they
// never count as ground scenery.
func adventureSceneryAt(biome biomeData, worldX int) string {
	if len(biome.Scenery) == 0 || worldX%scenerySpacing != 0 {
		return ""
	}
	emoji := biome.Scenery[(worldX/scenerySpacing)%len(biome.Scenery)]
	if emoji == "🦋" {
		return ""
	}
	return emoji
}

// adventureAirSceneryAt returns the low-air decoration at a world column, or ""
// when there is none.
func adventureAirSceneryAt(biome biomeData, worldX int) string {
	if len(biome.Scenery) == 0 || worldX%airScenerySpacing != 0 {
		return ""
	}
	emoji := biome.Scenery[(worldX/airScenerySpacing)%len(biome.Scenery)]
	if emoji == "🦋" || emoji == "🐦" {
		return emoji
	}
	return ""
}

// adventureObstacles is the scenery a cat can't just walk through. Everything
// else (flowers, leaves, grass) it strolls over without comment.
var adventureObstacles = map[string]bool{
	"🪨": true, // rock
	"🌳": true, // tree
	"🌲": true, // evergreen
	"🪵": true, // log
	"🪴": true, // potted plant
}

// adventureIsObstacle reports whether scenery blocks the cat's path — the
// things it hops over and can land on top of.
func adventureIsObstacle(emoji string) bool { return adventureObstacles[emoji] }

// Hop and perch timings, in animation frames.
const (
	// hopFrames is how long the cat stays airborne clearing an obstacle.
	hopFrames = 6
	// perchMinFrames / perchMaxFrames bound how long the cat sits on top of
	// something before hopping down and carrying on.
	perchMinFrames = 20
	perchMaxFrames = 40
	// perchChance is the percent of obstacles the cat climbs rather than
	// clears. Most get hopped; a good rock is worth stopping for.
	perchChance = 35
)

// bloodTTL is how long a splatter lingers.
const bloodTTL = 1200 * time.Millisecond

// presentChance is the percent chance that a successful catch gets carried
// home instead of eaten on the spot, and maxPresents caps how many gifts can
// be piled up in the yard at once. presentTTL is how long one waits to be
// noticed before the cat takes it back.
const (
	presentChance = 60
	maxPresents   = 3
	presentTTL    = 10 * time.Minute
)

// clampAdventureX keeps an adventure sprite inside the play area.
//
// maxX is already the last column a two-cell emoji fits in (it is derived as
// width-5 for exactly that reason), and the renderer draws a sprite only while
// its column is inside the row — so anything past maxX does not overflow the
// row, it silently vanishes. Everything the chase moves has to come back
// through here, or the cat is left stalking a bird that is no longer on screen.
func clampAdventureX(x, maxX int) int {
	if maxX < 0 {
		maxX = 0
	}
	if x < 0 {
		return 0
	}
	if x > maxX {
		return maxX
	}
	return x
}

// adventureIdleDwell is how long the cat has to have been doing nothing before
// it will wander off. Without it the cat can leave the instant you put a toy
// down, because "idle" is only ever one frame away.
const adventureIdleDwell = 8

// petCanAdventure reports whether the cat is free to head off. The bar is
// higher than "state == idle": anything the owner has put in the yard (yarn,
// food, a mouse to chase) or asked the cat (a pending question) keeps it home,
// and it has to have been genuinely idle for a few seconds first.
func (c *Coordinator) petCanAdventure(now time.Time) bool {
	p := &c.pet
	if p.Adventure.Active || p.IsDead {
		return false
	}
	if p.HasTarget || p.ActionPending != "" {
		return false
	}
	if p.PendingQuestion != nil {
		return false
	}
	// Toys and food out means there's something better to do here.
	if p.YarnPos.X >= 0 && !p.YarnExpiresAt.IsZero() && now.Before(p.YarnExpiresAt) {
		return false
	}
	if p.FoodItem.X >= 0 {
		return false
	}
	if p.MousePos.X >= 0 {
		return false
	}
	// Don't leave a mess for someone else to find.
	if len(p.PoopPositions) > 0 {
		return false
	}
	if p.Stats.CurrentIdleSec < adventureIdleDwell {
		return false
	}
	// adventure_chance gates how often the urge actually strikes. It was
	// documented but never read before; 100 keeps the historical behaviour
	// for anyone who hasn't set it.
	chance := c.config.Widgets.Pet.AdventureChance
	if chance <= 0 {
		chance = 100
	}
	return chance >= 100 || rand.Intn(100) < chance
}

// dropPresent leaves a catch on the ground next to the cat as a gift. Cats
// bring you things; that is the whole feature.
func (c *Coordinator) dropPresent(now time.Time, kind, emoji string, maxX int) {
	if emoji == "" {
		return
	}
	x := c.pet.Pos.X + 1
	if x > maxX {
		x = maxX
	}
	if x < 0 {
		x = 0
	}
	c.pet.Presents = append(c.pet.Presents, petPresent{
		Type:      kind,
		Emoji:     emoji,
		X:         x,
		BroughtAt: now,
	})
	// Oldest gifts get taken away when the pile gets too deep.
	if len(c.pet.Presents) > maxPresents {
		c.pet.Presents = c.pet.Presents[len(c.pet.Presents)-maxPresents:]
	}
	c.pet.Stats.PresentsBrought++
	c.pet.LastThought = "brought you something."
}

// expirePresents removes gifts nobody acknowledged. The cat gives up and takes
// them back rather than leaving the yard littered forever.
func (c *Coordinator) expirePresents(now time.Time) {
	if len(c.pet.Presents) == 0 {
		return
	}
	kept := c.pet.Presents[:0]
	for _, p := range c.pet.Presents {
		if now.Sub(p.BroughtAt) < presentTTL {
			kept = append(kept, p)
		}
	}
	c.pet.Presents = kept
}

// acceptPresentAt takes the gift at a play-area column, if there is one, and
// reports whether it found one. Acknowledging a present makes the cat happy.
func (c *Coordinator) acceptPresentAt(x int) bool {
	for i, p := range c.pet.Presents {
		if abs(p.X-x) > 1 {
			continue
		}
		c.pet.Presents = append(c.pet.Presents[:i], c.pet.Presents[i+1:]...)
		c.pet.Stats.PresentsAccepted++
		c.pet.Stats.recordInteraction(time.Now())
		c.pet.Happiness += 10
		if c.pet.Happiness > 100 {
			c.pet.Happiness = 100
		}
		c.pet.LastThought = "you liked it!"
		return true
	}
	return false
}

// spawnBlood drops a splatter at a play-area cell. It is opt-in: without
// adventure_blood the kill is bloodless.
func (c *Coordinator) spawnBlood(now time.Time, x, y int) {
	if !c.config.Widgets.Pet.AdventureBlood {
		return
	}
	blood := "🩸"
	if c.config.Widgets.Pet.Icons.Blood != "" {
		blood = c.config.Widgets.Pet.Icons.Blood
	}
	c.pet.FloatingItems = append(c.pet.FloatingItems, floatingItem{
		Emoji:     blood,
		Pos:       pos2D{X: x, Y: y},
		ExpiresAt: now.Add(bloodTTL),
	})
}
