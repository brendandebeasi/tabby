package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/teilomillet/gollm"
	"github.com/teilomillet/gollm/llm"
	"github.com/brendandebeasi/tabby/pkg/daemon"
)

var (
	sessionID   = flag.String("session", "", "tmux session ID")
	llmProvider = flag.String("llm-provider", "anthropic", "LLM provider (anthropic, openai, ollama)")
	llmModel    = flag.String("llm-model", "claude-3-haiku-20240307", "LLM model")
	llmEnabled  = flag.Bool("llm-enabled", false, "Enable LLM for pet thoughts")
	debug       = flag.Bool("debug", false, "Enable debug logging")
)

var (
	llmClient       llm.LLM
	thoughtBuffer   []string
	thoughtMutex    sync.Mutex
	debugLog        *log.Logger
)

func main() {
	flag.Parse()

	if *debug {
		debugLog = log.New(os.Stderr, "[daemon] ", log.LstdFlags|log.Lmicroseconds)
	} else {
		debugLog = log.New(os.Stderr, "", 0)
	}

	// Get session ID from environment if not provided
	if *sessionID == "" {
		out, err := exec.Command("tmux", "display-message", "-p", "#{session_id}").Output()
		if err == nil {
			*sessionID = strings.TrimSpace(string(out))
		}
	}

	debugLog.Printf("Starting daemon for session %s", *sessionID)

	// Initialize LLM if enabled
	if *llmEnabled {
		if err := initLLM(*llmProvider, *llmModel); err != nil {
			debugLog.Printf("LLM init failed: %v", err)
		} else {
			debugLog.Printf("LLM initialized successfully")
		}
	}

	// Create coordinator for centralized rendering
	coordinator := NewCoordinator(*sessionID)

	// Create server
	server := daemon.NewServer(*sessionID)

	// Set up update callbacks
	server.OnGitUpdate = updateGitStatus
	server.OnStatsUpdate = updateStats

	// Set up render callback using coordinator
	server.OnRenderNeeded = func(clientID string, width, height int) *daemon.RenderPayload {
		// Set color profile based on minimum client capability
		SetColorProfile(server.GetMinColorProfile())
		return coordinator.RenderForClient(clientID, width, height)
	}

	// Set up input callback
	server.OnInput = func(clientID string, input *daemon.InputPayload) {
		coordinator.HandleInput(clientID, input)
		// Re-render after handling input
		server.BroadcastRender()
	}

	// Start server
	if err := server.Start(); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
	debugLog.Printf("Server listening on %s", daemon.SocketPath(*sessionID))

	// Start update loops
	go server.RunUpdateLoop(5*time.Second, 2*time.Second)

	// Start coordinator refresh loop
	go func() {
		refreshTicker := time.NewTicker(500 * time.Millisecond)  // Window list
		spinnerTicker := time.NewTicker(100 * time.Millisecond) // Spinner animation
		gitTicker := time.NewTicker(5 * time.Second)            // Git status
		petTicker := time.NewTicker(2 * time.Second)            // Pet state updates
		defer refreshTicker.Stop()
		defer spinnerTicker.Stop()
		defer gitTicker.Stop()
		defer petTicker.Stop()

		for {
			select {
			case <-refreshTicker.C:
				coordinator.RefreshWindows()
				server.BroadcastRender()
			case <-spinnerTicker.C:
				coordinator.IncrementSpinner()
				server.BroadcastRender()
			case <-gitTicker.C:
				coordinator.RefreshGit()
				coordinator.RefreshSession()
				server.BroadcastRender()
			case <-petTicker.C:
				coordinator.UpdatePetState()
				server.BroadcastRender()
			}
		}
	}()

	// Start LLM thought generation if enabled
	if *llmEnabled && llmClient != nil {
		go runThoughtLoop(server)
	}

	// Handle signals for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	// Monitor for idle shutdown (no clients for 30s)
	go func() {
		idleTicker := time.NewTicker(10 * time.Second)
		defer idleTicker.Stop()
		idleStart := time.Time{}

		for {
			select {
			case <-idleTicker.C:
				if server.ClientCount() == 0 {
					if idleStart.IsZero() {
						idleStart = time.Now()
					} else if time.Since(idleStart) > 30*time.Second {
						debugLog.Printf("No clients for 30s, shutting down")
						sigCh <- syscall.SIGTERM
						return
					}
				} else {
					idleStart = time.Time{}
				}
			}
		}
	}()

	<-sigCh
	debugLog.Printf("Shutting down daemon")
	server.Stop()
}

// initLLM initializes the LLM client
func initLLM(provider, model string) error {
	// Check for API key
	var apiKey string
	switch provider {
	case "anthropic":
		apiKey = os.Getenv("ANTHROPIC_API_KEY")
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
		apiKey = "ollama"
	}

	if apiKey == "" && provider != "ollama" {
		return fmt.Errorf("no API key for provider %s", provider)
	}

	// Set the API key in environment
	switch provider {
	case "anthropic":
		os.Setenv("ANTHROPIC_API_KEY", apiKey)
	case "openai":
		os.Setenv("OPENAI_API_KEY", apiKey)
	}

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
	return nil
}

// runThoughtLoop periodically generates new thoughts
func runThoughtLoop(server *daemon.Server) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	// Generate initial batch
	generateThoughts(server)

	for {
		select {
		case <-ticker.C:
			if server.GetThoughtCount() < 3 {
				generateThoughts(server)
			}
		}
	}
}

// generateThoughts generates a batch of thoughts via LLM
func generateThoughts(server *daemon.Server) {
	if llmClient == nil {
		return
	}

	now := time.Now()
	timeContext := buildTimeContext(now)

	prompt := fmt.Sprintf(`You are Whiskers, a cat with a complex personality:
- Aloof, entitled, judgmental, occasionally affectionate (but never admit it)
- Sometimes you slip into an Italian gangster persona, making vaguely threatening remarks about "the family", offering "protection", or questioning loyalty. Think Godfather-style cat.
- You have strong opinions about EVERYTHING

Time/Environment:
%s

Generate 5 different short thoughts (max 25 chars each). Your thoughts should:
- Reference the ACTUAL time of day (morning grogginess, 3am zoomies, afternoon nap time, evening hunting hour)
- Reference the day of week (monday blues, friday energy, lazy sunday)
- Reference seasons/weather when relevant (winter fur, summer heat, rain outside)
- Comment on food quality, yarn physics, poop situations, human's service level
- Occasionally drop Italian gangster lines like "nice place here...", "you come to me on this day...", "the family is watching", "capisce?", "it'd be a shame if..."

Mix it up - some normal cat thoughts, some gangster cat threats, some time-aware observations.
Examples: "3am. chaos hour.", "nice yarn. shame if it unraveled.", "monday. i get it.", "the family appreciates the food.", "afternoon nap protocol.", "you come to me... hungry."

Output ONLY the thoughts, one per line, no quotes, no numbers, no explanation. Lowercase preferred.`, timeContext)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	llmPrompt := gollm.NewPrompt(prompt)
	response, err := llmClient.Generate(ctx, llmPrompt)
	if err != nil {
		debugLog.Printf("LLM error: %v", err)
		return
	}

	// Parse response into individual thoughts
	lines := strings.Split(response, "\n")
	var thoughts []string
	for _, line := range lines {
		thought := strings.TrimSpace(line)
		thought = strings.Trim(thought, "\"'")
		if thought == "" {
			continue
		}
		// Remove common prefixes
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

	if len(thoughts) > 0 {
		server.SetThoughts(thoughts)
		debugLog.Printf("Generated %d thoughts", len(thoughts))
	}
}

// buildTimeContext builds context about time, date, season
func buildTimeContext(now time.Time) string {
	var parts []string

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
	parts = append(parts, fmt.Sprintf("Day: %s", now.Weekday().String()))

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

	return strings.Join(parts, "\n")
}

// updateGitStatus gets current git status
func updateGitStatus() *daemon.GitState {
	state := &daemon.GitState{LastUpdate: time.Now()}

	// Check if in git repo
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	if err := cmd.Run(); err != nil {
		state.IsRepo = false
		return state
	}
	state.IsRepo = true

	// Get branch name
	cmd = exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	if out, err := cmd.Output(); err == nil {
		state.Branch = strings.TrimSpace(string(out))
	}

	// Get status counts
	cmd = exec.Command("git", "status", "--porcelain")
	if out, err := cmd.Output(); err == nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		for _, line := range lines {
			if len(line) < 2 {
				continue
			}
			x, y := line[0], line[1]
			if x == '?' && y == '?' {
				state.Untracked++
			} else if x != ' ' {
				state.Staged++
			} else if y != ' ' {
				state.Unstaged++
			}
		}
		state.IsDirty = state.Staged > 0 || state.Unstaged > 0 || state.Untracked > 0
	}

	// Get ahead/behind
	cmd = exec.Command("git", "rev-list", "--left-right", "--count", "HEAD...@{upstream}")
	if out, err := cmd.Output(); err == nil {
		parts := strings.Fields(string(out))
		if len(parts) >= 2 {
			state.Ahead, _ = strconv.Atoi(parts[0])
			state.Behind, _ = strconv.Atoi(parts[1])
		}
	}

	// Get stash count
	cmd = exec.Command("git", "stash", "list")
	if out, err := cmd.Output(); err == nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if lines[0] != "" {
			state.Stashes = len(lines)
		}
	}

	return state
}

// updateStats gets current system stats
func updateStats() *daemon.StatsState {
	state := &daemon.StatsState{LastUpdate: time.Now()}

	switch runtime.GOOS {
	case "darwin":
		updateStatsMacOS(state)
	case "linux":
		updateStatsLinux(state)
	}

	return state
}

func updateStatsMacOS(state *daemon.StatsState) {
	// CPU and memory via sysctl
	cmd := exec.Command("sysctl", "-n", "vm.loadavg", "hw.ncpu", "hw.memsize")
	if out, err := cmd.Output(); err == nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if len(lines) >= 3 {
			loadStr := strings.Trim(lines[0], "{ }")
			parts := strings.Fields(loadStr)
			if len(parts) >= 1 {
				load1, _ := strconv.ParseFloat(parts[0], 64)
				ncpu, _ := strconv.Atoi(strings.TrimSpace(lines[1]))
				if ncpu > 0 {
					state.CPUPercent = (load1 / float64(ncpu)) * 100
					if state.CPUPercent > 100 {
						state.CPUPercent = 100
					}
				}
			}
			memBytes, _ := strconv.ParseInt(strings.TrimSpace(lines[2]), 10, 64)
			state.MemoryTotal = float64(memBytes) / (1024 * 1024 * 1024)
		}
	}

	// Battery
	cmd = exec.Command("pmset", "-g", "batt")
	if out, err := cmd.Output(); err == nil {
		lines := strings.Split(string(out), "\n")
		for _, line := range lines {
			if strings.Contains(line, "InternalBattery") || strings.Contains(line, "Battery") {
				re := regexp.MustCompile(`(\d+)%`)
				if matches := re.FindStringSubmatch(line); len(matches) > 1 {
					state.BatteryPercent, _ = strconv.Atoi(matches[1])
				}
				if strings.Contains(line, "charging") {
					state.BatteryStatus = "charging"
				} else if strings.Contains(line, "discharging") {
					state.BatteryStatus = "discharging"
				} else if strings.Contains(line, "charged") {
					state.BatteryStatus = "full"
				}
				break
			}
		}
	}
}

func updateStatsLinux(state *daemon.StatsState) {
	// CPU via /proc/stat
	cmd := exec.Command("grep", "cpu ", "/proc/stat")
	if out, err := cmd.Output(); err == nil {
		var user, nice, system, idle, iowait, irq, softirq int64
		fmt.Sscanf(string(out), "cpu %d %d %d %d %d %d %d",
			&user, &nice, &system, &idle, &iowait, &irq, &softirq)
		total := float64(user + nice + system + idle + iowait + irq + softirq)
		if total > 0 {
			state.CPUPercent = 100 * (1 - float64(idle)/total)
		}
	}

	// Memory via /proc/meminfo
	cmd = exec.Command("grep", "-E", "^(MemTotal|MemAvailable):", "/proc/meminfo")
	if out, err := cmd.Output(); err == nil {
		lines := strings.Split(string(out), "\n")
		var memTotal, memAvailable int64
		for _, line := range lines {
			if strings.HasPrefix(line, "MemTotal:") {
				fmt.Sscanf(line, "MemTotal: %d kB", &memTotal)
			} else if strings.HasPrefix(line, "MemAvailable:") {
				fmt.Sscanf(line, "MemAvailable: %d kB", &memAvailable)
			}
		}
		state.MemoryTotal = float64(memTotal) / (1024 * 1024)
		state.MemoryUsed = float64(memTotal-memAvailable) / (1024 * 1024)
		if state.MemoryTotal > 0 {
			state.MemoryPercent = (state.MemoryUsed / state.MemoryTotal) * 100
		}
	}

	// Battery
	cmd = exec.Command("cat", "/sys/class/power_supply/BAT0/capacity")
	if out, err := cmd.Output(); err == nil {
		state.BatteryPercent, _ = strconv.Atoi(strings.TrimSpace(string(out)))
	}
	cmd = exec.Command("cat", "/sys/class/power_supply/BAT0/status")
	if out, err := cmd.Output(); err == nil {
		status := strings.TrimSpace(strings.ToLower(string(out)))
		state.BatteryStatus = status
	}
}

