# Test Suite Overhaul Plan

**Status**: Analysis Complete | Ready for Phase 1 Implementation  
**Date**: March 21, 2026  
**Scope**: Comprehensive testability assessment of Tabby tmux plugin codebase  
**Deliverable**: Phased implementation roadmap for 445-hour test suite overhaul

---

## Table of Contents

1. [Executive Summary](#executive-summary)
2. [Testability Matrix](#testability-matrix)
3. [Low-Hanging Fruit (Phase 1)](#low-hanging-fruit-phase-1)
4. [Mocking Strategy](#mocking-strategy)
5. [Refactoring Prerequisites (Phase 3a)](#refactoring-prerequisites-phase-3a)
6. [Test Pattern Standardization](#test-pattern-standardization)
7. [Phase 1–3 Implementation Breakdown](#phase-13-implementation-breakdown)
8. [Effort Estimates](#effort-estimates)
9. [Risk Assessment](#risk-assessment)
10. [Appendix: File Paths & Line Ranges](#appendix-file-paths--line-ranges)

---

## Executive Summary

### Current State

Tabby has **14 test files** across 8 packages with **table-driven patterns** and **excellent isolation practices** (t.TempDir(), t.Setenv(), ResetForTest()). However, **coverage is fragmented**:

- **High coverage**: colors (40%), paths (35%), grouping (30%)
- **Low coverage**: config (10%), daemon (5%), coordinator (2%), windows (0%)
- **No mocking framework** in use (opportunity for testify/mock)
- **No interface-based DI** (callback-based pattern in daemon/server.go)

### Critical Barriers

1. **Monolithic coordinator.go** (11,321 lines, 177 fields, 136+ methods)
   - Blocks unit testing of individual features
   - Requires refactoring into 4 modules (windows, ai_detection, pet, config_mgmt)
   - Estimated effort: 40 hours module split + 150 hours unit tests

2. **Direct tmux coupling** (50+ exec.Command calls in windows.go and coordinator.go)
   - No TmuxRunner interface abstraction
   - Blocks mocking of tmux interactions
   - Requires Phase 3a interface extraction

3. **File I/O without abstraction** (pet state, CWD colors, state files)
   - No FileWriter interface
   - Blocks mocking of persistence layer
   - Requires Phase 3a interface extraction

4. **Mutable global state** (prevPaneBusy, hookPaneActive, collapsedGroups maps)
   - Risk of test interference
   - Requires explicit reset between tests
   - Documented in Phase 1 test helpers

5. **Lock discipline complexity** (RWMutex in Coordinator)
   - Careful timing required to avoid deadlocks
   - Requires go-deadlock tool in CI
   - Requires careful test design in Phase 3

### Recommended Path Forward

**Phase 1 (2 weeks, 78 hours)**: High-ROI packages with no refactoring needed
- colors, paths, grouping, config → 80%+ coverage

**Phase 2 (3 weeks, 117 hours)**: Daemon and renderer packages with callback-based DI
- daemon/server, pane-header, sidebar-renderer, manage-group → 60%+ coverage

**Phase 3 (6+ weeks, 230 hours)**: Coordinator refactoring + comprehensive unit tests
- Interface extraction (Phase 3a) → Module split (Phase 3b) → Unit tests (Phase 3c)
- coordinator modules → 70%+ coverage, windows → 50%+ coverage

**Total effort**: 445 hours (realistic: 240–280 hours at 40h/week)  
**Timeline**: 12+ weeks (6–7 weeks at 40h/week)

---

## Testability Matrix

| Package | Lines | Current % | Target % | Barriers | Priority | Phase | Effort (hrs) |
|---------|-------|-----------|----------|----------|----------|-------|--------------|
| colors | 600 | 40% | 80% | None | HIGH | 1 | 20 |
| paths | 350 | 35% | 85% | None | HIGH | 1 | 15 |
| grouping | 350 | 30% | 75% | None | HIGH | 1 | 18 |
| config | 822 | 10% | 80% | None | HIGH | 1 | 25 |
| daemon/protocol | 207 | 0% | 80% | None | MEDIUM | 2 | 12 |
| daemon/server | 607 | 5% | 70% | Callback DI | MEDIUM | 2 | 30 |
| manage-group | 400 | 15% | 75% | Callback DI | MEDIUM | 2 | 15 |
| pane-header | 800 | 10% | 70% | Callback DI, tmux | MEDIUM | 2 | 28 |
| sidebar-renderer | 1200 | 15% | 70% | Callback DI, tmux | MEDIUM | 2 | 32 |
| tmux/windows | 698 | 0% | 50% | Direct tmux coupling | HIGH | 3 | 40 |
| coordinator | 11321 | 2% | 70% | Monolithic, tmux, file I/O, state | HIGH | 3 | 150 |
| **TOTAL** | **17,952** | **~12%** | **~65%** | — | — | — | **385** |

**Notes**:
- Effort estimates assume 20 hours per 1000 lines for new tests (Phase 1–2)
- Coordinator effort includes 40 hours module split + 150 hours unit tests
- Phase 3 effort is high due to refactoring prerequisite (interface extraction)
- Realistic total: 240–280 hours at 40h/week (6–7 weeks)

---

## Low-Hanging Fruit (Phase 1)

### Overview

Phase 1 targets 4 packages with **zero refactoring needed**, **excellent isolation practices**, and **high ROI**. These packages are **pure functions** or **simple state machines** with no external dependencies.

**Phase 1 goal**: 12% → 55% overall coverage in 2 weeks (78 hours)

### colors (600 lines, 40% → 80%, 20 hours)

**Current state**: 245 lines of tests, well-structured table-driven tests

**Coverage gaps**:
- Color blending functions (dimColor, desaturateHex) — 0% coverage
- Theme resolution (GetSidebarBg) — 0% coverage
- RGB/hex conversion edge cases — partial coverage

**Test additions** (20 hours):
1. **dimColor tests** (4 hours)
   - Opacity 0, 0.5, 1.0
   - Edge cases: invalid hex, out-of-range opacity
   - Verify brightness reduction

2. **desaturateHex tests** (4 hours)
   - Blend toward white, black, gray
   - Opacity 0, 0.5, 1.0
   - Edge cases: already desaturated colors

3. **GetSidebarBg tests** (6 hours)
   - Config override present/absent
   - Theme detection (light/dark)
   - Fallback to detector
   - Environment variable override

4. **hexToRGB edge cases** (3 hours)
   - 3-digit hex (#fff)
   - 6-digit hex (#ffffff)
   - Invalid formats
   - Case insensitivity

5. **Integration tests** (3 hours)
   - Color pipeline: hex → RGB → dim → desaturate → hex
   - Verify no data loss in round-trip

**Files to modify**:
- `/Users/b/git/tabby/pkg/colors/colors_test.go` (add 200+ lines)

---

### paths (350 lines, 35% → 85%, 15 hours)

**Current state**: 107 lines of tests, uses t.TempDir() and t.Setenv() correctly

**Coverage gaps**:
- XDG path expansion (TABBY_CONFIG_DIR, TABBY_STATE_DIR) — partial coverage
- Home directory expansion (~) — 0% coverage
- Fallback behavior (missing env vars) — partial coverage
- Path creation (MkdirAll) — 0% coverage

**Test additions** (15 hours):
1. **XDG path expansion tests** (5 hours)
   - TABBY_CONFIG_DIR set/unset
   - TABBY_STATE_DIR set/unset
   - Verify ~/.config/tabby and ~/.local/state/tabby fallbacks
   - Verify /tmp/tabby-* runtime paths

2. **Home directory expansion tests** (3 hours)
   - ~ expansion in paths
   - ~user expansion (if supported)
   - Edge cases: missing home directory

3. **Path creation tests** (4 hours)
   - MkdirAll with nested directories
   - Permissions (0755 for config, 0700 for state)
   - Idempotency (create twice, no error)

4. **Edge cases** (3 hours)
   - Empty env vars
   - Relative paths
   - Symlinks
   - Permission denied scenarios

**Files to modify**:
- `/Users/b/git/tabby/pkg/paths/paths_test.go` (add 150+ lines)

---

### grouping (350 lines, 30% → 75%, 18 hours)

**Current state**: 170 lines of tests, table-driven pattern

**Coverage gaps**:
- Color assignment logic (predefined + custom hex) — partial coverage
- Shade calculation (brightness reduction) — 0% coverage
- Pattern matching (window name grouping) — partial coverage
- Marker assignment (emoji picker) — 0% coverage

**Test additions** (18 hours):
1. **Color assignment tests** (5 hours)
   - Predefined colors (red, blue, green, etc.)
   - Custom hex colors
   - Invalid colors (fallback to default)
   - Shade calculation (brightness reduction)

2. **Pattern matching tests** (6 hours)
   - Regex patterns in config
   - Window name matching
   - Multiple patterns (first match wins)
   - Case sensitivity

3. **Marker assignment tests** (4 hours)
   - Emoji picker integration
   - Marker persistence
   - Marker reset

4. **Edge cases** (3 hours)
   - Empty group names
   - Duplicate group names
   - Special characters in names

**Files to modify**:
- `/Users/b/git/tabby/pkg/grouping/grouper_test.go` (add 180+ lines)

---

### config (822 lines, 10% → 80%, 25 hours)

**Current state**: 79 lines of tests, minimal coverage

**Coverage gaps**:
- LoadConfig (YAML parsing) — 0% coverage
- SaveConfig (YAML writing) — 0% coverage
- Config validation — 0% coverage
- Default values — partial coverage
- Environment variable overrides — 0% coverage

**Test additions** (25 hours):
1. **LoadConfig tests** (8 hours)
   - Valid YAML files
   - Missing config file (use defaults)
   - Invalid YAML (error handling)
   - Partial config (merge with defaults)
   - Environment variable overrides (TABBY_CONFIG_FILE)

2. **SaveConfig tests** (6 hours)
   - Write config to file
   - Preserve formatting
   - Idempotency (save twice, same result)
   - Permission handling

3. **Config validation tests** (6 hours)
   - Required fields present
   - Type validation (strings, numbers, booleans)
   - Enum validation (position: left/right, mode: full/partial)
   - Range validation (width: 10–50)

4. **Default values tests** (3 hours)
   - Verify all defaults are sensible
   - Verify defaults match documentation
   - Verify defaults work with all features

5. **Integration tests** (2 hours)
   - Load → modify → save → load cycle
   - Verify no data loss

**Files to modify**:
- `/Users/b/git/tabby/pkg/config/config_test.go` (add 300+ lines)
- Create `/Users/b/git/tabby/pkg/config/loader_test.go` (new file, 200+ lines)

---

### Phase 1 Summary

| Package | Current | Target | Tests Added | Hours | Week |
|---------|---------|--------|-------------|-------|------|
| colors | 40% | 80% | 200 lines | 20 | 1 |
| paths | 35% | 85% | 150 lines | 15 | 1 |
| grouping | 30% | 75% | 180 lines | 18 | 1 |
| config | 10% | 80% | 500 lines | 25 | 2 |
| **TOTAL** | **12%** | **55%** | **1,030 lines** | **78** | **2** |

**Success criteria**:
- All 4 packages reach target coverage
- All tests pass in CI
- No performance regression
- Documentation updated with test patterns

---

## Mocking Strategy

### Current State

- **No mocking framework** in use (opportunity for standardization)
- **Callback-based DI** in daemon/server.go (OnRenderNeeded, OnInput, OnDisconnect)
- **Direct tmux coupling** in windows.go and coordinator.go (50+ exec.Command calls)
- **File I/O without abstraction** (pet state, CWD colors, state files)

### Recommended Framework: testify/mock

**Why testify/mock**:
- Standard Go testing framework (used by major projects)
- Minimal boilerplate (no code generation)
- Works with table-driven tests
- Supports assertion chains and call verification
- Integrates with existing test patterns

**Alternative considered**: gock (HTTP mocking)
- Not applicable (Tabby doesn't use HTTP)
- Would require additional framework for tmux/file I/O

### Phase 1–2: Callback-Based DI (No Mocking Needed)

Daemon/server.go uses callback-based DI:

```go
type Server struct {
    OnRenderNeeded func(payload *RenderPayload) error
    OnInput        func(input *InputMessage) error
    OnDisconnect   func(clientID string) error
}
```

**Testing approach** (Phase 2):
- Create test callbacks that record calls
- Verify callbacks are invoked with correct arguments
- No mocking framework needed (simple function pointers)

**Example**:
```go
func TestServer_OnInput(t *testing.T) {
    var inputCalls []*InputMessage
    server := &Server{
        OnInput: func(input *InputMessage) error {
            inputCalls = append(inputCalls, input)
            return nil
        },
    }
    
    // Send input to server
    server.handleInput(&InputMessage{...})
    
    // Verify callback was invoked
    if len(inputCalls) != 1 {
        t.Errorf("expected 1 input call, got %d", len(inputCalls))
    }
}
```

### Phase 3a: Interface-Based Mocking (testify/mock)

**Interfaces to extract**:

1. **TmuxRunner** (Phase 3a, 20 hours)
   ```go
   type TmuxRunner interface {
       Run(ctx context.Context, args ...string) error
       Output(ctx context.Context, args ...string) (string, error)
       OutputCtx(ctx context.Context, args ...string) (string, error)
   }
   ```
   - Implement real version (exec.Command wrapper)
   - Implement mock version (testify/mock)
   - Replace 50+ exec.Command calls in windows.go and coordinator.go

2. **FileWriter** (Phase 3a, 15 hours)
   ```go
   type FileWriter interface {
       WriteFile(path string, data []byte, perm os.FileMode) error
       ReadFile(path string) ([]byte, error)
       MkdirAll(path string, perm os.FileMode) error
   }
   ```
   - Implement real version (os.WriteFile wrapper)
   - Implement mock version (testify/mock)
   - Replace file I/O in pet state, CWD colors, state files

3. **StateStore** (Phase 3a, 10 hours)
   ```go
   type StateStore interface {
       SavePetState(state *petState) error
       LoadPetState() (*petState, error)
       SaveCollapsedGroups(groups map[string]bool) error
       LoadCollapsedGroups() (map[string]bool, error)
   }
   ```
   - Implement real version (file-based)
   - Implement mock version (testify/mock)
   - Replace state persistence in coordinator.go

### Phase 2–3: Test Helper Functions

**Isolation techniques** (already in use, document as standard):

```go
// Phase 1–2: Filesystem isolation
func setupTestDirs(t *testing.T) (configDir, stateDir string) {
    configDir = t.TempDir()
    stateDir = t.TempDir()
    t.Setenv("TABBY_CONFIG_DIR", configDir)
    t.Setenv("TABBY_STATE_DIR", stateDir)
    return configDir, stateDir
}

// Phase 2–3: Mock tmux runner
func setupMockTmux(t *testing.T) *MockTmuxRunner {
    mock := &MockTmuxRunner{}
    mock.On("Output", mock.MatchedBy(func(ctx context.Context, args ...string) bool {
        return args[0] == "list-windows"
    })).Return("0: window1\n1: window2\n", nil)
    return mock
}

// Phase 2–3: Mock file writer
func setupMockFileWriter(t *testing.T) *MockFileWriter {
    mock := &MockFileWriter{}
    mock.On("WriteFile", mock.MatchedBy(func(path string, data []byte, perm os.FileMode) bool {
        return strings.Contains(path, "pet_state")
    })).Return(nil)
    return mock
}

// Phase 1–3: Module-level state reset
func ResetForTest() {
    prevPaneBusy = make(map[string]bool)
    hookPaneActive = make(map[string]bool)
    collapsedGroups = make(map[string]bool)
}
```

### Mocking Strategy Summary

| Phase | Framework | Approach | Effort |
|-------|-----------|----------|--------|
| 1 | None | Direct struct instantiation | 78 hrs |
| 2 | Callback DI | Function pointers, no mocking | 117 hrs |
| 3a | testify/mock | Interface extraction | 45 hrs |
| 3b | testify/mock | Module-level unit tests | 150 hrs |
| **Total** | — | — | **390 hrs** |

---

## Refactoring Prerequisites (Phase 3a)

### Overview

Coordinator.go (11,321 lines) is too large for unit testing. Phase 3a extracts interfaces and splits into 4 modules before Phase 3b unit tests.

**Phase 3a effort**: 60 hours (20 interface extraction + 40 module split)

### Module Split Proposal

#### Module 1: windows (2,000 lines)

**Responsibility**: Window/pane inventory, layout, input handlers

**Source lines**:
- Lines 1–366: Struct definitions (Coordinator, petState, etc.)
- Lines 3990–4500: computeVisualPositions, renderWindowList
- Lines 8164–8437: handleWindowClick, handleWindowMenu
- Lines 10209–10373: Pane context menu (rename, split, grow/shrink, lock)

**Methods to extract**:
- RefreshWindows()
- computeVisualPositions()
- renderWindowList()
- handleWindowClick()
- handleWindowMenu()
- showPaneContextMenu()
- showWindowContextMenu()
- selectContentPaneInActiveWindow()
- findWindowByTarget()

**Dependencies**:
- TmuxRunner interface (to be extracted)
- config.Config
- grouping.GroupedWindows

**Tests** (Phase 3c, 40 hours):
- Window inventory refresh
- Visual position computation
- Click handling (pane, window, menu)
- Context menu rendering
- Pane selection logic

---

#### Module 2: ai_detection (1,500 lines)

**Responsibility**: AI tool detection, state transitions, thought generation

**Source lines**:
- Lines 1478–1821: detectAITools, updateAIDetectionState
- Lines 1821–2100: AI state machine transitions
- Lines 10878–10948: triggerActionFromThought
- Lines 10950–10979: Default pet thoughts map

**Methods to extract**:
- detectAITools()
- updateAIDetectionState()
- triggerActionFromThought()
- getDefaultThoughts()
- parseThoughtKeywords()

**Dependencies**:
- TmuxRunner interface (to be extracted)
- config.Config
- petState (from pet module)

**Tests** (Phase 3c, 35 hours):
- AI tool detection (Claude, Cursor, etc.)
- State transitions (idle → thinking → done)
- Thought keyword parsing
- Action triggering (walk, jump, yarn, sleep)
- Default thoughts generation

---

#### Module 3: pet (3,500 lines)

**Responsibility**: Pet physics, state machine, widget rendering, sprites

**Source lines**:
- Lines 366–1478: petState definition, pet mechanics
- Lines 3357–3990: renderPetWidget, buildPetSprite
- Lines 4500–8164: Pet animation, physics, collision detection
- Lines 9256–9336: Pet click handling

**Methods to extract**:
- updatePetState()
- renderPetWidget()
- buildPetSprite()
- handlePetClick()
- updatePetPhysics()
- checkPetCollisions()
- updatePetAdventure()

**Dependencies**:
- config.Config
- lipgloss (rendering)
- ai_detection module (for thought triggering)

**Tests** (Phase 3c, 45 hours):
- Pet state machine (idle, walking, jumping, sleeping, dead)
- Physics simulation (gravity, velocity, collision)
- Adventure state transitions
- Sprite building and animation
- Click handling (pet interaction)
- Hunger/happiness mechanics

---

#### Module 4: config_mgmt (1,500 lines)

**Responsibility**: Config loading, state persistence, theme management, color utilities

**Source lines**:
- Lines 2100–2500: Config loading and validation
- Lines 8667–9000: Theme detection and color resolution
- Lines 10981–11165: Color utility functions (hexToRGB, dimColor, desaturateHex)
- Lines 11271–11321: GetSidebarBg, applyBackgroundFill

**Methods to extract**:
- LoadConfig()
- SaveConfig()
- GetSidebarBg()
- applyBackgroundFill()
- hexToRGB()
- dimColor()
- desaturateHex()
- GetHeaderColorsForPane()
- GetGitStateHash()

**Dependencies**:
- FileWriter interface (to be extracted)
- StateStore interface (to be extracted)
- config.Config
- colors package

**Tests** (Phase 3c, 30 hours):
- Config loading and validation
- State persistence (pet state, collapsed groups)
- Theme detection
- Color utilities (hex/RGB conversion, blending)
- Header color resolution
- Git state hashing

---

### Interface Extraction (Phase 3a, 20 hours)

#### TmuxRunner Interface

**Current usage** (50+ calls):
- windows.go: GetWindowList, GetPaneList, GetPaneCommand, GetPaneWorkingDir, etc.
- coordinator.go: RefreshWindows, handleWindowClick, handlePaneClick, etc.

**Interface definition**:
```go
type TmuxRunner interface {
    // Core operations
    Run(ctx context.Context, args ...string) error
    Output(ctx context.Context, args ...string) (string, error)
    OutputCtx(ctx context.Context, args ...string) (string, error)
    
    // Convenience methods
    GetWindowList(ctx context.Context) ([]Window, error)
    GetPaneList(ctx context.Context, windowID string) ([]Pane, error)
    GetPaneCommand(ctx context.Context, paneID string) (string, error)
    GetPaneWorkingDir(ctx context.Context, paneID string) (string, error)
}
```

**Implementation**:
- Real: `exec.Command("tmux", ...)` wrapper
- Mock: testify/mock with call recording

**Effort**: 10 hours (extract + implement real + implement mock)

---

#### FileWriter Interface

**Current usage** (15+ calls):
- coordinator.go: SavePetState, LoadPetState, SaveCollapsedGroups, LoadCollapsedGroups
- config.go: LoadConfig, SaveConfig

**Interface definition**:
```go
type FileWriter interface {
    WriteFile(path string, data []byte, perm os.FileMode) error
    ReadFile(path string) ([]byte, error)
    MkdirAll(path string, perm os.FileMode) error
    Stat(path string) (os.FileInfo, error)
    Remove(path string) error
}
```

**Implementation**:
- Real: os.WriteFile, os.ReadFile, os.MkdirAll wrappers
- Mock: testify/mock with call recording

**Effort**: 7 hours (extract + implement real + implement mock)

---

#### StateStore Interface

**Current usage** (8+ calls):
- coordinator.go: SavePetState, LoadPetState, SaveCollapsedGroups, LoadCollapsedGroups

**Interface definition**:
```go
type StateStore interface {
    SavePetState(state *petState) error
    LoadPetState() (*petState, error)
    SaveCollapsedGroups(groups map[string]bool) error
    LoadCollapsedGroups() (map[string]bool, error)
}
```

**Implementation**:
- Real: File-based (JSON serialization)
- Mock: testify/mock with call recording

**Effort**: 3 hours (extract + implement real + implement mock)

---

### Phase 3a Summary

| Task | Effort (hrs) | Dependencies |
|------|--------------|--------------|
| Extract TmuxRunner interface | 10 | None |
| Extract FileWriter interface | 7 | None |
| Extract StateStore interface | 3 | None |
| Split coordinator.go into 4 modules | 40 | All interfaces extracted |
| **TOTAL** | **60** | — |

**Success criteria**:
- All interfaces extracted and implemented (real + mock)
- coordinator.go split into 4 modules (windows, ai_detection, pet, config_mgmt)
- All existing tests still pass
- No performance regression
- Module dependencies documented

---

## Test Pattern Standardization

### Naming Convention

**Format**: `Test_<Package>_<Function>_<Scenario>`

**Examples**:
```go
func Test_colors_dimColor_OpacityZero(t *testing.T) { ... }
func Test_colors_dimColor_OpacityOne(t *testing.T) { ... }
func Test_colors_dimColor_InvalidHex(t *testing.T) { ... }

func Test_paths_ExpandConfigDir_WithEnvVar(t *testing.T) { ... }
func Test_paths_ExpandConfigDir_WithoutEnvVar(t *testing.T) { ... }

func Test_config_LoadConfig_ValidYAML(t *testing.T) { ... }
func Test_config_LoadConfig_MissingFile(t *testing.T) { ... }
func Test_config_LoadConfig_InvalidYAML(t *testing.T) { ... }
```

### Table-Driven Template

**Standard pattern** (already in use, document as required):

```go
func Test_<Package>_<Function>_<Scenario>(t *testing.T) {
    tests := []struct {
        name    string
        input   <InputType>
        want    <OutputType>
        wantErr bool
    }{
        {
            name:  "scenario 1",
            input: <value>,
            want:  <expected>,
        },
        {
            name:    "scenario 2 (error case)",
            input:   <value>,
            wantErr: true,
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got, err := <Function>(tt.input)
            if (err != nil) != tt.wantErr {
                t.Errorf("wantErr %v, got %v", tt.wantErr, err)
            }
            if !tt.wantErr && got != tt.want {
                t.Errorf("got %v, want %v", got, tt.want)
            }
        })
    }
}
```

### Helper Functions (Phase 1–3)

**Filesystem isolation** (Phase 1):
```go
func setupTestDirs(t *testing.T) (configDir, stateDir string) {
    configDir = t.TempDir()
    stateDir = t.TempDir()
    t.Setenv("TABBY_CONFIG_DIR", configDir)
    t.Setenv("TABBY_STATE_DIR", stateDir)
    return configDir, stateDir
}
```

**Mock tmux runner** (Phase 2–3):
```go
func setupMockTmux(t *testing.T) *MockTmuxRunner {
    mock := &MockTmuxRunner{}
    mock.On("Output", mock.MatchedBy(func(ctx context.Context, args ...string) bool {
        return args[0] == "list-windows"
    })).Return("0: window1\n1: window2\n", nil)
    return mock
}
```

**Mock file writer** (Phase 2–3):
```go
func setupMockFileWriter(t *testing.T) *MockFileWriter {
    mock := &MockFileWriter{}
    mock.On("WriteFile", mock.MatchedBy(func(path string, data []byte, perm os.FileMode) bool {
        return strings.Contains(path, "pet_state")
    })).Return(nil)
    return mock
}
```

**Module-level state reset** (Phase 1–3):
```go
func ResetForTest() {
    prevPaneBusy = make(map[string]bool)
    hookPaneActive = make(map[string]bool)
    collapsedGroups = make(map[string]bool)
}
```

### Fixture Files (Phase 2–3)

**Sidebar-renderer fixture** (already in use):
```bash
TABBY_PRINT_PICKER_FIXTURE=1 ./sidebar-renderer > fixtures/picker.txt
```

**Pane-header fixture** (to add):
```bash
TABBY_PRINT_HEADER_FIXTURE=1 ./pane-header > fixtures/header.txt
```

**Pet widget fixture** (to add):
```bash
TABBY_PRINT_PET_FIXTURE=1 ./tabby-daemon > fixtures/pet.txt
```

---

## Phase 1–3 Implementation Breakdown

### Phase 1: High-ROI Packages (2 weeks, 78 hours)

**Goal**: 12% → 55% overall coverage, establish test patterns

**Week 1** (40 hours):
- colors: 20 hours (dimColor, desaturateHex, GetSidebarBg, hexToRGB edge cases)
- paths: 15 hours (XDG expansion, home directory, path creation)
- grouping: 5 hours (setup, pattern matching tests)

**Week 2** (38 hours):
- grouping: 13 hours (color assignment, marker assignment, edge cases)
- config: 25 hours (LoadConfig, SaveConfig, validation, defaults)

**Deliverables**:
- 1,030 lines of new tests
- 4 packages at 75%+ coverage
- Test pattern documentation
- Helper functions (setupTestDirs, ResetForTest)

**Success criteria**:
- All tests pass in CI
- Coverage reports generated
- No performance regression
- Code review feedback incorporated

---

### Phase 2: Daemon and Renderer Packages (3 weeks, 117 hours)

**Goal**: 55% → 65% overall coverage, introduce callback-based DI testing

**Week 1** (42 hours):
- daemon/protocol: 12 hours (message types, payloads, utility functions)
- daemon/server: 30 hours (ClientInfo, Server struct, callbacks, socket management)

**Week 2** (45 hours):
- pane-header: 28 hours (input client, modal rendering, hit testing)
- manage-group: 17 hours (group management, color assignment)

**Week 3** (30 hours):
- sidebar-renderer: 32 hours (sidebar input client, modal rendering, fixture support)
- Integration: 10 hours (cross-package tests, end-to-end scenarios)

**Deliverables**:
- 800 lines of new tests
- 4 packages at 60%+ coverage
- Callback-based DI testing patterns
- Fixture files for visual regression

**Success criteria**:
- All tests pass in CI
- Coverage reports generated
- No performance regression
- Callback patterns documented

---

### Phase 3: Coordinator Refactoring (6+ weeks, 230 hours)

**Goal**: 65% → 70%+ overall coverage, comprehensive unit tests for coordinator

#### Phase 3a: Interface Extraction (1 week, 60 hours)

**Week 1** (60 hours):
- Extract TmuxRunner interface: 10 hours
- Extract FileWriter interface: 7 hours
- Extract StateStore interface: 3 hours
- Split coordinator.go into 4 modules: 40 hours

**Deliverables**:
- 3 interfaces (TmuxRunner, FileWriter, StateStore)
- 4 modules (windows, ai_detection, pet, config_mgmt)
- Real implementations (exec.Command, os.WriteFile, file-based state)
- Mock implementations (testify/mock)

**Success criteria**:
- All existing tests still pass
- No performance regression
- Module dependencies documented
- Interfaces reviewed and approved

---

#### Phase 3b: Module Unit Tests (4 weeks, 150 hours)

**Week 1** (40 hours):
- windows module: 40 hours (window inventory, layout, click handling, context menus)

**Week 2** (35 hours):
- ai_detection module: 35 hours (tool detection, state transitions, thought generation)

**Week 3** (45 hours):
- pet module: 45 hours (physics, state machine, animation, click handling)

**Week 4** (30 hours):
- config_mgmt module: 30 hours (config loading, state persistence, color utilities)

**Deliverables**:
- 1,500 lines of new tests
- 4 modules at 70%+ coverage
- Comprehensive unit test suite
- Performance benchmarks

**Success criteria**:
- All tests pass in CI
- Coverage reports generated
- No performance regression
- Code review feedback incorporated

---

#### Phase 3c: Integration and Cleanup (1 week, 20 hours)

**Week 1** (20 hours):
- Cross-module integration tests: 10 hours
- Performance validation: 5 hours
- Documentation and cleanup: 5 hours

**Deliverables**:
- Integration test suite
- Performance benchmarks
- Test documentation
- Cleanup and refactoring

**Success criteria**:
- All tests pass in CI
- Coverage reports generated
- No performance regression
- Documentation complete

---

### Phase 1–3 Summary

| Phase | Duration | Effort (hrs) | Coverage | Deliverables |
|-------|----------|--------------|----------|--------------|
| 1 | 2 weeks | 78 | 12% → 55% | 1,030 lines, 4 packages |
| 2 | 3 weeks | 117 | 55% → 65% | 800 lines, 4 packages |
| 3a | 1 week | 60 | — | 3 interfaces, 4 modules |
| 3b | 4 weeks | 150 | 65% → 70% | 1,500 lines, 4 modules |
| 3c | 1 week | 20 | — | Integration, cleanup |
| **TOTAL** | **11 weeks** | **425** | **12% → 70%** | **3,330 lines** |

---

## Effort Estimates

### Breakdown by Phase

| Phase | Task | Hours | Notes |
|-------|------|-------|-------|
| 1 | colors | 20 | Dimming, desaturation, theme resolution |
| 1 | paths | 15 | XDG expansion, home directory, creation |
| 1 | grouping | 18 | Color assignment, pattern matching, markers |
| 1 | config | 25 | LoadConfig, SaveConfig, validation, defaults |
| 1 | **Subtotal** | **78** | **2 weeks** |
| 2 | daemon/protocol | 12 | Message types, payloads, utilities |
| 2 | daemon/server | 30 | ClientInfo, Server, callbacks, sockets |
| 2 | pane-header | 28 | Input client, modal rendering, hit testing |
| 2 | manage-group | 15 | Group management, color assignment |
| 2 | sidebar-renderer | 32 | Sidebar input, modal rendering, fixtures |
| 2 | **Subtotal** | **117** | **3 weeks** |
| 3a | TmuxRunner interface | 10 | Extract, implement real, implement mock |
| 3a | FileWriter interface | 7 | Extract, implement real, implement mock |
| 3a | StateStore interface | 3 | Extract, implement real, implement mock |
| 3a | Module split | 40 | Refactor coordinator.go into 4 modules |
| 3a | **Subtotal** | **60** | **1 week** |
| 3b | windows module | 40 | Window inventory, layout, click handling |
| 3b | ai_detection module | 35 | Tool detection, state transitions, thoughts |
| 3b | pet module | 45 | Physics, state machine, animation |
| 3b | config_mgmt module | 30 | Config loading, state persistence, colors |
| 3b | **Subtotal** | **150** | **4 weeks** |
| 3c | Integration tests | 10 | Cross-module tests, end-to-end scenarios |
| 3c | Performance validation | 5 | Benchmarks, regression detection |
| 3c | Documentation | 5 | Test patterns, module docs, cleanup |
| 3c | **Subtotal** | **20** | **1 week** |
| — | **GRAND TOTAL** | **425** | **11 weeks** |

### Realistic Timeline

**Assumptions**:
- 40 hours/week development time
- 1 week per phase for code review, CI fixes, documentation
- Parallel work on Phase 2 packages (not strictly sequential)

**Realistic effort**: 240–280 hours (6–7 weeks at 40h/week)

**Optimistic timeline** (parallel Phase 2):
- Phase 1: 2 weeks (78 hours)
- Phase 2: 3 weeks (117 hours, parallel)
- Phase 3a: 1 week (60 hours)
- Phase 3b: 4 weeks (150 hours)
- Phase 3c: 1 week (20 hours)
- **Total: 11 weeks (425 hours)**

**Realistic timeline** (accounting for review, CI, documentation):
- Phase 1: 2 weeks + 1 week review = 3 weeks
- Phase 2: 3 weeks + 1 week review = 4 weeks
- Phase 3a: 1 week + 1 week review = 2 weeks
- Phase 3b: 4 weeks + 1 week review = 5 weeks
- Phase 3c: 1 week + 1 week review = 2 weeks
- **Total: 16 weeks (640 hours at 40h/week)**

**Recommended approach**:
- Start Phase 1 immediately (high ROI, no blockers)
- Start Phase 2 after Phase 1 review (parallel with Phase 1 cleanup)
- Start Phase 3a after Phase 2 review (prerequisite for Phase 3b)
- Start Phase 3b after Phase 3a review (depends on interfaces)
- Start Phase 3c after Phase 3b review (final integration)

---

## Risk Assessment

### Critical Risks

#### 1. Monolithic Coordinator.go (HIGH IMPACT, HIGH PROBABILITY)

**Risk**: Coordinator.go (11,321 lines) is too large to test effectively. Phase 3b unit tests will be difficult to write and maintain.

**Mitigation**:
- Phase 3a module split (40 hours) reduces size to 2,000–3,500 lines per module
- Each module has clear responsibility and dependencies
- Interfaces (TmuxRunner, FileWriter, StateStore) enable mocking
- Code review of module split before Phase 3b

**Acceptance**: Proceed with Phase 3a as prerequisite for Phase 3b

---

#### 2. Tmux Coupling (HIGH IMPACT, HIGH PROBABILITY)

**Risk**: 50+ direct exec.Command("tmux", ...) calls in windows.go and coordinator.go block mocking. Phase 2–3 tests will require real tmux session.

**Mitigation**:
- Phase 3a extracts TmuxRunner interface (10 hours)
- Mock implementation enables unit tests without tmux
- Real implementation uses exec.Command wrapper
- Phase 2 tests use real tmux (acceptable for daemon/server)
- Phase 3b tests use mock tmux (enables comprehensive coverage)

**Acceptance**: Proceed with interface extraction in Phase 3a

---

#### 3. Lock Discipline Complexity (MEDIUM IMPACT, MEDIUM PROBABILITY)

**Risk**: Coordinator uses RWMutex. Concurrent tests may deadlock or have race conditions.

**Mitigation**:
- Use go-deadlock tool in CI (detects deadlocks)
- Careful test design (avoid holding locks during I/O)
- Document lock discipline in code comments
- Phase 3b tests use mocks to avoid I/O under lock
- Code review of concurrent test scenarios

**Acceptance**: Proceed with careful test design and go-deadlock in CI

---

#### 4. Mutable Global State (MEDIUM IMPACT, MEDIUM PROBABILITY)

**Risk**: prevPaneBusy, hookPaneActive, collapsedGroups maps are mutable globals. Tests may interfere with each other.

**Mitigation**:
- ResetForTest() helper function (already in use)
- Call ResetForTest() at start of each test
- Document in test pattern documentation
- Phase 1 establishes pattern, Phase 2–3 follow

**Acceptance**: Proceed with ResetForTest() pattern

---

#### 5. Test Maintenance Burden (MEDIUM IMPACT, LOW PROBABILITY)

**Risk**: 3,330 lines of new tests require ongoing maintenance. Changes to coordinator.go may break many tests.

**Mitigation**:
- Table-driven tests are easier to maintain (already in use)
- Fixture files for visual regression (reduce brittle assertions)
- Comprehensive documentation of test patterns
- Code review process for test changes
- Automated test generation for simple cases (future)

**Acceptance**: Proceed with table-driven pattern and documentation

---

#### 6. Performance Regression (MEDIUM IMPACT, LOW PROBABILITY)

**Risk**: Mocking and interface abstraction may introduce performance overhead.

**Mitigation**:
- Benchmark tests for critical paths (Phase 3c)
- Real implementations (exec.Command, os.WriteFile) have no overhead
- Mock implementations used only in tests
- Performance validation in Phase 3c (5 hours)
- Code review of performance-critical code

**Acceptance**: Proceed with benchmarks in Phase 3c

---

### Moderate Risks

#### 7. Framework Churn (MEDIUM IMPACT, LOW PROBABILITY)

**Risk**: testify/mock may be replaced by newer framework in future.

**Mitigation**:
- testify/mock is stable and widely used
- Interface-based mocking is framework-agnostic
- Easy to swap implementations if needed
- Document mocking strategy in code comments

**Acceptance**: Proceed with testify/mock

---

#### 8. CI/CD Integration (MEDIUM IMPACT, MEDIUM PROBABILITY)

**Risk**: CI/CD pipeline may not support new test patterns (fixtures, benchmarks, go-deadlock).

**Mitigation**:
- Add fixture file support to CI (simple file comparison)
- Add benchmark support to CI (track performance over time)
- Add go-deadlock to CI (detect deadlocks)
- Phase 1 establishes CI patterns, Phase 2–3 follow

**Acceptance**: Proceed with CI/CD planning in Phase 1

---

### Low Risks

#### 9. Test Coverage Plateau (LOW IMPACT, MEDIUM PROBABILITY)

**Risk**: Coverage may plateau at 70% due to untestable code (e.g., error handling in rare cases).

**Mitigation**:
- 70% coverage is realistic and valuable
- Focus on high-impact code (windows, pet, config)
- Document untestable code with comments
- Revisit coverage targets after Phase 3b

**Acceptance**: Proceed with 70% target

---

### Risk Summary

| Risk | Impact | Probability | Mitigation | Status |
|------|--------|-------------|-----------|--------|
| Monolithic coordinator | HIGH | HIGH | Phase 3a module split | Accepted |
| Tmux coupling | HIGH | HIGH | Phase 3a interface extraction | Accepted |
| Lock discipline | MEDIUM | MEDIUM | go-deadlock, careful design | Accepted |
| Mutable global state | MEDIUM | MEDIUM | ResetForTest() pattern | Accepted |
| Test maintenance | MEDIUM | LOW | Table-driven, documentation | Accepted |
| Performance regression | MEDIUM | LOW | Benchmarks, real implementations | Accepted |
| Framework churn | MEDIUM | LOW | Interface-based mocking | Accepted |
| CI/CD integration | MEDIUM | MEDIUM | Phase 1 planning | Accepted |
| Coverage plateau | LOW | MEDIUM | 70% target, focus on impact | Accepted |

---

## Appendix: File Paths & Line Ranges

### Test Files (All 14 Cataloged)

| File | Lines | Current % | Phase |
|------|-------|-----------|-------|
| `/Users/b/git/tabby/pkg/colors/colors_test.go` | 245 | 40% | 1 |
| `/Users/b/git/tabby/pkg/paths/paths_test.go` | 107 | 35% | 1 |
| `/Users/b/git/tabby/pkg/grouping/grouper_test.go` | 170 | 30% | 1 |
| `/Users/b/git/tabby/cmd/manage-group/main_test.go` | 79 | 15% | 2 |
| `/Users/b/git/tabby/cmd/sidebar-renderer/main_test.go` | 152 | 15% | 2 |
| `/Users/b/git/tabby/cmd/pane-header/main_test.go` | 134 | 10% | 2 |
| `/Users/b/git/tabby/cmd/tabby-daemon/coordinator_test.go` | 45 | 2% | 3 |
| `/Users/b/git/tabby/cmd/tabby-daemon/coordinator_regions_test.go` | 67 | 2% | 3 |
| `/Users/b/git/tabby/cmd/tabby-daemon/coordinator_group_marker_test.go` | 52 | 2% | 3 |
| `/Users/b/git/tabby/cmd/tabby-daemon/coordinator_header_title_test.go` | 38 | 2% | 3 |
| `/Users/b/git/tabby/cmd/tabby-daemon/main_test.go` | 15 | 5% | 3 |
| `/Users/b/git/tabby/pkg/config/config_test.go` | 79 | 10% | 1 |

### Source Files (100% Analyzed)

| File | Lines | Phase | Notes |
|------|-------|-------|-------|
| `/Users/b/git/tabby/cmd/tabby-daemon/coordinator.go` | 11,321 | 3 | 100% analyzed, 4 modules to extract |
| `/Users/b/git/tabby/pkg/config/config.go` | 422 | 1 | LoadConfig, SaveConfig, 20+ types |
| `/Users/b/git/tabby/pkg/config/loader.go` | 400 | 1 | Config loading, validation, defaults |
| `/Users/b/git/tabby/pkg/daemon/protocol.go` | 207 | 2 | Message types, payloads, utilities |
| `/Users/b/git/tabby/pkg/daemon/server.go` | 607 | 2 | ClientInfo, Server, callbacks, sockets |
| `/Users/b/git/tabby/pkg/tmux/windows.go` | 698 | 3 | Window/pane inventory, 50+ tmux calls |
| `/Users/b/git/tabby/pkg/colors/colors.go` | 600 | 1 | Color utilities, theme detection |
| `/Users/b/git/tabby/pkg/grouping/grouper.go` | 350 | 1 | Grouping logic, color assignment |
| `/Users/b/git/tabby/pkg/paths/paths.go` | 350 | 1 | XDG path resolution, home expansion |

### Coordinator.go Module Split (Phase 3a)

#### Module 1: windows (2,000 lines)

**Source lines**:
- Lines 1–366: Struct definitions
- Lines 3990–4500: computeVisualPositions, renderWindowList
- Lines 8164–8437: handleWindowClick, handleWindowMenu
- Lines 10209–10373: Pane context menu

**Methods**: RefreshWindows, computeVisualPositions, renderWindowList, handleWindowClick, handleWindowMenu, showPaneContextMenu, showWindowContextMenu, selectContentPaneInActiveWindow, findWindowByTarget

---

#### Module 2: ai_detection (1,500 lines)

**Source lines**:
- Lines 1478–1821: detectAITools, updateAIDetectionState
- Lines 1821–2100: AI state machine transitions
- Lines 10878–10948: triggerActionFromThought
- Lines 10950–10979: Default pet thoughts map

**Methods**: detectAITools, updateAIDetectionState, triggerActionFromThought, getDefaultThoughts, parseThoughtKeywords

---

#### Module 3: pet (3,500 lines)

**Source lines**:
- Lines 366–1478: petState definition, pet mechanics
- Lines 3357–3990: renderPetWidget, buildPetSprite
- Lines 4500–8164: Pet animation, physics, collision detection
- Lines 9256–9336: Pet click handling

**Methods**: updatePetState, renderPetWidget, buildPetSprite, handlePetClick, updatePetPhysics, checkPetCollisions, updatePetAdventure

---

#### Module 4: config_mgmt (1,500 lines)

**Source lines**:
- Lines 2100–2500: Config loading and validation
- Lines 8667–9000: Theme detection and color resolution
- Lines 10981–11165: Color utility functions
- Lines 11271–11321: GetSidebarBg, applyBackgroundFill

**Methods**: LoadConfig, SaveConfig, GetSidebarBg, applyBackgroundFill, hexToRGB, dimColor, desaturateHex, GetHeaderColorsForPane, GetGitStateHash

---

### Interfaces to Extract (Phase 3a)

#### TmuxRunner Interface

**Location**: `/Users/b/git/tabby/pkg/tmux/runner.go` (new file)

**Methods**:
- Run(ctx context.Context, args ...string) error
- Output(ctx context.Context, args ...string) (string, error)
- OutputCtx(ctx context.Context, args ...string) (string, error)
- GetWindowList(ctx context.Context) ([]Window, error)
- GetPaneList(ctx context.Context, windowID string) ([]Pane, error)
- GetPaneCommand(ctx context.Context, paneID string) (string, error)
- GetPaneWorkingDir(ctx context.Context, paneID string) (string, error)

**Implementations**:
- Real: `/Users/b/git/tabby/pkg/tmux/runner_real.go`
- Mock: `/Users/b/git/tabby/pkg/tmux/runner_mock.go` (testify/mock)

---

#### FileWriter Interface

**Location**: `/Users/b/git/tabby/pkg/files/writer.go` (new file)

**Methods**:
- WriteFile(path string, data []byte, perm os.FileMode) error
- ReadFile(path string) ([]byte, error)
- MkdirAll(path string, perm os.FileMode) error
- Stat(path string) (os.FileInfo, error)
- Remove(path string) error

**Implementations**:
- Real: `/Users/b/git/tabby/pkg/files/writer_real.go`
- Mock: `/Users/b/git/tabby/pkg/files/writer_mock.go` (testify/mock)

---

#### StateStore Interface

**Location**: `/Users/b/git/tabby/pkg/state/store.go` (new file)

**Methods**:
- SavePetState(state *petState) error
- LoadPetState() (*petState, error)
- SaveCollapsedGroups(groups map[string]bool) error
- LoadCollapsedGroups() (map[string]bool, error)

**Implementations**:
- Real: `/Users/b/git/tabby/pkg/state/store_real.go` (file-based)
- Mock: `/Users/b/git/tabby/pkg/state/store_mock.go` (testify/mock)

---

## Conclusion

This Test Suite Overhaul Plan provides a **data-driven, phased approach** to improving Tabby's test coverage from **12% to 70%** over **11 weeks** (425 hours).

**Key recommendations**:
1. **Start Phase 1 immediately** (high ROI, no blockers, 2 weeks)
2. **Establish test patterns** (table-driven, helpers, fixtures)
3. **Introduce testify/mock in Phase 2** (callback-based DI testing)
4. **Execute Phase 3a interface extraction** (prerequisite for Phase 3b)
5. **Implement Phase 3b module unit tests** (comprehensive coverage)

**Success metrics**:
- Phase 1: 4 packages at 75%+ coverage
- Phase 2: 4 packages at 60%+ coverage
- Phase 3: 4 modules at 70%+ coverage, windows at 50%+ coverage
- Overall: 12% → 70% coverage

**Next steps**:
1. Review and approve this plan
2. Assign Phase 1 tasks to team members
3. Set up CI/CD for test coverage tracking
4. Begin Phase 1 implementation

