package daemon

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestMessageTypeConstants verifies all MessageType constants exist and are unique
func TestMessageTypeConstants(t *testing.T) {
	t.Run("all_constants_exist", func(t *testing.T) {
		expected := map[MessageType]bool{
			MsgSubscribe:      true,
			MsgUnsubscribe:    true,
			MsgRender:         true,
			MsgInput:          true,
			MsgResize:         true,
			MsgViewportUpdate: true,
			MsgMenu:           true,
			MsgMenuSelect:     true,
			MsgMarkerPicker:   true,
			MsgColorPicker:    true,
			MsgPing:           true,
			MsgPong:           true,
		}
		assert.Equal(t, 12, len(expected), "should have exactly 12 message types")
	})

	t.Run("constants_have_correct_string_values", func(t *testing.T) {
		tests := []struct {
			constant MessageType
			expected string
		}{
			{MsgSubscribe, "subscribe"},
			{MsgUnsubscribe, "unsubscribe"},
			{MsgRender, "render"},
			{MsgInput, "input"},
			{MsgResize, "resize"},
			{MsgViewportUpdate, "viewport_update"},
			{MsgMenu, "menu"},
			{MsgMenuSelect, "menu_select"},
			{MsgMarkerPicker, "marker_picker"},
			{MsgColorPicker, "color_picker"},
			{MsgPing, "ping"},
			{MsgPong, "pong"},
		}

		for _, tt := range tests {
			t.Run(string(tt.constant), func(t *testing.T) {
				assert.Equal(t, tt.expected, string(tt.constant))
			})
		}
	})

	t.Run("all_constants_are_unique", func(t *testing.T) {
		constants := []MessageType{
			MsgSubscribe, MsgUnsubscribe, MsgRender, MsgInput, MsgResize,
			MsgViewportUpdate, MsgMenu, MsgMenuSelect, MsgMarkerPicker,
			MsgColorPicker, MsgPing, MsgPong,
		}

		seen := make(map[MessageType]bool)
		for _, c := range constants {
			assert.False(t, seen[c], "MessageType %s appears more than once", c)
			seen[c] = true
		}
		assert.Equal(t, 12, len(seen), "should have exactly 12 unique constants")
	})
}

func TestMessageTypeJSONFieldName(t *testing.T) {
	msg := Message{Type: MsgRender, ClientID: "c1"}
	data, err := json.Marshal(msg)
	assert.NoError(t, err)
	assert.Contains(t, string(data), `"type":"render"`)
	assert.Contains(t, string(data), `"client_id":"c1"`)

	msg2 := Message{Type: MsgSubscribe}
	data2, err := json.Marshal(msg2)
	assert.NoError(t, err)
	assert.Contains(t, string(data2), `"type":"subscribe"`)
}

// TestJSONRoundTrip verifies all payload types can be marshaled and unmarshaled
func TestJSONRoundTrip(t *testing.T) {
	t.Run("RenderPayload", func(t *testing.T) {
		original := RenderPayload{
			SequenceNum:    42,
			Content:        "hello",
			PinnedContent:  "pinned",
			Width:          80,
			Height:         24,
			TotalLines:     100,
			PinnedHeight:   3,
			ViewportOffset: 5,
			SidebarBg:      "#1a1a2e",
			TerminalBg:     "#0f0f1e",
			Regions: []ClickableRegion{
				{StartLine: 0, EndLine: 5, StartCol: 0, EndCol: 0, Action: "select_window", Target: "1"},
				{StartLine: 6, EndLine: 10, StartCol: 0, EndCol: 0, Action: "select_pane", Target: "%1"},
			},
			PinnedRegions: []ClickableRegion{
				{StartLine: 0, EndLine: 2, StartCol: 0, EndCol: 0, Action: "toggle_group", Target: "dev"},
			},
		}

		data, err := json.Marshal(original)
		if !assert.NoError(t, err) {
			return
		}

		var restored RenderPayload
		err = json.Unmarshal(data, &restored)
		if !assert.NoError(t, err) {
			return
		}

		assert.Equal(t, original, restored)
	})

	t.Run("InputPayload_mouse", func(t *testing.T) {
		original := InputPayload{
			SequenceNum:           100,
			Type:                  "mouse",
			MouseX:                10,
			MouseY:                5,
			Button:                "left",
			Action:                "press",
			ClickedArea:           "scrollable",
			ResolvedAction:        "select_window",
			ResolvedTarget:        "1",
			IsSimulatedRightClick: false,
		}

		data, err := json.Marshal(original)
		if !assert.NoError(t, err) {
			return
		}

		var restored InputPayload
		err = json.Unmarshal(data, &restored)
		if !assert.NoError(t, err) {
			return
		}

		assert.Equal(t, original, restored)
	})

	t.Run("InputPayload_keyboard", func(t *testing.T) {
		original := InputPayload{
			Type: "key",
			Key:  "Enter",
		}

		data, err := json.Marshal(original)
		if !assert.NoError(t, err) {
			return
		}

		var restored InputPayload
		err = json.Unmarshal(data, &restored)
		if !assert.NoError(t, err) {
			return
		}

		assert.Equal(t, original, restored)
	})

	t.Run("InputPayload_picker", func(t *testing.T) {
		original := InputPayload{
			Type:         "action",
			PickerAction: "apply",
			PickerScope:  "window",
			PickerTarget: "%1",
			PickerValue:  "🚀",
			PickerQuery:  "rocket",
		}

		data, err := json.Marshal(original)
		if !assert.NoError(t, err) {
			return
		}

		var restored InputPayload
		err = json.Unmarshal(data, &restored)
		if !assert.NoError(t, err) {
			return
		}

		assert.Equal(t, original, restored)
	})

	t.Run("ResizePayload", func(t *testing.T) {
		original := ResizePayload{
			Width:        120,
			Height:       40,
			ColorProfile: "TrueColor",
			PaneID:       "%5",
		}

		data, err := json.Marshal(original)
		if !assert.NoError(t, err) {
			return
		}

		var restored ResizePayload
		err = json.Unmarshal(data, &restored)
		if !assert.NoError(t, err) {
			return
		}

		assert.Equal(t, original, restored)
	})

	t.Run("ViewportUpdatePayload", func(t *testing.T) {
		original := ViewportUpdatePayload{
			ViewportOffset: 15,
		}

		data, err := json.Marshal(original)
		if !assert.NoError(t, err) {
			return
		}

		var restored ViewportUpdatePayload
		err = json.Unmarshal(data, &restored)
		if !assert.NoError(t, err) {
			return
		}

		assert.Equal(t, original, restored)
	})

	t.Run("MenuPayload", func(t *testing.T) {
		original := MenuPayload{
			Title: "Window Options",
			Items: []MenuItemPayload{
				{Label: "Rename", Key: "r"},
				{Label: "---", Separator: true},
				{Label: "Header", Header: true},
				{Label: "Kill", Key: "k"},
			},
			X: 10,
			Y: 5,
		}

		data, err := json.Marshal(original)
		if !assert.NoError(t, err) {
			return
		}

		var restored MenuPayload
		err = json.Unmarshal(data, &restored)
		if !assert.NoError(t, err) {
			return
		}

		assert.Equal(t, original, restored)
	})

	t.Run("MenuSelectPayload_selected", func(t *testing.T) {
		original := MenuSelectPayload{
			Index: 2,
		}

		data, err := json.Marshal(original)
		if !assert.NoError(t, err) {
			return
		}

		var restored MenuSelectPayload
		err = json.Unmarshal(data, &restored)
		if !assert.NoError(t, err) {
			return
		}

		assert.Equal(t, original, restored)
	})

	t.Run("MenuSelectPayload_cancelled", func(t *testing.T) {
		original := MenuSelectPayload{
			Index: -1,
		}

		data, err := json.Marshal(original)
		if !assert.NoError(t, err) {
			return
		}

		var restored MenuSelectPayload
		err = json.Unmarshal(data, &restored)
		if !assert.NoError(t, err) {
			return
		}

		assert.Equal(t, original, restored)
	})

	t.Run("MarkerPickerPayload", func(t *testing.T) {
		original := MarkerPickerPayload{
			Title:  "Set Icon",
			Scope:  "window",
			Target: "1",
			Options: []MarkerOptionPayload{
				{Symbol: "🚀", Name: "Rocket", Keywords: "space"},
				{Symbol: "🎯", Name: "Target", Keywords: "goal"},
			},
		}

		data, err := json.Marshal(original)
		if !assert.NoError(t, err) {
			return
		}

		var restored MarkerPickerPayload
		err = json.Unmarshal(data, &restored)
		if !assert.NoError(t, err) {
			return
		}

		assert.Equal(t, original, restored)
	})

	t.Run("ColorPickerPayload", func(t *testing.T) {
		original := ColorPickerPayload{
			Title:        "Pick Color",
			Scope:        "group",
			Target:       "Dev",
			CurrentColor: "#3498db",
		}

		data, err := json.Marshal(original)
		if !assert.NoError(t, err) {
			return
		}

		var restored ColorPickerPayload
		err = json.Unmarshal(data, &restored)
		if !assert.NoError(t, err) {
			return
		}

		assert.Equal(t, original, restored)
	})

	t.Run("ClickableRegion", func(t *testing.T) {
		original := ClickableRegion{
			StartLine: 5,
			EndLine:   8,
			StartCol:  0,
			EndCol:    0,
			Action:    "select_window",
			Target:    "1",
		}

		data, err := json.Marshal(original)
		if !assert.NoError(t, err) {
			return
		}

		var restored ClickableRegion
		err = json.Unmarshal(data, &restored)
		if !assert.NoError(t, err) {
			return
		}

		assert.Equal(t, original, restored)
	})

	t.Run("Message_with_RenderPayload", func(t *testing.T) {
		payload := RenderPayload{
			SequenceNum: 42,
			Content:     "test",
			Width:       80,
			Height:      24,
		}

		original := Message{
			Type:     MsgRender,
			ClientID: "client-1",
			Payload:  payload,
		}

		data, err := json.Marshal(original)
		if !assert.NoError(t, err) {
			return
		}

		var restored Message
		err = json.Unmarshal(data, &restored)
		if !assert.NoError(t, err) {
			return
		}

		assert.Equal(t, original.Type, restored.Type)
		assert.Equal(t, original.ClientID, restored.ClientID)
		assert.NotNil(t, restored.Payload)
	})
}

// TestWidgetStateJSON verifies widget state types can be marshaled and unmarshaled
func TestWidgetStateJSON(t *testing.T) {
	t.Run("GitState", func(t *testing.T) {
		now := time.Now()
		original := GitState{
			Branch:     "main",
			IsRepo:     true,
			IsDirty:    true,
			Ahead:      2,
			Behind:     1,
			Staged:     3,
			Unstaged:   1,
			Untracked:  2,
			Stashes:    0,
			LastUpdate: now,
		}

		data, err := json.Marshal(original)
		if !assert.NoError(t, err) {
			return
		}

		var restored GitState
		err = json.Unmarshal(data, &restored)
		if !assert.NoError(t, err) {
			return
		}

		assert.Equal(t, original.Branch, restored.Branch)
		assert.Equal(t, original.IsRepo, restored.IsRepo)
		assert.Equal(t, original.IsDirty, restored.IsDirty)
		assert.Equal(t, original.Ahead, restored.Ahead)
		assert.Equal(t, original.Behind, restored.Behind)
		assert.Equal(t, original.Staged, restored.Staged)
		assert.Equal(t, original.Unstaged, restored.Unstaged)
		assert.Equal(t, original.Untracked, restored.Untracked)
		assert.Equal(t, original.Stashes, restored.Stashes)
	})

	t.Run("StatsState", func(t *testing.T) {
		now := time.Now()
		original := StatsState{
			CPUPercent:     45.2,
			MemoryUsed:     8.5,
			MemoryTotal:    16.0,
			MemoryPercent:  53.125,
			BatteryPercent: 80,
			BatteryStatus:  "Charging",
			LastUpdate:     now,
		}

		data, err := json.Marshal(original)
		if !assert.NoError(t, err) {
			return
		}

		var restored StatsState
		err = json.Unmarshal(data, &restored)
		if !assert.NoError(t, err) {
			return
		}

		assert.Equal(t, original.CPUPercent, restored.CPUPercent)
		assert.Equal(t, original.MemoryUsed, restored.MemoryUsed)
		assert.Equal(t, original.MemoryTotal, restored.MemoryTotal)
		assert.Equal(t, original.MemoryPercent, restored.MemoryPercent)
		assert.Equal(t, original.BatteryPercent, restored.BatteryPercent)
		assert.Equal(t, original.BatteryStatus, restored.BatteryStatus)
	})

	t.Run("PetState", func(t *testing.T) {
		now := time.Now()
		original := PetState{
			Hunger:            50,
			Happiness:         75,
			LastFed:           now,
			LastPet:           now,
			TotalPets:         10,
			TotalFeedings:     5,
			TotalPoopsCleaned: 3,
			TotalYarnPlays:    2,
			State:             "idle",
			LastThought:       "I wonder...",
			PosX:              20,
			YarnPosX:          30,
			PoopPositions:     []int{3, 7},
		}

		data, err := json.Marshal(original)
		if !assert.NoError(t, err) {
			return
		}

		var restored PetState
		err = json.Unmarshal(data, &restored)
		if !assert.NoError(t, err) {
			return
		}

		assert.Equal(t, original.Hunger, restored.Hunger)
		assert.Equal(t, original.Happiness, restored.Happiness)
		assert.Equal(t, original.TotalPets, restored.TotalPets)
		assert.Equal(t, original.TotalFeedings, restored.TotalFeedings)
		assert.Equal(t, original.TotalPoopsCleaned, restored.TotalPoopsCleaned)
		assert.Equal(t, original.TotalYarnPlays, restored.TotalYarnPlays)
		assert.Equal(t, original.State, restored.State)
		assert.Equal(t, original.LastThought, restored.LastThought)
		assert.Equal(t, original.PosX, restored.PosX)
		assert.Equal(t, original.YarnPosX, restored.YarnPosX)
		assert.Equal(t, original.PoopPositions, restored.PoopPositions)
	})
}

// TestRuntimePaths verifies path helper functions
func TestRuntimePaths(t *testing.T) {
	tests := []struct {
		name      string
		sessionID string
		suffix    string
		expected  string
		envPrefix string
	}{
		{
			name:      "SocketPath_with_session",
			sessionID: "ses123",
			suffix:    ".sock",
			expected:  "/tmp/tabby-daemon-ses123.sock",
			envPrefix: "",
		},
		{
			name:      "PidPath_with_session",
			sessionID: "ses123",
			suffix:    ".pid",
			expected:  "/tmp/tabby-daemon-ses123.pid",
			envPrefix: "",
		},
		{
			name:      "RuntimePath_empty_session",
			sessionID: "",
			suffix:    ".sock",
			expected:  "/tmp/tabby-daemon-default.sock",
			envPrefix: "",
		},
		{
			name:      "RuntimePath_with_prefix",
			sessionID: "s1",
			suffix:    ".sock",
			expected:  "/tmp/demo-tabby-daemon-s1.sock",
			envPrefix: "demo-",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envPrefix != "" {
				t.Setenv("TABBY_RUNTIME_PREFIX", tt.envPrefix)
			}

			result := RuntimePath(tt.sessionID, tt.suffix)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestSocketPath verifies SocketPath helper
func TestSocketPath(t *testing.T) {
	t.Run("SocketPath_basic", func(t *testing.T) {
		result := SocketPath("ses123")
		assert.Equal(t, "/tmp/tabby-daemon-ses123.sock", result)
	})

	t.Run("SocketPath_with_env_prefix", func(t *testing.T) {
		t.Setenv("TABBY_RUNTIME_PREFIX", "test-")
		result := SocketPath("ses123")
		assert.Equal(t, "/tmp/test-tabby-daemon-ses123.sock", result)
	})
}

// TestPidPath verifies PidPath helper
func TestPidPath(t *testing.T) {
	t.Run("PidPath_basic", func(t *testing.T) {
		result := PidPath("ses123")
		assert.Equal(t, "/tmp/tabby-daemon-ses123.pid", result)
	})

	t.Run("PidPath_with_env_prefix", func(t *testing.T) {
		t.Setenv("TABBY_RUNTIME_PREFIX", "test-")
		result := PidPath("ses123")
		assert.Equal(t, "/tmp/test-tabby-daemon-ses123.pid", result)
	})
}

// TestRuntimePrefix verifies environment variable handling
func TestRuntimePrefix(t *testing.T) {
	t.Run("without_env_var", func(t *testing.T) {
		t.Setenv("TABBY_RUNTIME_PREFIX", "")
		result := RuntimePath("s1", ".sock")
		assert.Equal(t, "/tmp/tabby-daemon-s1.sock", result)
	})

	t.Run("with_env_var", func(t *testing.T) {
		t.Setenv("TABBY_RUNTIME_PREFIX", "demo-")
		result := RuntimePath("s1", ".sock")
		assert.Equal(t, "/tmp/demo-tabby-daemon-s1.sock", result)
	})

	t.Run("with_custom_prefix", func(t *testing.T) {
		t.Setenv("TABBY_RUNTIME_PREFIX", "custom_")
		result := RuntimePath("session", ".pid")
		assert.Equal(t, "/tmp/custom_tabby-daemon-session.pid", result)
	})
}

// TestClickableRegionJSON verifies ClickableRegion JSON tags
func TestClickableRegionJSON(t *testing.T) {
	t.Run("json_tags_correct", func(t *testing.T) {
		original := ClickableRegion{
			StartLine: 5,
			EndLine:   8,
			StartCol:  10,
			EndCol:    20,
			Action:    "select_window",
			Target:    "1",
		}

		data, err := json.Marshal(original)
		if !assert.NoError(t, err) {
			return
		}

		var m map[string]interface{}
		err = json.Unmarshal(data, &m)
		if !assert.NoError(t, err) {
			return
		}

		assert.Contains(t, m, "start")
		assert.Contains(t, m, "end")
		assert.Contains(t, m, "start_col")
		assert.Contains(t, m, "end_col")
		assert.Contains(t, m, "action")
		assert.Contains(t, m, "target")
	})
}

// TestMenuItemPayloadJSON verifies MenuItemPayload JSON tags
func TestMenuItemPayloadJSON(t *testing.T) {
	t.Run("json_tags_correct", func(t *testing.T) {
		original := MenuItemPayload{
			Label:     "Test",
			Key:       "t",
			Separator: true,
			Header:    true,
		}

		data, err := json.Marshal(original)
		if !assert.NoError(t, err) {
			return
		}

		var m map[string]interface{}
		err = json.Unmarshal(data, &m)
		if !assert.NoError(t, err) {
			return
		}

		assert.Contains(t, m, "label")
		assert.Contains(t, m, "key")
		assert.Contains(t, m, "separator")
		assert.Contains(t, m, "header")
	})
}

// TestMarkerOptionPayloadJSON verifies MarkerOptionPayload JSON tags
func TestMarkerOptionPayloadJSON(t *testing.T) {
	t.Run("json_tags_correct", func(t *testing.T) {
		original := MarkerOptionPayload{
			Symbol:   "🚀",
			Name:     "Rocket",
			Keywords: "space,launch",
		}

		data, err := json.Marshal(original)
		if !assert.NoError(t, err) {
			return
		}

		var m map[string]interface{}
		err = json.Unmarshal(data, &m)
		if !assert.NoError(t, err) {
			return
		}

		assert.Contains(t, m, "symbol")
		assert.Contains(t, m, "name")
		assert.Contains(t, m, "keywords")
	})
}

// TestEmptyPayloads verifies empty/zero-value payloads marshal correctly
func TestEmptyPayloads(t *testing.T) {
	t.Run("empty_RenderPayload", func(t *testing.T) {
		original := RenderPayload{}
		data, err := json.Marshal(original)
		if !assert.NoError(t, err) {
			return
		}

		var restored RenderPayload
		err = json.Unmarshal(data, &restored)
		if !assert.NoError(t, err) {
			return
		}

		assert.Equal(t, original, restored)
	})

	t.Run("empty_InputPayload", func(t *testing.T) {
		original := InputPayload{}
		data, err := json.Marshal(original)
		if !assert.NoError(t, err) {
			return
		}

		var restored InputPayload
		err = json.Unmarshal(data, &restored)
		if !assert.NoError(t, err) {
			return
		}

		assert.Equal(t, original, restored)
	})

	t.Run("empty_MenuPayload", func(t *testing.T) {
		original := MenuPayload{}
		data, err := json.Marshal(original)
		if !assert.NoError(t, err) {
			return
		}

		var restored MenuPayload
		err = json.Unmarshal(data, &restored)
		if !assert.NoError(t, err) {
			return
		}

		assert.Equal(t, original, restored)
	})
}
