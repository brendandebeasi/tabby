package daemon

import (
	"sort"
	"time"
)

// lifetimeStats is the cat's permanent record. It lives inside petState and
// therefore rides along in pet.json; every field is omitempty/omitzero so an
// existing pet.json written before this struct existed still loads (Go
// zero-initialises what isn't there) and a brand new cat doesn't write a wall
// of zeroes.
//
// Durations are stored as whole seconds rather than time.Duration because
// pet.json is a file people open and read. Milliseconds are used for the two
// "fastest" records, where second granularity would round everything to 0.
//
// Rates and rankings (catch rate, favourite biome, rarest catch) are NOT
// stored: they're derived from the raw counters on read, so a counter fix
// never leaves a stale derived value behind.
type lifetimeStats struct {
	// Identity & lifecycle
	BornAt           time.Time `json:"born_at,omitzero"`
	CurrentLifeStart time.Time `json:"current_life_start,omitzero"`
	TotalDeaths      int       `json:"total_deaths,omitempty"`
	LongestLifeSec   int64     `json:"longest_life_sec,omitempty"`
	FirstDeathAt     time.Time `json:"first_death_at,omitzero"`

	// Time tracking. LastTickAt anchors the per-tick delta; a gap larger than
	// statsTickCap (daemon restart, laptop asleep) is discarded rather than
	// credited to whatever state the cat happened to be frozen in.
	LastTickAt      time.Time        `json:"last_tick_at,omitzero"`
	TimeByStateSec  map[string]int64 `json:"time_by_state_sec,omitempty"`
	TotalAliveSec   int64            `json:"total_alive_sec,omitempty"`
	LongestNapSec   int64            `json:"longest_nap_sec,omitempty"`
	CurrentNapSec   int64            `json:"current_nap_sec,omitempty"`
	LongestIdleSec  int64            `json:"longest_idle_sec,omitempty"`
	CurrentIdleSec  int64            `json:"current_idle_sec,omitempty"`
	TimeStarvingSec int64            `json:"time_starving_sec,omitempty"`
	TimeMaxHappySec int64            `json:"time_max_happy_sec,omitempty"`

	// Activity & movement
	TotalDistance int `json:"total_distance,omitempty"`
	TotalJumps    int `json:"total_jumps,omitempty"`
	PoopJumps     int `json:"poop_jumps,omitempty"`

	// Adventures
	TotalAdventures     int            `json:"total_adventures,omitempty"`
	AdventureSuccesses  int            `json:"adventure_successes,omitempty"`
	FirstAdventureAt    time.Time      `json:"first_adventure_at,omitzero"`
	LongestAdventureSec int64          `json:"longest_adventure_sec,omitempty"`
	AdventureStreak     int            `json:"adventure_streak,omitempty"`
	CurrentAdvStreak    int            `json:"current_adventure_streak,omitempty"`
	BiomeVisits         map[string]int `json:"biome_visits,omitempty"`
	WildlifeCaught      map[string]int `json:"wildlife_caught,omitempty"`
	WildlifeEscaped     map[string]int `json:"wildlife_escaped,omitempty"`
	PresentsBrought     int            `json:"presents_brought,omitempty"`
	PresentsAccepted    int            `json:"presents_accepted,omitempty"`

	// Interaction & care
	TotalInteractions int       `json:"total_interactions,omitempty"`
	LastInteractionAt time.Time `json:"last_interaction_at,omitzero"`
	LongestNeglectSec int64     `json:"longest_neglect_sec,omitempty"`
	CareStreak        int       `json:"care_streak,omitempty"`
	CurrentCareStreak int       `json:"current_care_streak,omitempty"`
	LastCareDay       string    `json:"last_care_day,omitempty"` // YYYY-MM-DD

	// Yarn & mouse
	YarnThrows         int       `json:"yarn_throws,omitempty"`
	YarnCatches        int       `json:"yarn_catches,omitempty"`
	FastestYarnCatchMs int64     `json:"fastest_yarn_catch_ms,omitempty"`
	LastYarnThrowAt    time.Time `json:"last_yarn_throw_at,omitzero"`
	MouseAppearances   int       `json:"mouse_appearances,omitempty"`
	MouseCatches       int       `json:"mouse_catches,omitempty"`
	FirstMouseCatchAt  time.Time `json:"first_mouse_catch_at,omitzero"`
	FastestMouseCatch  int64     `json:"fastest_mouse_catch_ms,omitempty"`
	LastMouseSpawnAt   time.Time `json:"last_mouse_spawn_at,omitzero"`

	// Food, poop, mood
	FirstFedAt            time.Time `json:"first_fed_at,omitzero"`
	LowestHungerBeforeFed int       `json:"lowest_hunger_before_fed,omitempty"`
	EverFed               bool      `json:"ever_fed,omitempty"`
	TotalPoopsProduced    int       `json:"total_poops_produced,omitempty"`
	PeakPoopCount         int       `json:"peak_poop_count,omitempty"`

	// Per-day records. DayKey is the day the two counters are accumulating
	// into; when the calendar day rolls over they're folded into the records
	// and reset.
	DayKey              string `json:"day_key,omitempty"`
	DayFeedings         int    `json:"day_feedings,omitempty"`
	DayPoops            int    `json:"day_poops,omitempty"`
	DayInteractions     int    `json:"day_interactions,omitempty"`
	MostFeedingsInADay  int    `json:"most_feedings_in_a_day,omitempty"`
	MostPoopsInADay     int    `json:"most_poops_in_a_day,omitempty"`
	BusiestDayCount     int    `json:"busiest_day_count,omitempty"`
	BusiestDay          string `json:"busiest_day,omitempty"`
	MostPoopsInADayDate string `json:"most_poops_in_a_day_date,omitempty"`

	// Thoughts
	TotalThoughts  int `json:"total_thoughts,omitempty"`
	LongestThought int `json:"longest_thought_chars,omitempty"`
}

// statsTickCap bounds a single accounting delta. Anything larger is treated as
// the daemon having been away (restart, suspended laptop) rather than as real
// elapsed cat-time, so a machine that slept for a week doesn't report a
// six-day nap.
const statsTickCap = 5 * time.Second

func dayKey(t time.Time) string { return t.Format("2006-01-02") }

// ensureBorn stamps a birthday on a cat that predates lifetime stats, so
// grandfathered pets get "today" rather than the zero time.
func (s *lifetimeStats) ensureBorn(now time.Time) {
	if s.BornAt.IsZero() {
		s.BornAt = now
	}
	if s.CurrentLifeStart.IsZero() {
		s.CurrentLifeStart = now
	}
}

// rollDay folds yesterday's running counters into the all-time records once
// the calendar day changes. Called from every counter that has a per-day
// record so the fold happens on the first event of the new day.
func (s *lifetimeStats) rollDay(now time.Time) {
	key := dayKey(now)
	if s.DayKey == key {
		return
	}
	if s.DayKey != "" {
		if s.DayFeedings > s.MostFeedingsInADay {
			s.MostFeedingsInADay = s.DayFeedings
		}
		if s.DayPoops > s.MostPoopsInADay {
			s.MostPoopsInADay = s.DayPoops
			s.MostPoopsInADayDate = s.DayKey
		}
		if s.DayInteractions > s.BusiestDayCount {
			s.BusiestDayCount = s.DayInteractions
			s.BusiestDay = s.DayKey
		}
	}
	s.DayKey = key
	s.DayFeedings = 0
	s.DayPoops = 0
	s.DayInteractions = 0
}

// tick accounts for the time elapsed since the previous tick, attributing it
// to the state the cat spent it in. state is the pet's animation state
// ("idle", "sleeping", ...); dead cats accrue nothing but their own clock.
func (s *lifetimeStats) tick(now time.Time, state string, dead bool, hunger, happiness int) {
	s.ensureBorn(now)
	prev := s.LastTickAt
	if prev.IsZero() {
		s.LastTickAt = now
		return
	}
	delta := now.Sub(prev)
	if delta <= 0 || delta > statsTickCap {
		s.LastTickAt = now
		return
	}
	// The pet ticks ~10x a second, so most deltas are a fraction of a second
	// and would floor to zero. Leave the anchor where it is until a whole
	// second has piled up behind it; that way the remainder is carried into
	// the next tick instead of being thrown away ten times a second.
	secs := int64(delta / time.Second)
	if secs == 0 {
		return
	}
	s.LastTickAt = prev.Add(time.Duration(secs) * time.Second)

	if dead {
		return
	}
	if s.TimeByStateSec == nil {
		s.TimeByStateSec = map[string]int64{}
	}
	if state == "" {
		state = "idle"
	}
	s.TimeByStateSec[state] += secs
	s.TotalAliveSec += secs

	if state == "sleeping" {
		s.CurrentNapSec += secs
		if s.CurrentNapSec > s.LongestNapSec {
			s.LongestNapSec = s.CurrentNapSec
		}
	} else {
		s.CurrentNapSec = 0
	}

	if state == "idle" {
		s.CurrentIdleSec += secs
		if s.CurrentIdleSec > s.LongestIdleSec {
			s.LongestIdleSec = s.CurrentIdleSec
		}
	} else {
		s.CurrentIdleSec = 0
	}

	if hunger <= 0 {
		s.TimeStarvingSec += secs
	}
	if happiness >= 100 {
		s.TimeMaxHappySec += secs
	}
}

// recordInteraction covers every deliberate act by the owner: petting,
// feeding, throwing yarn, scooping poop, taking a present. It drives the care
// streak and the neglect record.
func (s *lifetimeStats) recordInteraction(now time.Time) {
	s.ensureBorn(now)
	s.rollDay(now)
	if !s.LastInteractionAt.IsZero() {
		if gap := int64(now.Sub(s.LastInteractionAt) / time.Second); gap > s.LongestNeglectSec {
			s.LongestNeglectSec = gap
		}
	}
	s.LastInteractionAt = now
	s.TotalInteractions++
	s.DayInteractions++

	today := dayKey(now)
	switch s.LastCareDay {
	case today:
		// already counted today
	case dayKey(now.AddDate(0, 0, -1)):
		s.CurrentCareStreak++
	default:
		s.CurrentCareStreak = 1
	}
	s.LastCareDay = today
	if s.CurrentCareStreak > s.CareStreak {
		s.CareStreak = s.CurrentCareStreak
	}
}

func (s *lifetimeStats) recordFeeding(now time.Time, hungerBefore int) {
	s.rollDay(now)
	s.DayFeedings++
	if s.FirstFedAt.IsZero() {
		s.FirstFedAt = now
	}
	if !s.EverFed || hungerBefore < s.LowestHungerBeforeFed {
		s.LowestHungerBeforeFed = hungerBefore
		s.EverFed = true
	}
}

func (s *lifetimeStats) recordPoop(now time.Time, onGround int) {
	s.rollDay(now)
	s.DayPoops++
	s.TotalPoopsProduced++
	if onGround > s.PeakPoopCount {
		s.PeakPoopCount = onGround
	}
}

func (s *lifetimeStats) recordDeath(now time.Time) {
	s.TotalDeaths++
	if s.FirstDeathAt.IsZero() {
		s.FirstDeathAt = now
	}
	if !s.CurrentLifeStart.IsZero() {
		if life := int64(now.Sub(s.CurrentLifeStart) / time.Second); life > s.LongestLifeSec {
			s.LongestLifeSec = life
		}
	}
	s.CurrentLifeStart = time.Time{}
}

func (s *lifetimeStats) recordRevival(now time.Time) {
	s.CurrentLifeStart = now
}

func (s *lifetimeStats) recordAdventureStart(now time.Time, biome string) {
	s.ensureBorn(now)
	s.TotalAdventures++
	if s.FirstAdventureAt.IsZero() {
		s.FirstAdventureAt = now
	}
	if biome != "" {
		if s.BiomeVisits == nil {
			s.BiomeVisits = map[string]int{}
		}
		s.BiomeVisits[biome]++
	}
}

// recordAdventureEnd closes out an adventure. caught reports whether the cat
// came home with anything, which is what the success streak counts.
func (s *lifetimeStats) recordAdventureEnd(startedAt time.Time, now time.Time, caught bool) {
	if !startedAt.IsZero() {
		if d := int64(now.Sub(startedAt) / time.Second); d > s.LongestAdventureSec {
			s.LongestAdventureSec = d
		}
	}
	if caught {
		s.AdventureSuccesses++
		s.CurrentAdvStreak++
		if s.CurrentAdvStreak > s.AdventureStreak {
			s.AdventureStreak = s.CurrentAdvStreak
		}
	} else {
		s.CurrentAdvStreak = 0
	}
}

func (s *lifetimeStats) recordCatch(kind string) {
	if kind == "" {
		return
	}
	if s.WildlifeCaught == nil {
		s.WildlifeCaught = map[string]int{}
	}
	s.WildlifeCaught[kind]++
}

func (s *lifetimeStats) recordEscape(kind string) {
	if kind == "" {
		return
	}
	if s.WildlifeEscaped == nil {
		s.WildlifeEscaped = map[string]int{}
	}
	s.WildlifeEscaped[kind]++
}

// recordYarnThrow counts the throw and, since tossing yarn is the owner
// showing up, also counts as an interaction.
func (s *lifetimeStats) recordYarnThrow(now time.Time) {
	s.YarnThrows++
	s.LastYarnThrowAt = now
	s.recordInteraction(now)
}

func (s *lifetimeStats) recordYarnCatch(now time.Time) {
	s.YarnCatches++
	if s.LastYarnThrowAt.IsZero() {
		return
	}
	ms := int64(now.Sub(s.LastYarnThrowAt) / time.Millisecond)
	if ms > 0 && (s.FastestYarnCatchMs == 0 || ms < s.FastestYarnCatchMs) {
		s.FastestYarnCatchMs = ms
	}
	s.LastYarnThrowAt = time.Time{}
}

func (s *lifetimeStats) recordMouseAppear(now time.Time) {
	s.MouseAppearances++
	s.LastMouseSpawnAt = now
}

func (s *lifetimeStats) recordMouseCatch(now time.Time) {
	s.MouseCatches++
	if s.FirstMouseCatchAt.IsZero() {
		s.FirstMouseCatchAt = now
	}
	if s.LastMouseSpawnAt.IsZero() {
		return
	}
	ms := int64(now.Sub(s.LastMouseSpawnAt) / time.Millisecond)
	if ms > 0 && (s.FastestMouseCatch == 0 || ms < s.FastestMouseCatch) {
		s.FastestMouseCatch = ms
	}
	s.LastMouseSpawnAt = time.Time{}
}

func (s *lifetimeStats) recordThought(text string) {
	s.TotalThoughts++
	if n := len([]rune(text)); n > s.LongestThought {
		s.LongestThought = n
	}
}

// --- derived stats -------------------------------------------------------

func (s *lifetimeStats) YarnCatchRate() float64 {
	if s.YarnThrows == 0 {
		return 0
	}
	return float64(s.YarnCatches) / float64(s.YarnThrows)
}

func (s *lifetimeStats) MouseCatchRate() float64 {
	if s.MouseAppearances == 0 {
		return 0
	}
	return float64(s.MouseCatches) / float64(s.MouseAppearances)
}

// FavoriteBiome is the most-visited biome, ties broken alphabetically so the
// answer doesn't flicker between renders of the same data.
func (s *lifetimeStats) FavoriteBiome() string {
	return topKey(s.BiomeVisits, true)
}

// RarestCatch is the least-caught wildlife the cat has actually caught at
// least once. Something never caught isn't rare, it's absent.
func (s *lifetimeStats) RarestCatch() string {
	return topKey(s.WildlifeCaught, false)
}

func topKey(m map[string]int, highest bool) string {
	if len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	best := keys[0]
	for _, k := range keys[1:] {
		if (highest && m[k] > m[best]) || (!highest && m[k] < m[best]) {
			best = k
		}
	}
	return best
}
