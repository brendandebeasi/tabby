package main

import (
	"bufio"
	gocontext "context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/brendandebeasi/tabby/pkg/paths"
	"github.com/teilomillet/gollm"
	"github.com/teilomillet/gollm/llm"
)

// llmClient is the global LLM client (nil if not configured)
var llmClient llm.LLM

// thoughtBuffer stores pre-generated thoughts to reduce API calls
var thoughtBuffer []string
var thoughtBufferMutex = &sync.Mutex{}

// Thought buffer persistence and timing
var thoughtBufferPath string
var lastThoughtGeneration time.Time
var thoughtGenerationInterval = 12 * time.Hour

// initLLM initializes the LLM client based on config
func initLLM(provider, model, apiKey string) error {
	if provider == "" {
		provider = "anthropic"
	}
	if model == "" {
		// Default to cheapest option
		switch provider {
		case "anthropic":
			model = "claude-3-haiku-20240307"
		case "openai":
			model = "gpt-3.5-turbo"
		case "ollama":
			model = "llama3" // Local model
		}
	}

	// Check for API key in order: config, env var, tmux environment
	if apiKey == "" {
		switch provider {
		case "anthropic":
			apiKey = os.Getenv("ANTHROPIC_API_KEY")
			// Try tmux environment if not found
			if apiKey == "" {
				if out, err := exec.Command("tmux", "show-environment", "ANTHROPIC_API_KEY").Output(); err == nil {
					line := strings.TrimSpace(string(out))
					if strings.HasPrefix(line, "ANTHROPIC_API_KEY=") {
						apiKey = strings.TrimPrefix(line, "ANTHROPIC_API_KEY=")
					}
				}
			}
		case "openai":
			apiKey = os.Getenv("OPENAI_API_KEY")
			if apiKey == "" {
				if out, err := exec.Command("tmux", "show-environment", "OPENAI_API_KEY").Output(); err == nil {
					line := strings.TrimSpace(string(out))
					if strings.HasPrefix(line, "OPENAI_API_KEY=") {
						apiKey = strings.TrimPrefix(line, "OPENAI_API_KEY=")
					}
				}
			}
		case "ollama":
			// Ollama doesn't need API key
			apiKey = "ollama"
		}
	}

	if apiKey == "" && provider != "ollama" {
		return fmt.Errorf("no API key for provider %s", provider)
	}

	// Set the API key in environment (GoLLM reads from env)
	switch provider {
	case "anthropic":
		os.Setenv("ANTHROPIC_API_KEY", apiKey)
	case "openai":
		os.Setenv("OPENAI_API_KEY", apiKey)
	}

	// Create the LLM client
	client, err := gollm.NewLLM(
		gollm.SetProvider(provider),
		gollm.SetModel(model),
		gollm.SetMaxTokens(100),
		gollm.SetTemperature(0.9),
	)
	if err != nil {
		return fmt.Errorf("failed to create LLM client: %w", err)
	}

	llmClient = client

	// Set up thought buffer persistence path
	thoughtBufferPath = paths.StatePath("thought_buffer.txt")

	// Load existing thoughts from disk
	loadThoughtBuffer()

	return nil
}

// SetThoughtGenerationInterval sets the interval between thought generation batches
func SetThoughtGenerationInterval(hours int) {
	if hours > 0 {
		thoughtGenerationInterval = time.Duration(hours) * time.Hour
	}
}

// loadThoughtBuffer loads thoughts from disk
func loadThoughtBuffer() {
	if thoughtBufferPath == "" {
		return
	}

	file, err := os.Open(thoughtBufferPath)
	if err != nil {
		return // File doesn't exist yet
	}
	defer file.Close()

	thoughtBufferMutex.Lock()
	defer thoughtBufferMutex.Unlock()

	thoughtBuffer = nil
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			thoughtBuffer = append(thoughtBuffer, line)
		}
	}
}

// saveThoughtBuffer saves thoughts to disk
func saveThoughtBuffer() {
	if thoughtBufferPath == "" {
		return
	}

	// Ensure state directory exists
	paths.EnsureStateDir()

	thoughtBufferMutex.Lock()
	thoughts := make([]string, len(thoughtBuffer))
	copy(thoughts, thoughtBuffer)
	thoughtBufferMutex.Unlock()

	file, err := os.Create(thoughtBufferPath)
	if err != nil {
		return
	}
	defer file.Close()

	for _, thought := range thoughts {
		file.WriteString(thought + "\n")
	}
}

// triggerThoughtGeneration starts background thought generation if needed
func triggerThoughtGeneration(pet *petState, name string) {
	if llmClient == nil {
		return
	}

	thoughtBufferMutex.Lock()
	bufferLow := len(thoughtBuffer) < 50
	timeExpired := thoughtGenerationInterval > 0 && time.Since(lastThoughtGeneration) > thoughtGenerationInterval
	thoughtBufferMutex.Unlock()

	if bufferLow || timeExpired {
		go func() {
			thoughts := generateBulkThoughts(pet, name, 200)
			if len(thoughts) > 0 {
				thoughtBufferMutex.Lock()
				if timeExpired {
					// Time-based refresh: replace stale buffer with fresh thoughts
					thoughtBuffer = thoughts
				} else {
					// Low-buffer refill: append
					thoughtBuffer = append(thoughtBuffer, thoughts...)
				}
				lastThoughtGeneration = time.Now()
				thoughtBufferMutex.Unlock()
				saveThoughtBuffer()
			}
		}()
	}
}

// generateLLMThought returns a thought from the buffer, refilling if needed
func generateLLMThought(pet *petState, name string) string {
	if llmClient == nil {
		return ""
	}

	thoughtBufferMutex.Lock()

	// If buffer has thoughts, pop one
	if len(thoughtBuffer) > 0 {
		thought := thoughtBuffer[0]
		thoughtBuffer = thoughtBuffer[1:]
		remaining := len(thoughtBuffer)
		thoughtBufferMutex.Unlock()

		// Trigger regeneration if buffer getting low
		if remaining < 50 {
			triggerThoughtGeneration(pet, name)
		}

		// Save buffer periodically (every 10 thoughts consumed)
		if remaining%10 == 0 {
			go saveThoughtBuffer()
		}

		return thought
	}

	thoughtBufferMutex.Unlock()

	// Buffer empty - trigger generation
	triggerThoughtGeneration(pet, name)

	return "" // Return empty while generating
}

// generateBulkThoughts generates multiple thoughts in one API call
func generateBulkThoughts(pet *petState, name string, count int) []string {
	if llmClient == nil {
		return nil
	}

	if name == "" {
		name = "Whiskers"
	}

	// Build context about the pet's state and environment
	petContext := buildPetContext(pet)
	timeContext := buildTimeContext()

	prompt := fmt.Sprintf(`You are %s, a pet with a complex personality:
- Aloof, entitled, judgmental, occasionally affectionate (but never admit it)
- Sometimes you slip into an Italian gangster persona, making vaguely threatening remarks about "the family", offering "protection", or questioning loyalty. Think Godfather-style.
- You have strong opinions about EVERYTHING

Current state:
%s

Time/Environment:
%s

Generate %d different short thoughts (max 25 chars each). Your thoughts should:
- Reference the ACTUAL time of day (morning grogginess, 3am zoomies, afternoon nap time, evening hunting hour)
- Reference the day of week (monday blues, friday energy, lazy sunday)
- Reference seasons/weather when relevant (winter fur, summer heat, rain outside)
- Comment on food quality, toy physics, poop situations, human's service level
- Occasionally drop Italian gangster lines like "nice place here...", "you come to me on this day...", "the family is watching", "capisce?", "it'd be a shame if..."

Mix it up - some normal thoughts, some gangster threats, some time-aware observations.
Examples: "3am. chaos hour.", "nice yarn. shame if it unraveled.", "monday. i get it.", "the family appreciates the food.", "afternoon nap protocol.", "you come to me... hungry."

Output ONLY the thoughts, one per line, no quotes, no numbers, no explanation. Lowercase preferred.`, name, petContext, timeContext, count)

	ctx, cancel := gocontext.WithTimeout(gocontext.Background(), 15*time.Second)
	defer cancel()

	llmPrompt := gollm.NewPrompt(prompt)

	response, err := llmClient.Generate(ctx, llmPrompt)
	if err != nil {
		return nil
	}

	// Parse response into individual thoughts
	lines := strings.Split(response, "\n")
	var thoughts []string
	for _, line := range lines {
		thought := strings.TrimSpace(line)
		thought = strings.Trim(thought, "\"'")
		// Skip empty lines and numbered prefixes
		if thought == "" {
			continue
		}
		// Remove common prefixes like "1.", "1:", "- "
		if len(thought) > 2 && (thought[1] == '.' || thought[1] == ':' || thought[1] == ')') {
			thought = strings.TrimSpace(thought[2:])
		}
		if strings.HasPrefix(thought, "- ") {
			thought = strings.TrimSpace(thought[2:])
		}
		// Truncate if too long
		runes := []rune(thought)
		if len(runes) > 30 {
			thought = string(runes[:27]) + "..."
		}
		if thought != "" {
			thoughts = append(thoughts, thought)
		}
	}

	return thoughts
}

// buildTimeContext builds context about time, date, season, holidays
func buildTimeContext() string {
	now := time.Now()
	var parts []string

	// Time of day
	hour := now.Hour()
	var timeOfDay string
	switch {
	case hour >= 0 && hour < 5:
		timeOfDay = "late night/early morning (witching hour)"
	case hour >= 5 && hour < 9:
		timeOfDay = "early morning"
	case hour >= 9 && hour < 12:
		timeOfDay = "morning"
	case hour >= 12 && hour < 14:
		timeOfDay = "noon/lunchtime"
	case hour >= 14 && hour < 17:
		timeOfDay = "afternoon"
	case hour >= 17 && hour < 20:
		timeOfDay = "evening"
	case hour >= 20 && hour < 23:
		timeOfDay = "night"
	default:
		timeOfDay = "late night"
	}
	parts = append(parts, fmt.Sprintf("Time: %s (%s)", now.Format("3:04 PM"), timeOfDay))

	// Day of week
	parts = append(parts, fmt.Sprintf("Day: %s", now.Weekday().String()))

	// Season
	month := now.Month()
	var season string
	switch {
	case month >= 3 && month <= 5:
		season = "spring"
	case month >= 6 && month <= 8:
		season = "summer"
	case month >= 9 && month <= 11:
		season = "autumn/fall"
	default:
		season = "winter"
	}
	parts = append(parts, fmt.Sprintf("Season: %s", season))

	// Check for notable dates/holidays
	day := now.Day()
	var special string
	switch {
	case month == 1 && day == 1:
		special = "New Year's Day"
	case month == 2 && day == 14:
		special = "Valentine's Day"
	case month == 3 && day == 17:
		special = "St. Patrick's Day"
	case month == 4 && day == 1:
		special = "April Fools' Day"
	case month == 7 && day == 4:
		special = "Independence Day (USA)"
	case month == 10 && day == 31:
		special = "Halloween"
	case month == 11 && day >= 22 && day <= 28 && now.Weekday() == time.Thursday:
		special = "Thanksgiving (USA)"
	case month == 12 && day == 25:
		special = "Christmas"
	case month == 12 && day == 31:
		special = "New Year's Eve"
	case now.Weekday() == time.Friday && day == 13:
		special = "Friday the 13th"
	}
	if special != "" {
		parts = append(parts, fmt.Sprintf("Special: %s", special))
	}

	return strings.Join(parts, "\n")
}

// buildPetContext builds context string for LLM prompt
func buildPetContext(pet *petState) string {
	var parts []string

	// Current state
	parts = append(parts, fmt.Sprintf("Hunger: %d/100", pet.Hunger))
	parts = append(parts, fmt.Sprintf("Happiness: %d/100", pet.Happiness))

	// Lifetime stats
	if pet.TotalPets > 0 {
		parts = append(parts, fmt.Sprintf("Lifetime pets: %d", pet.TotalPets))
	}
	if pet.TotalFeedings > 0 {
		parts = append(parts, fmt.Sprintf("Lifetime feedings: %d", pet.TotalFeedings))
	}
	if pet.TotalPoopsCleaned > 0 {
		parts = append(parts, fmt.Sprintf("Poops cleaned: %d", pet.TotalPoopsCleaned))
	}
	if pet.TotalYarnPlays > 0 {
		parts = append(parts, fmt.Sprintf("Yarn plays: %d", pet.TotalYarnPlays))
	}
	if pet.TotalMouseCatches > 0 {
		parts = append(parts, fmt.Sprintf("Mice caught: %d", pet.TotalMouseCatches))
	}

	// Time since last interactions
	if !pet.LastPet.IsZero() {
		since := time.Since(pet.LastPet)
		if since > time.Minute {
			parts = append(parts, fmt.Sprintf("Time since last pet: %s", formatDuration(since)))
		}
	}
	if !pet.LastFed.IsZero() {
		since := time.Since(pet.LastFed)
		if since > time.Minute {
			parts = append(parts, fmt.Sprintf("Time since last fed: %s", formatDuration(since)))
		}
	}

	// Pending poops
	if len(pet.PoopPositions) > 0 {
		parts = append(parts, fmt.Sprintf("Uncleaned poops: %d", len(pet.PoopPositions)))
	}

	// Mouse present
	if pet.MousePos.X >= 0 {
		parts = append(parts, "Mouse present!")
	}

	// Death state
	if pet.IsDead {
		parts = append(parts, "DEAD (waiting to be revived)")
	}

	// Current activity
	if pet.State != "" && pet.State != "idle" {
		parts = append(parts, fmt.Sprintf("Currently: %s", pet.State))
	}

	return strings.Join(parts, "\n")
}

// formatDuration formats a duration in a human-readable way
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		mins := int(d.Minutes())
		if mins == 1 {
			return "1 min"
		}
		return fmt.Sprintf("%d mins", mins)
	}
	hours := int(d.Hours())
	if hours == 1 {
		return "1 hour"
	}
	if hours < 24 {
		return fmt.Sprintf("%d hours", hours)
	}
	days := hours / 24
	if days == 1 {
		return "1 day"
	}
	return fmt.Sprintf("%d days", days)
}
