package daemon

import (
	"encoding/json"
	"testing"
	"time"
)

func TestLifetimeStatsTickAttributesTimeToState(t *testing.T) {
	var s lifetimeStats
	start := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	s.tick(start, "idle", false, 50, 50)
	if s.BornAt.IsZero() {
		t.Fatal("first tick should stamp BornAt")
	}
	if s.TotalAliveSec != 0 {
		t.Fatalf("first tick has no previous anchor, got %d alive seconds", s.TotalAliveSec)
	}

	s.tick(start.Add(3*time.Second), "idle", false, 50, 50)
	if s.TotalAliveSec != 3 {
		t.Fatalf("TotalAliveSec = %d, want 3", s.TotalAliveSec)
	}
	if s.TimeByStateSec["idle"] != 3 {
		t.Fatalf("idle seconds = %d, want 3", s.TimeByStateSec["idle"])
	}
	if s.CurrentIdleSec != 3 || s.LongestIdleSec != 3 {
		t.Fatalf("idle dwell = %d/%d, want 3/3", s.CurrentIdleSec, s.LongestIdleSec)
	}

	// Switching state resets the idle dwell but keeps the record.
	s.tick(start.Add(5*time.Second), "walking", false, 50, 50)
	if s.CurrentIdleSec != 0 {
		t.Fatalf("CurrentIdleSec = %d after leaving idle, want 0", s.CurrentIdleSec)
	}
	if s.LongestIdleSec != 3 {
		t.Fatalf("LongestIdleSec = %d, want the 3 it reached", s.LongestIdleSec)
	}
}

func TestLifetimeStatsTickCarriesSubSecondRemainder(t *testing.T) {
	var s lifetimeStats
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	s.tick(now, "idle", false, 50, 50)

	// Ten ticks of 100ms is one second, not zero — the anchor only advances
	// by whole seconds so the remainder isn't discarded each frame.
	for i := 1; i <= 10; i++ {
		s.tick(now.Add(time.Duration(i)*100*time.Millisecond), "idle", false, 50, 50)
	}
	if s.TotalAliveSec != 1 {
		t.Fatalf("TotalAliveSec = %d after 1s of 100ms ticks, want 1", s.TotalAliveSec)
	}
}

func TestLifetimeStatsTickDiscardsLongGaps(t *testing.T) {
	var s lifetimeStats
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	s.tick(now, "idle", false, 50, 50)

	// A closed laptop shouldn't credit the cat with eight hours of idling.
	s.tick(now.Add(8*time.Hour), "idle", false, 50, 50)
	if s.TotalAliveSec != 0 {
		t.Fatalf("TotalAliveSec = %d across a gap, want 0", s.TotalAliveSec)
	}
}

func TestLifetimeStatsDeadCatAccruesNothing(t *testing.T) {
	var s lifetimeStats
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	s.tick(now, "dead", true, 0, 0)
	s.tick(now.Add(3*time.Second), "dead", true, 0, 0)
	if s.TotalAliveSec != 0 || len(s.TimeByStateSec) != 0 {
		t.Fatalf("dead cat accrued time: alive=%d states=%v", s.TotalAliveSec, s.TimeByStateSec)
	}
}

func TestLifetimeStatsCareStreak(t *testing.T) {
	var s lifetimeStats
	day1 := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

	s.recordInteraction(day1)
	s.recordInteraction(day1.Add(2 * time.Hour)) // same day, no streak bump
	if s.CurrentCareStreak != 1 {
		t.Fatalf("same-day streak = %d, want 1", s.CurrentCareStreak)
	}
	s.recordInteraction(day1.AddDate(0, 0, 1))
	s.recordInteraction(day1.AddDate(0, 0, 2))
	if s.CurrentCareStreak != 3 || s.CareStreak != 3 {
		t.Fatalf("streak = %d (best %d), want 3/3", s.CurrentCareStreak, s.CareStreak)
	}

	// Skipping a day starts over but leaves the record standing.
	s.recordInteraction(day1.AddDate(0, 0, 5))
	if s.CurrentCareStreak != 1 {
		t.Fatalf("streak after a skipped day = %d, want 1", s.CurrentCareStreak)
	}
	if s.CareStreak != 3 {
		t.Fatalf("best streak = %d, want 3", s.CareStreak)
	}
	if s.LongestNeglectSec < int64(3*24*time.Hour/time.Second) {
		t.Fatalf("LongestNeglectSec = %d, want at least three days", s.LongestNeglectSec)
	}
}

func TestLifetimeStatsPerDayCountersRoll(t *testing.T) {
	var s lifetimeStats
	day1 := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

	s.recordFeeding(day1, 10)
	s.recordFeeding(day1.Add(time.Hour), 20)
	s.recordPoop(day1.Add(2*time.Hour), 2)
	if s.DayFeedings != 2 {
		t.Fatalf("DayFeedings = %d, want 2", s.DayFeedings)
	}

	s.recordFeeding(day1.AddDate(0, 0, 1), 40)
	if s.DayFeedings != 1 {
		t.Fatalf("DayFeedings = %d after the day rolled, want 1", s.DayFeedings)
	}
	if s.MostFeedingsInADay != 2 {
		t.Fatalf("MostFeedingsInADay = %d, want 2", s.MostFeedingsInADay)
	}
	if s.LowestHungerBeforeFed != 10 {
		t.Fatalf("LowestHungerBeforeFed = %d, want 10", s.LowestHungerBeforeFed)
	}
}

func TestLifetimeStatsAdventureStreakAndBiomes(t *testing.T) {
	var s lifetimeStats
	now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

	for i, biome := range []string{"forest", "forest", "meadow"} {
		start := now.Add(time.Duration(i) * time.Hour)
		s.recordAdventureStart(start, biome)
		s.recordAdventureEnd(start, start.Add(30*time.Second), true)
	}
	if s.TotalAdventures != 3 || s.AdventureSuccesses != 3 {
		t.Fatalf("adventures = %d/%d, want 3/3", s.TotalAdventures, s.AdventureSuccesses)
	}
	if s.AdventureStreak != 3 {
		t.Fatalf("AdventureStreak = %d, want 3", s.AdventureStreak)
	}
	if s.LongestAdventureSec != 30 {
		t.Fatalf("LongestAdventureSec = %d, want 30", s.LongestAdventureSec)
	}
	if got := s.FavoriteBiome(); got != "forest" {
		t.Fatalf("FavoriteBiome = %q, want forest", got)
	}

	// A fruitless outing breaks the streak without touching the record.
	last := now.Add(4 * time.Hour)
	s.recordAdventureStart(last, "garden")
	s.recordAdventureEnd(last, last.Add(time.Second), false)
	if s.CurrentAdvStreak != 0 || s.AdventureStreak != 3 {
		t.Fatalf("streak = %d (best %d), want 0/3", s.CurrentAdvStreak, s.AdventureStreak)
	}
}

func TestLifetimeStatsRarestCatchIgnoresNeverCaught(t *testing.T) {
	var s lifetimeStats
	s.recordCatch("bird")
	s.recordCatch("bird")
	s.recordCatch("bird")
	s.recordCatch("squirrel")
	s.recordEscape("mouse")

	if got := s.RarestCatch(); got != "squirrel" {
		t.Fatalf("RarestCatch = %q, want squirrel (mouse was never caught)", got)
	}
}

func TestLifetimeStatsCatchRates(t *testing.T) {
	var s lifetimeStats
	now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

	if s.YarnCatchRate() != 0 || s.MouseCatchRate() != 0 {
		t.Fatal("rates should be 0, not NaN, before anything has happened")
	}

	s.recordYarnThrow(now)
	s.recordYarnCatch(now.Add(800 * time.Millisecond))
	s.recordYarnThrow(now.Add(time.Minute))
	if got := s.YarnCatchRate(); got != 0.5 {
		t.Fatalf("YarnCatchRate = %v, want 0.5", got)
	}
	if s.FastestYarnCatchMs != 800 {
		t.Fatalf("FastestYarnCatchMs = %d, want 800", s.FastestYarnCatchMs)
	}

	// A slower catch doesn't overwrite the record.
	s.recordYarnCatch(now.Add(time.Minute + 5*time.Second))
	if s.FastestYarnCatchMs != 800 {
		t.Fatalf("FastestYarnCatchMs = %d after a slow catch, want 800", s.FastestYarnCatchMs)
	}
}

func TestLifetimeStatsDeathAndRevival(t *testing.T) {
	var s lifetimeStats
	born := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	s.ensureBorn(born)

	died := born.Add(2 * time.Hour)
	s.recordDeath(died)
	if s.TotalDeaths != 1 || !s.FirstDeathAt.Equal(died) {
		t.Fatalf("death not recorded: deaths=%d first=%v", s.TotalDeaths, s.FirstDeathAt)
	}
	if s.LongestLifeSec != int64(2*time.Hour/time.Second) {
		t.Fatalf("LongestLifeSec = %d, want two hours", s.LongestLifeSec)
	}

	revived := died.Add(time.Minute)
	s.recordRevival(revived)
	if !s.CurrentLifeStart.Equal(revived) {
		t.Fatalf("CurrentLifeStart = %v, want %v", s.CurrentLifeStart, revived)
	}

	// A shorter second life leaves the record alone.
	s.recordDeath(revived.Add(time.Minute))
	if s.LongestLifeSec != int64(2*time.Hour/time.Second) {
		t.Fatalf("LongestLifeSec = %d after a short life, want two hours", s.LongestLifeSec)
	}
}

// A pet.json written before lifetime stats existed has to keep loading, and a
// brand new cat shouldn't write a wall of zeroes into it.
func TestLifetimeStatsRoundTripIsBackwardCompatible(t *testing.T) {
	var old petState
	if err := json.Unmarshal([]byte(`{"Hunger":80,"Happiness":60}`), &old); err != nil {
		t.Fatalf("legacy pet.json failed to load: %v", err)
	}
	if old.Stats.TotalAdventures != 0 || old.Stats.TimeByStateSec != nil {
		t.Fatal("missing stats should decode as zero values")
	}

	blob, err := json.Marshal(petState{Hunger: 80})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(blob, &probe); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := probe["lifetime_stats"]; ok {
		t.Fatal("an untouched stats block should be omitted, not written as zeroes")
	}
}
