package daemon

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	return &Server{
		pidPath:          filepath.Join(dir, "test.pid"),
		socketPath:       filepath.Join(dir, "test.sock"),
		clients:          make(map[string]*ClientInfo),
		done:             make(chan struct{}),
		renderPending:    make(map[string]bool),
		renderBatchDelay: 5 * time.Millisecond, // short delay for tests
	}
}

func TestHashContent(t *testing.T) {
	t.Run("empty_string_returns_zero", func(t *testing.T) {
		assert.Equal(t, uint32(0), hashContent(""))
	})

	t.Run("deterministic_same_input", func(t *testing.T) {
		assert.Equal(t, hashContent("abc"), hashContent("abc"))
	})

	t.Run("different_inputs_differ", func(t *testing.T) {
		assert.NotEqual(t, hashContent("abc"), hashContent("abd"))
	})

	t.Run("stable_across_three_calls", func(t *testing.T) {
		h := hashContent("hello world")
		assert.Equal(t, h, hashContent("hello world"))
		assert.Equal(t, h, hashContent("hello world"))
	})
}

func TestNewServer(t *testing.T) {
	t.Setenv("TABBY_RUNTIME_PREFIX", "")

	t.Run("socket_path", func(t *testing.T) {
		s := NewServer("ses123")
		assert.Equal(t, "/tmp/tabby-daemon-ses123.sock", s.socketPath)
	})

	t.Run("pid_path", func(t *testing.T) {
		s := NewServer("ses123")
		assert.Equal(t, "/tmp/tabby-daemon-ses123.pid", s.pidPath)
	})

	t.Run("clients_map_initialized", func(t *testing.T) {
		s := NewServer("ses123")
		assert.NotNil(t, s.clients)
	})

	t.Run("done_channel_initialized", func(t *testing.T) {
		s := NewServer("ses123")
		assert.NotNil(t, s.done)
	})

	t.Run("empty_session_uses_default", func(t *testing.T) {
		s := NewServer("")
		assert.Contains(t, s.socketPath, "default")
		assert.Contains(t, s.pidPath, "default")
	})
}

func TestClientCount(t *testing.T) {
	t.Run("zero_clients", func(t *testing.T) {
		s := newTestServer(t)
		assert.Equal(t, 0, s.ClientCount())
	})

	t.Run("one_client", func(t *testing.T) {
		s := newTestServer(t)
		s.clients["c1"] = &ClientInfo{Width: 80, Height: 24}
		assert.Equal(t, 1, s.ClientCount())
	})

	t.Run("three_clients", func(t *testing.T) {
		s := newTestServer(t)
		s.clients["c1"] = &ClientInfo{}
		s.clients["c2"] = &ClientInfo{}
		s.clients["c3"] = &ClientInfo{}
		assert.Equal(t, 3, s.ClientCount())
	})
}

func TestGetClientInfo(t *testing.T) {
	t.Run("existing_client_returns_correct_fields", func(t *testing.T) {
		s := newTestServer(t)
		s.clients["c1"] = &ClientInfo{
			Width:          120,
			Height:         40,
			ViewportOffset: 5,
			ColorProfile:   "TrueColor",
		}
		info := s.GetClientInfo("c1")
		if !assert.NotNil(t, info) {
			return
		}
		assert.Equal(t, 120, info.Width)
		assert.Equal(t, 40, info.Height)
		assert.Equal(t, 5, info.ViewportOffset)
		assert.Equal(t, "TrueColor", info.ColorProfile)
	})

	t.Run("non_existent_client_returns_nil", func(t *testing.T) {
		s := newTestServer(t)
		assert.Nil(t, s.GetClientInfo("ghost"))
	})

	t.Run("returned_copy_is_independent", func(t *testing.T) {
		s := newTestServer(t)
		s.clients["c1"] = &ClientInfo{Width: 80, Height: 24}

		info := s.GetClientInfo("c1")
		if !assert.NotNil(t, info) {
			return
		}
		info.Width = 999

		assert.Equal(t, 80, s.clients["c1"].Width)
	})
}

func TestGetAllClientIDs(t *testing.T) {
	t.Run("empty_map_returns_empty_non_nil_slice", func(t *testing.T) {
		s := newTestServer(t)
		ids := s.GetAllClientIDs()
		assert.NotNil(t, ids)
		assert.Empty(t, ids)
	})

	t.Run("three_clients_returns_all_ids", func(t *testing.T) {
		s := newTestServer(t)
		s.clients["@1"] = &ClientInfo{}
		s.clients["@2"] = &ClientInfo{}
		s.clients["@3"] = &ClientInfo{}

		ids := s.GetAllClientIDs()
		sort.Strings(ids)
		assert.ElementsMatch(t, []string{"@1", "@2", "@3"}, ids)
	})
}

func TestUpdateClientSize(t *testing.T) {
	t.Run("updates_width_and_height", func(t *testing.T) {
		s := newTestServer(t)
		s.clients["c1"] = &ClientInfo{Width: 80, Height: 24}

		s.UpdateClientSize("c1", 120, 40)

		assert.Equal(t, 120, s.clients["c1"].Width)
		assert.Equal(t, 40, s.clients["c1"].Height)
	})

	t.Run("resets_last_content_hash", func(t *testing.T) {
		s := newTestServer(t)
		s.clients["c1"] = &ClientInfo{Width: 80, Height: 24, lastContentHash: 0xDEADBEEF}

		s.UpdateClientSize("c1", 100, 30)

		assert.Equal(t, uint32(0), s.clients["c1"].lastContentHash)
	})

	t.Run("non_existent_client_no_panic", func(t *testing.T) {
		s := newTestServer(t)
		assert.NotPanics(t, func() {
			s.UpdateClientSize("ghost", 100, 50)
		})
		assert.Empty(t, s.clients)
	})
}

func TestUpdateClientWidth(t *testing.T) {
	t.Run("updates_width_leaves_height_unchanged", func(t *testing.T) {
		s := newTestServer(t)
		s.clients["c1"] = &ClientInfo{Width: 80, Height: 24}

		s.UpdateClientWidth("c1", 120)

		assert.Equal(t, 120, s.clients["c1"].Width)
		assert.Equal(t, 24, s.clients["c1"].Height)
	})

	t.Run("resets_last_content_hash", func(t *testing.T) {
		s := newTestServer(t)
		s.clients["c1"] = &ClientInfo{Width: 80, lastContentHash: 0xCAFEBABE}

		s.UpdateClientWidth("c1", 100)

		assert.Equal(t, uint32(0), s.clients["c1"].lastContentHash)
	})

	t.Run("non_existent_client_no_panic", func(t *testing.T) {
		s := newTestServer(t)
		assert.NotPanics(t, func() {
			s.UpdateClientWidth("ghost", 80)
		})
	})
}

func TestGetMinColorProfile(t *testing.T) {
	t.Run("zero_clients_returns_ANSI256", func(t *testing.T) {
		s := newTestServer(t)
		assert.Equal(t, "ANSI256", s.GetMinColorProfile())
	})

	t.Run("single_truecolor_client", func(t *testing.T) {
		s := newTestServer(t)
		s.clients["c1"] = &ClientInfo{ColorProfile: "TrueColor"}
		assert.Equal(t, "TrueColor", s.GetMinColorProfile())
	})

	t.Run("truecolor_and_ansi256_returns_ansi256", func(t *testing.T) {
		s := newTestServer(t)
		s.clients["c1"] = &ClientInfo{ColorProfile: "TrueColor"}
		s.clients["c2"] = &ClientInfo{ColorProfile: "ANSI256"}
		assert.Equal(t, "ANSI256", s.GetMinColorProfile())
	})

	t.Run("truecolor_and_ansi_returns_ansi", func(t *testing.T) {
		s := newTestServer(t)
		s.clients["c1"] = &ClientInfo{ColorProfile: "TrueColor"}
		s.clients["c2"] = &ClientInfo{ColorProfile: "ANSI"}
		assert.Equal(t, "ANSI", s.GetMinColorProfile())
	})

	t.Run("truecolor_and_ascii_returns_ascii", func(t *testing.T) {
		s := newTestServer(t)
		s.clients["c1"] = &ClientInfo{ColorProfile: "TrueColor"}
		s.clients["c2"] = &ClientInfo{ColorProfile: "Ascii"}
		assert.Equal(t, "Ascii", s.GetMinColorProfile())
	})

	t.Run("empty_profile_normalized_to_ansi256", func(t *testing.T) {
		s := newTestServer(t)
		s.clients["c1"] = &ClientInfo{ColorProfile: "TrueColor"}
		s.clients["c2"] = &ClientInfo{ColorProfile: ""}
		assert.Equal(t, "ANSI256", s.GetMinColorProfile())
	})

	t.Run("unknown_profile_lowers_minimum_below_truecolor", func(t *testing.T) {
		s := newTestServer(t)
		s.clients["c1"] = &ClientInfo{ColorProfile: "TrueColor"}
		s.clients["c2"] = &ClientInfo{ColorProfile: "Unknown"}
		assert.NotEqual(t, "TrueColor", s.GetMinColorProfile())
	})
}

func TestColorProfileOrder(t *testing.T) {
	assert.Less(t, colorProfileOrder["Ascii"], colorProfileOrder["ANSI"])
	assert.Less(t, colorProfileOrder["ANSI"], colorProfileOrder["ANSI256"])
	assert.Less(t, colorProfileOrder["ANSI256"], colorProfileOrder["TrueColor"])
}

func TestCheckAndClaimPid(t *testing.T) {
	t.Run("no_pidfile_succeeds_and_writes_current_pid", func(t *testing.T) {
		s := newTestServer(t)

		err := s.checkAndClaimPid()
		if !assert.NoError(t, err) {
			return
		}

		data, readErr := os.ReadFile(s.pidPath)
		if !assert.NoError(t, readErr) {
			return
		}
		writtenPID, parseErr := strconv.Atoi(string(data))
		if !assert.NoError(t, parseErr) {
			return
		}
		assert.Equal(t, os.Getpid(), writtenPID)
	})

	t.Run("stale_pidfile_is_reclaimed", func(t *testing.T) {
		s := newTestServer(t)

		writeErr := os.WriteFile(s.pidPath, []byte(fmt.Sprintf("%d", 99999999)), 0644)
		if !assert.NoError(t, writeErr) {
			return
		}

		err := s.checkAndClaimPid()
		if !assert.NoError(t, err) {
			return
		}

		data, readErr := os.ReadFile(s.pidPath)
		if !assert.NoError(t, readErr) {
			return
		}
		writtenPID, parseErr := strconv.Atoi(string(data))
		if !assert.NoError(t, parseErr) {
			return
		}
		assert.Equal(t, os.Getpid(), writtenPID)
	})

	t.Run("active_pid_returns_error", func(t *testing.T) {
		s := newTestServer(t)

		writeErr := os.WriteFile(s.pidPath, []byte(fmt.Sprintf("%d", os.Getpid())), 0644)
		if !assert.NoError(t, writeErr) {
			return
		}

		err := s.checkAndClaimPid()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "daemon already running")
	})

	t.Run("unwritable_dir_returns_error", func(t *testing.T) {
		roDir := t.TempDir()
		if err := os.Chmod(roDir, 0555); err != nil {
			t.Skipf("cannot chmod dir: %v", err)
		}
		t.Cleanup(func() { os.Chmod(roDir, 0755) })

		s := newTestServer(t)
		s.pidPath = filepath.Join(roDir, "test.pid")

		err := s.checkAndClaimPid()
		assert.Error(t, err, "writing to read-only dir should fail")
		assert.Contains(t, err.Error(), "failed to write pidfile")
	})
}

func TestGetSocketPath(t *testing.T) {
	t.Setenv("TABBY_RUNTIME_PREFIX", "")
	s := NewServer("abc")
	assert.Equal(t, "/tmp/tabby-daemon-abc.sock", s.GetSocketPath())
}

func TestStop(t *testing.T) {
	t.Run("empty_server_no_pidfile", func(t *testing.T) {
		s := newTestServer(t)
		assert.NotPanics(t, func() { s.Stop() })
	})

	t.Run("empty_server_with_own_pidfile_removes_files", func(t *testing.T) {
		s := newTestServer(t)
		err := os.WriteFile(s.pidPath, []byte(fmt.Sprintf("%d", os.Getpid())), 0644)
		if !assert.NoError(t, err) {
			return
		}

		s.Stop()

		_, statErr := os.Stat(s.pidPath)
		assert.True(t, os.IsNotExist(statErr), "pidfile should be removed after Stop")
	})

	t.Run("empty_server_with_foreign_pidfile_leaves_files", func(t *testing.T) {
		s := newTestServer(t)
		err := os.WriteFile(s.pidPath, []byte("99999"), 0644)
		if !assert.NoError(t, err) {
			return
		}

		s.Stop()

		_, statErr := os.Stat(s.pidPath)
		assert.NoError(t, statErr, "foreign pidfile should NOT be removed")
	})

	t.Run("clients_closed_and_removed", func(t *testing.T) {
		s := newTestServer(t)
		err := os.WriteFile(s.pidPath, []byte(fmt.Sprintf("%d", os.Getpid())), 0644)
		if !assert.NoError(t, err) {
			return
		}

		serverConn, clientConn := net.Pipe()
		defer clientConn.Close()
		s.clients["c1"] = &ClientInfo{Conn: serverConn, Width: 80, Height: 24}

		s.Stop()

		assert.Equal(t, 0, len(s.clients))
		buf := make([]byte, 1)
		_, readErr := clientConn.Read(buf)
		assert.Error(t, readErr, "clientConn should be closed after Stop")
	})
}

func TestSendMenuToClient(t *testing.T) {
	t.Run("non_existent_client_no_panic", func(t *testing.T) {
		s := newTestServer(t)
		assert.NotPanics(t, func() {
			s.SendMenuToClient("ghost", &MenuPayload{Title: "Test"})
		})
	})

	t.Run("existing_client_sends_message", func(t *testing.T) {
		s := newTestServer(t)
		serverConn, clientConn := net.Pipe()
		defer serverConn.Close()
		defer clientConn.Close()

		s.clients["c1"] = &ClientInfo{Conn: serverConn}

		received := make(chan string, 1)
		go func() {
			buf := make([]byte, 4096)
			n, _ := clientConn.Read(buf)
			received <- string(buf[:n])
		}()

		s.SendMenuToClient("c1", &MenuPayload{Title: "TestMenu", X: 10, Y: 20})

		data := <-received
		assert.Contains(t, data, "TestMenu")
	})
}

func TestSendMarkerPickerToClient(t *testing.T) {
	t.Run("non_existent_client_no_panic", func(t *testing.T) {
		s := newTestServer(t)
		assert.NotPanics(t, func() {
			s.SendMarkerPickerToClient("ghost", &MarkerPickerPayload{Title: "Test"})
		})
	})

	t.Run("existing_client_sends_message", func(t *testing.T) {
		s := newTestServer(t)
		serverConn, clientConn := net.Pipe()
		defer serverConn.Close()
		defer clientConn.Close()

		s.clients["c1"] = &ClientInfo{Conn: serverConn}

		received := make(chan string, 1)
		go func() {
			buf := make([]byte, 4096)
			n, _ := clientConn.Read(buf)
			received <- string(buf[:n])
		}()

		s.SendMarkerPickerToClient("c1", &MarkerPickerPayload{Title: "PickerTitle"})

		data := <-received
		assert.Contains(t, data, "PickerTitle")
	})
}

func TestSendColorPickerToClient(t *testing.T) {
	t.Run("non_existent_client_no_panic", func(t *testing.T) {
		s := newTestServer(t)
		assert.NotPanics(t, func() {
			s.SendColorPickerToClient("ghost", &ColorPickerPayload{Title: "Test"})
		})
	})

	t.Run("existing_client_sends_message", func(t *testing.T) {
		s := newTestServer(t)
		serverConn, clientConn := net.Pipe()
		defer serverConn.Close()
		defer clientConn.Close()

		s.clients["c1"] = &ClientInfo{Conn: serverConn}

		received := make(chan string, 1)
		go func() {
			buf := make([]byte, 4096)
			n, _ := clientConn.Read(buf)
			received <- string(buf[:n])
		}()

		s.SendColorPickerToClient("c1", &ColorPickerPayload{Title: "ColorPicker", Scope: "window"})

		data := <-received
		assert.Contains(t, data, "ColorPicker")
	})
}

func TestRenderActiveWindowOnly(t *testing.T) {
	t.Run("no_matching_client_no_panic", func(t *testing.T) {
		s := newTestServer(t)
		s.clients["@2"] = &ClientInfo{Width: 80, Height: 24}
		assert.NotPanics(t, func() {
			s.RenderActiveWindowOnly("@1")
		})
	})

	t.Run("matching_client_nil_callback_no_panic", func(t *testing.T) {
		s := newTestServer(t)
		s.clients["@1"] = &ClientInfo{Width: 80, Height: 24}
		assert.NotPanics(t, func() {
			s.RenderActiveWindowOnly("@1")
		})
	})

	t.Run("empty_clients_no_panic", func(t *testing.T) {
		s := newTestServer(t)
		assert.NotPanics(t, func() {
			s.RenderActiveWindowOnly("@1")
		})
	})
}
