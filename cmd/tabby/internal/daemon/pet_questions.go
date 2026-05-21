package daemon

// QuestionDef is one entry in the hardcoded seed question bank used by the
// pet Q&A personality-building loop. The bank is the Phase-1 source of
// questions (Phase 3 may layer LLM-generated questions on top). Each entry
// carries enough metadata for the daemon to:
//
//  1. Construct a PendingQuestion (ID/Text/Kind/Choices)
//  2. Distill an answer into a PersonalityTrait without any LLM call
//     (TraitFor)
//  3. Filter / weight the bank by category (Tags)
//
// TraitFor semantics:
//
//   - For Kind == "choice", keys are exact choice strings from Choices and
//     values are the trait template to store on a match. A choice with no
//     entry in TraitFor produces no trait (e.g. "skip" / "neither" style
//     answers).
//
//   - For Kind == "free_text", the conventional key is "*" — the value is a
//     trait template containing a single %s placeholder that the logic-author
//     interpolates with the user's free-text answer (truncated for prompt
//     budget). Bank entries here only declare the template; the actual
//     %s-substitution happens in pet_qa.go later.
//
// Tags categorise the question for cadence / weighting:
//
//   - "system"      — handled specially (e.g. the consent question)
//   - "about_you"   — facts about the user
//   - "about_cat"   — facts about the cat's character / preferences
//   - "reflective"  — open-ended state-of-mind / day-shape prompts
//   - "consent"     — reserved for any future consent prompts
type QuestionDef struct {
	ID       string            // stable, used as PendingQuestion.ID
	Text     string            // question text shown to the user
	Kind     string            // "choice" | "free_text"
	Choices  []string          // nil for free_text
	TraitFor map[string]string // choice -> trait template; "*" for free_text
	Tags     []string          // see categories above
}

// SeedQuestions is the bank consumed by pickQuestion. Index 0 is the consent
// question and must remain index 0 — pet_qa.go bootstraps the first
// interaction by selecting SeedQuestions[0] explicitly. Everything after
// index 0 is shuffled / weighted by tag.
//
// Counts: 1 system (consent) + 10 about_you + 10 about_cat + 9 reflective = 30.
var SeedQuestions = []QuestionDef{
	// -------------------------------------------------------------------
	// Index 0 — Consent. Special-cased in pet_qa.go; TraitFor is empty
	// because the answer drives PetState.QAOptedOut / QAFreeTextOptedOut,
	// not the traits list.
	// -------------------------------------------------------------------
	{
		ID:   "consent",
		Text: "I'd love to learn about you so my thoughts can be more personal. Your answers will be saved locally in pet.json and sent to the LLM that already generates my thoughts. Are you okay with that?",
		Kind: "choice",
		Choices: []string{
			"Yes, ask away",
			"Multi-choice only (no free-text)",
			"No thanks",
		},
		TraitFor: map[string]string{},
		Tags:     []string{"system"},
	},

	// -------------------------------------------------------------------
	// About you — 10 entries. Mix of choice + free_text.
	// -------------------------------------------------------------------
	{
		ID:      "morning_or_night",
		Text:    "Be honest: are you a morning person or a night owl?",
		Kind:    "choice",
		Choices: []string{"Morning person", "Night owl", "Somewhere in between"},
		TraitFor: map[string]string{
			"Morning person":      "user is a morning person",
			"Night owl":           "user is a night owl",
			"Somewhere in between": "user keeps flexible hours",
		},
		Tags: []string{"about_you"},
	},
	{
		ID:      "tabs_or_spaces",
		Text:    "The eternal question — tabs or spaces?",
		Kind:    "choice",
		Choices: []string{"Tabs", "Spaces", "Whatever the linter says"},
		TraitFor: map[string]string{
			"Tabs":                    "user prefers tabs over spaces",
			"Spaces":                  "user prefers spaces over tabs",
			"Whatever the linter says": "user defers to the linter",
		},
		Tags: []string{"about_you"},
	},
	{
		ID:      "coffee_or_tea",
		Text:    "Coffee or tea while you code?",
		Kind:    "choice",
		Choices: []string{"Coffee", "Tea", "Water", "Something with sugar in it"},
		TraitFor: map[string]string{
			"Coffee":                     "user drinks coffee while working",
			"Tea":                        "user drinks tea while working",
			"Water":                      "user keeps hydrated with water",
			"Something with sugar in it": "user runs on sugary drinks",
		},
		Tags: []string{"about_you"},
	},
	{
		ID:      "editor_loyalty",
		Text:    "Which editor have you spent the most hours in this year?",
		Kind:    "choice",
		Choices: []string{"Neovim/Vim", "VS Code", "JetBrains", "Emacs", "Something else"},
		TraitFor: map[string]string{
			"Neovim/Vim":     "user lives in vim/neovim",
			"VS Code":        "user works in VS Code",
			"JetBrains":      "user works in a JetBrains IDE",
			"Emacs":          "user works in emacs",
			"Something else": "user uses a less-common editor",
		},
		Tags: []string{"about_you"},
	},
	{
		ID:      "music_while_coding",
		Text:    "Do you listen to music while you work?",
		Kind:    "choice",
		Choices: []string{"Always", "Sometimes", "Silence please"},
		TraitFor: map[string]string{
			"Always":         "user always codes with music on",
			"Sometimes":      "user sometimes codes to music",
			"Silence please": "user prefers silence while working",
		},
		Tags: []string{"about_you"},
	},
	{
		ID:      "favourite_language",
		Text:    "What language do you reach for first these days?",
		Kind:    "free_text",
		TraitFor: map[string]string{
			"*": "user reaches for %s first",
		},
		Tags: []string{"about_you"},
	},
	{
		ID:      "current_project",
		Text:    "What are you building right now in a sentence?",
		Kind:    "free_text",
		TraitFor: map[string]string{
			"*": "user is currently building: %s",
		},
		Tags: []string{"about_you"},
	},
	{
		ID:      "favourite_terminal_font",
		Text:    "What font do you stare at all day in the terminal?",
		Kind:    "free_text",
		TraitFor: map[string]string{
			"*": "user codes in the %s font",
		},
		Tags: []string{"about_you"},
	},
	{
		ID:      "remote_or_office",
		Text:    "Are you working from home, an office, or somewhere weirder today?",
		Kind:    "choice",
		Choices: []string{"Home", "Office", "Coffee shop / public space", "Somewhere weirder"},
		TraitFor: map[string]string{
			"Home":                       "user works from home",
			"Office":                     "user works from an office",
			"Coffee shop / public space": "user often works from coffee shops",
			"Somewhere weirder":          "user enjoys unusual work locations",
		},
		Tags: []string{"about_you"},
	},
	{
		ID:      "biggest_distraction",
		Text:    "What's been pulling your focus away most this week?",
		Kind:    "free_text",
		TraitFor: map[string]string{
			"*": "user has been distracted by %s lately",
		},
		Tags: []string{"about_you"},
	},

	// -------------------------------------------------------------------
	// About the cat — 10 entries. The cat asks the user to define its
	// preferences, which become traits the LLM uses when generating
	// thoughts ("the cat believes its favourite food is tuna").
	// -------------------------------------------------------------------
	{
		ID:      "cat_favourite_food",
		Text:    "If I could pick any food, what should I say I love most?",
		Kind:    "choice",
		Choices: []string{"Tuna", "Salmon", "Chicken", "Cheese", "Surprise me"},
		TraitFor: map[string]string{
			"Tuna":        "cat's favourite food is tuna",
			"Salmon":      "cat's favourite food is salmon",
			"Chicken":     "cat's favourite food is chicken",
			"Cheese":      "cat insists its favourite food is cheese",
			"Surprise me": "cat enjoys unusual foods",
		},
		Tags: []string{"about_cat"},
	},
	{
		ID:      "cat_personality",
		Text:    "How would you describe my personality?",
		Kind:    "choice",
		Choices: []string{"Mischievous", "Lazy and content", "Curious and chatty", "Grumpy but loving", "Anxious overthinker"},
		TraitFor: map[string]string{
			"Mischievous":         "cat has a mischievous personality",
			"Lazy and content":    "cat is lazy and easily contented",
			"Curious and chatty":  "cat is curious and chatty",
			"Grumpy but loving":   "cat is grumpy but secretly loving",
			"Anxious overthinker": "cat is an anxious overthinker",
		},
		Tags: []string{"about_cat"},
	},
	{
		ID:      "cat_favourite_toy",
		Text:    "What's my favourite thing to play with?",
		Kind:    "choice",
		Choices: []string{"Yarn ball", "Cardboard box", "Hair tie", "Your keyboard cable", "Bottle cap"},
		TraitFor: map[string]string{
			"Yarn ball":           "cat's favourite toy is a yarn ball",
			"Cardboard box":       "cat's favourite toy is a cardboard box",
			"Hair tie":            "cat's favourite toy is a stolen hair tie",
			"Your keyboard cable": "cat's favourite toy is the user's keyboard cable",
			"Bottle cap":          "cat's favourite toy is a bottle cap",
		},
		Tags: []string{"about_cat"},
	},
	{
		ID:      "cat_sleeping_spot",
		Text:    "Where do I most love to sleep?",
		Kind:    "choice",
		Choices: []string{"On the keyboard", "In a sunbeam", "On top of the monitor", "In a laundry basket", "Curled on the user's lap"},
		TraitFor: map[string]string{
			"On the keyboard":           "cat loves sleeping on the keyboard",
			"In a sunbeam":              "cat loves napping in sunbeams",
			"On top of the monitor":     "cat loves sleeping on top of the monitor",
			"In a laundry basket":       "cat loves curling up in laundry baskets",
			"Curled on the user's lap":  "cat loves sleeping curled on the user's lap",
		},
		Tags: []string{"about_cat"},
	},
	{
		ID:      "cat_secret_talent",
		Text:    "What's a secret talent I have that nobody knows about?",
		Kind:    "free_text",
		TraitFor: map[string]string{
			"*": "cat has a secret talent: %s",
		},
		Tags: []string{"about_cat"},
	},
	{
		ID:      "cat_nemesis",
		Text:    "Do I have an arch-nemesis? Who or what is it?",
		Kind:    "free_text",
		TraitFor: map[string]string{
			"*": "cat's arch-nemesis is %s",
		},
		Tags: []string{"about_cat"},
	},
	{
		ID:      "cat_origin_story",
		Text:    "Where did I come from? Pick a backstory you like.",
		Kind:    "choice",
		Choices: []string{"Adopted from a shelter", "Found as a stray", "Born here", "Showed up one day and stayed", "Time traveller"},
		TraitFor: map[string]string{
			"Adopted from a shelter":      "cat was adopted from a shelter",
			"Found as a stray":            "cat was once a stray",
			"Born here":                   "cat was born in the user's home",
			"Showed up one day and stayed": "cat showed up one day and decided to stay",
			"Time traveller":              "cat believes itself to be a time traveller",
		},
		Tags: []string{"about_cat"},
	},
	{
		ID:      "cat_dream_job",
		Text:    "If I had to have a job, what should I do for a living?",
		Kind:    "free_text",
		TraitFor: map[string]string{
			"*": "cat would work as %s if cats had jobs",
		},
		Tags: []string{"about_cat"},
	},
	{
		ID:      "cat_relationship_to_water",
		Text:    "How do I feel about water?",
		Kind:    "choice",
		Choices: []string{"Terrified of it", "Fascinated by it", "Will only drink from running taps", "Loves the bath"},
		TraitFor: map[string]string{
			"Terrified of it":                "cat is terrified of water",
			"Fascinated by it":               "cat is fascinated by water",
			"Will only drink from running taps": "cat only drinks from running taps",
			"Loves the bath":                  "cat unusually enjoys baths",
		},
		Tags: []string{"about_cat"},
	},
	{
		ID:      "cat_catchphrase",
		Text:    "Give me a catchphrase I'd say if I could talk.",
		Kind:    "free_text",
		TraitFor: map[string]string{
			"*": "cat's catchphrase is \"%s\"",
		},
		Tags: []string{"about_cat"},
	},

	// -------------------------------------------------------------------
	// Reflective — 9 entries. Lighter weight on free-text traits; these
	// tend to be ephemeral mood / recent-events questions that the LLM
	// uses for immediate flavour rather than long-term personality.
	// -------------------------------------------------------------------
	{
		ID:      "how_are_you_today",
		Text:    "How's your day actually going so far?",
		Kind:    "choice",
		Choices: []string{"Great", "Fine", "Tired", "Stressed", "It's complicated"},
		TraitFor: map[string]string{
			"Great":            "user is having a great day",
			"Fine":             "user is having an ordinary day",
			"Tired":            "user is feeling tired today",
			"Stressed":         "user is feeling stressed today",
			"It's complicated": "user is having a complicated day",
		},
		Tags: []string{"reflective"},
	},
	{
		ID:      "shipped_today",
		Text:    "What did you ship or finish today?",
		Kind:    "free_text",
		TraitFor: map[string]string{
			"*": "user recently finished: %s",
		},
		Tags: []string{"reflective"},
	},
	{
		ID:      "current_blocker",
		Text:    "Is there anything blocking you right now?",
		Kind:    "free_text",
		TraitFor: map[string]string{
			"*": "user is currently blocked on: %s",
		},
		Tags: []string{"reflective"},
	},
	{
		ID:      "energy_level",
		Text:    "Where's your energy level at right now?",
		Kind:    "choice",
		Choices: []string{"Full of beans", "Steady", "Running low", "Empty"},
		TraitFor: map[string]string{
			"Full of beans": "user is feeling energetic right now",
			"Steady":        "user has steady energy right now",
			"Running low":   "user's energy is running low",
			"Empty":         "user is running on empty",
		},
		Tags: []string{"reflective"},
	},
	{
		ID:      "looking_forward_to",
		Text:    "What are you looking forward to this week?",
		Kind:    "free_text",
		TraitFor: map[string]string{
			"*": "user is looking forward to %s this week",
		},
		Tags: []string{"reflective"},
	},
	{
		ID:      "wins_this_week",
		Text:    "Small win to brag about? Anything counts.",
		Kind:    "free_text",
		TraitFor: map[string]string{
			"*": "user had a recent win: %s",
		},
		Tags: []string{"reflective"},
	},
	{
		ID:      "weekend_plans",
		Text:    "Got any plans for the weekend or just resting?",
		Kind:    "choice",
		Choices: []string{"Resting", "Going out", "Side projects", "Travelling", "Haven't decided"},
		TraitFor: map[string]string{
			"Resting":          "user likes to rest on weekends",
			"Going out":        "user likes going out on weekends",
			"Side projects":    "user spends weekends on side projects",
			"Travelling":       "user is travelling this weekend",
			"Haven't decided":  "user keeps weekend plans loose",
		},
		Tags: []string{"reflective"},
	},
	{
		ID:      "comfort_food",
		Text:    "What's your comfort food when nothing else fits?",
		Kind:    "free_text",
		TraitFor: map[string]string{
			"*": "user's comfort food is %s",
		},
		Tags: []string{"reflective"},
	},
	{
		ID:      "last_thing_made_you_laugh",
		Text:    "What's the last thing that made you genuinely laugh?",
		Kind:    "free_text",
		TraitFor: map[string]string{
			"*": "user recently laughed at: %s",
		},
		Tags: []string{"reflective"},
	},
}
