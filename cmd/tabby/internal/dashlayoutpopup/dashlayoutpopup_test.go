package dashlayoutpopup

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mattn/go-runewidth"

	"github.com/brendandebeasi/tabby/pkg/daemon"
)

// The picker must offer exactly the 5 native tmux arrangements, in this order,
// with "tiled" first (the default). The daemon validates against the same set.
func TestChoicesMatchExpectedLayouts(t *testing.T) {
	want := []string{"tiled", "even-horizontal", "even-vertical", "main-vertical", "main-horizontal"}
	if len(choices) != len(want) {
		t.Fatalf("got %d choices, want %d", len(choices), len(want))
	}
	for i, w := range want {
		if choices[i].name != w {
			t.Errorf("choices[%d].name = %q, want %q", i, choices[i].name, w)
		}
		if strings.TrimSpace(choices[i].label) == "" {
			t.Errorf("choices[%d] (%s) has empty label", i, w)
		}
	}
}

// Every preview line must be width-stable: all lines within a preview share one
// display width, and that width fits inside the popup's inner content area.
func TestPreviewWidthStable(t *testing.T) {
	const popupInnerWidth = 48 - 2*2 // popup -w 48 minus cardStyle Padding(1,2)
	for _, c := range choices {
		w := -1
		for j, line := range c.preview {
			lw := runewidth.StringWidth(line)
			if w == -1 {
				w = lw
			} else if lw != w {
				t.Errorf("%s preview line %d width %d != %d (not rectangular)", c.name, j, lw, w)
			}
			if lw > popupInnerWidth {
				t.Errorf("%s preview line %d width %d exceeds popup inner width %d", c.name, j, lw, popupInnerWidth)
			}
		}
	}
}

func TestPadPreviewAlwaysFixedHeight(t *testing.T) {
	for _, c := range choices {
		if got := len(padPreview(c.preview)); got != previewRows {
			t.Errorf("%s padded to %d rows, want %d", c.name, got, previewRows)
		}
	}
	// Short input is padded up.
	if got := len(padPreview([]string{"x"})); got != previewRows {
		t.Errorf("short preview padded to %d, want %d", got, previewRows)
	}
	// Over-long input is trimmed down.
	long := make([]string, previewRows+3)
	if got := len(padPreview(long)); got != previewRows {
		t.Errorf("long preview trimmed to %d, want %d", got, previewRows)
	}
}

func TestCurrentIndex(t *testing.T) {
	cases := map[string]int{
		"":                0, // unset -> default (tiled)
		"tiled":           0,
		"even-horizontal": 1,
		"main-horizontal": 4,
		"bogus":           0, // unknown -> default
	}
	for in, want := range cases {
		if got := currentIndex(in); got != want {
			t.Errorf("currentIndex(%q) = %d, want %d", in, got, want)
		}
	}
}

// View must render every label and not panic, with the cursor's selection
// marker present.
func TestViewRendersLabels(t *testing.T) {
	m := model{cursor: 3, current: 0}
	out := m.View()
	for _, c := range choices {
		if !strings.Contains(out, c.label) {
			t.Errorf("View() missing label %q", c.label)
		}
	}
	if !strings.Contains(out, "▸") {
		t.Errorf("View() missing selection marker")
	}
}

// sendSetLayout must post a well-formed dashboard_set_layout action carrying the
// chosen layout name to the session's daemon socket.
func TestSendSetLayoutEnvelope(t *testing.T) {
	sess := "test-dashlayout-" + time.Now().Format("150405.000000")
	sockPath := daemon.SocketPath(sess)
	_ = os.Remove(sockPath)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	defer os.Remove(sockPath)

	got := make(chan daemon.Message, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		line, err := bufio.NewReader(conn).ReadBytes('\n')
		if err != nil {
			return
		}
		var msg daemon.Message
		if json.Unmarshal(line, &msg) == nil {
			got <- msg
		}
	}()

	if err := sendSetLayout(sess, "main-vertical"); err != nil {
		t.Fatalf("sendSetLayout: %v", err)
	}

	select {
	case msg := <-got:
		if msg.Type != daemon.MsgInput {
			t.Errorf("Type = %q, want %q", msg.Type, daemon.MsgInput)
		}
		if msg.Target.Kind != daemon.TargetHook {
			t.Errorf("Target.Kind = %q, want %q", msg.Target.Kind, daemon.TargetHook)
		}
		// Payload round-trips as a generic map after JSON decode.
		payload, ok := msg.Payload.(map[string]any)
		if !ok {
			t.Fatalf("Payload type %T, want map", msg.Payload)
		}
		if payload["resolved_action"] != "dashboard_set_layout" {
			t.Errorf("resolved_action = %v, want dashboard_set_layout", payload["resolved_action"])
		}
		if payload["resolved_target"] != "main-vertical" {
			t.Errorf("resolved_target = %v, want main-vertical", payload["resolved_target"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for daemon message")
	}
}

func TestSendSetLayoutEmptySession(t *testing.T) {
	if err := sendSetLayout("", "tiled"); err == nil {
		t.Errorf("expected error for empty session id")
	}
}
