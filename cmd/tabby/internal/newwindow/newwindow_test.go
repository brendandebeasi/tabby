package newwindow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	tabbycfg "github.com/brendandebeasi/tabby/pkg/config"
	"github.com/brendandebeasi/tabby/pkg/paths"
)

func TestExpandWorkingDir(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("user home dir not available")
	}

	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"   ", ""},
		{"~", filepath.Clean(home)},
		{"~/projects", filepath.Clean(filepath.Join(home, "projects"))},
		{"/var/log/", "/var/log"},
	}

	for _, tc := range cases {
		got := expandWorkingDir(tc.in)
		if got != tc.want {
			t.Errorf("expandWorkingDir(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestResolveDirColor_CWDColorsJSON(t *testing.T) {
	stateFile := paths.StatePath("cwd-colors.json")
	_ = os.MkdirAll(filepath.Dir(stateFile), 0755)
	t.Cleanup(func() { os.Remove(stateFile) })

	data := map[string]struct {
		Color string `json:"color"`
	}{
		"/tmp/repo": {Color: "#123456"},
	}
	bytes, _ := json.Marshal(data)
	if err := os.WriteFile(stateFile, bytes, 0644); err != nil {
		t.Fatalf("failed to write mock state: %v", err)
	}

	color := resolveDirColor("/tmp/repo", nil)
	if color != "#123456" {
		t.Errorf("resolveDirColor(/tmp/repo) = %q, want %q", color, "#123456")
	}

	// Unregistered directory with no config
	unregistered := resolveDirColor("/tmp/other", nil)
	if unregistered != "" {
		t.Errorf("resolveDirColor(/tmp/other) = %q, want empty", unregistered)
	}
}

func TestResolveDirGroup_ConfigWorkingDir(t *testing.T) {
	cfg := &tabbycfg.Config{
		Groups: []tabbycfg.Group{
			{
				Name:       "Frontend",
				WorkingDir: "/projects/frontend",
				Theme: tabbycfg.Theme{
					Bg:   "#ff5500",
					Icon: "🌐",
				},
			},
			{
				Name:       "SubModule",
				WorkingDir: "/projects/frontend/sub",
				Theme: tabbycfg.Theme{
					Bg:   "#00ff55",
					Icon: "📦",
				},
			},
		},
	}

	// Exact match
	if g := resolveDirGroup("/projects/frontend", cfg); g == nil || g.Name != "Frontend" {
		t.Errorf("resolveDirGroup(/projects/frontend) = %v, want group Frontend", g)
	}

	// Subdirectory match
	if g := resolveDirGroup("/projects/frontend/src", cfg); g == nil || g.Name != "Frontend" {
		t.Errorf("resolveDirGroup(/projects/frontend/src) = %v, want group Frontend", g)
	}

	// Nested match
	if g := resolveDirGroup("/projects/frontend/sub/deep", cfg); g == nil || g.Name != "SubModule" {
		t.Errorf("resolveDirGroup(/projects/frontend/sub/deep) = %v, want group SubModule", g)
	}

	// Unrelated directory -> returns nil (default)
	if g := resolveDirGroup("/projects/backend", cfg); g != nil {
		t.Errorf("resolveDirGroup(/projects/backend) = %v, want nil", g)
	}
}

func TestResolveDirColor_ConfigWorkingDir(t *testing.T) {
	cfg := &tabbycfg.Config{
		Groups: []tabbycfg.Group{
			{
				Name:       "Frontend",
				WorkingDir: "/projects/frontend",
				Theme: tabbycfg.Theme{
					Bg: "#ff5500",
				},
			},
			{
				Name:       "SubModule",
				WorkingDir: "/projects/frontend/sub",
				Theme: tabbycfg.Theme{
					Bg: "#00ff55",
				},
			},
		},
	}

	// Exact match
	if got := resolveDirColor("/projects/frontend", cfg); got != "#ff5500" {
		t.Errorf("resolveDirColor(/projects/frontend) = %q, want #ff5500", got)
	}

	// Subdirectory match (inherited)
	if got := resolveDirColor("/projects/frontend/src", cfg); got != "#ff5500" {
		t.Errorf("resolveDirColor(/projects/frontend/src) = %q, want #ff5500", got)
	}

	// More specific nested match wins
	if got := resolveDirColor("/projects/frontend/sub/deep", cfg); got != "#00ff55" {
		t.Errorf("resolveDirColor(/projects/frontend/sub/deep) = %q, want #00ff55", got)
	}

	// Unrelated directory
	if got := resolveDirColor("/projects/backend", cfg); got != "" {
		t.Errorf("resolveDirColor(/projects/backend) = %q, want empty", got)
	}
}

func TestResolveDirColor_CWDColorsOverridesConfigWorkingDir(t *testing.T) {
	stateFile := paths.StatePath("cwd-colors.json")
	_ = os.MkdirAll(filepath.Dir(stateFile), 0755)
	t.Cleanup(func() { os.Remove(stateFile) })

	data := map[string]struct {
		Color string `json:"color"`
	}{
		"/projects/frontend": {Color: "#abcdef"},
	}
	bytes, _ := json.Marshal(data)
	_ = os.WriteFile(stateFile, bytes, 0644)

	cfg := &tabbycfg.Config{
		Groups: []tabbycfg.Group{
			{
				Name:       "Frontend",
				WorkingDir: "/projects/frontend",
				Theme: tabbycfg.Theme{
					Bg: "#ff5500",
				},
			},
		},
	}

	// Remembered color in cwd-colors.json takes priority over config working_dir
	if got := resolveDirColor("/projects/frontend", cfg); got != "#abcdef" {
		t.Errorf("resolveDirColor(/projects/frontend) = %q, want #abcdef", got)
	}
}
