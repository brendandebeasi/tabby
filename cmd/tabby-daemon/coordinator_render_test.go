package main

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/brendandebeasi/tabby/pkg/colors"
	"github.com/brendandebeasi/tabby/pkg/config"
)

func newMinimalCoordinator(cfg *config.Config) *Coordinator {
	return &Coordinator{
		config:     cfg,
		bgDetector: colors.NewBackgroundDetector(colors.ThemeModeDark),
	}
}

var updateGolden = flag.Bool("update", false, "update golden files")

func goldenPath(name string) string {
	return filepath.Join("testdata", name+".golden")
}

func checkOrUpdateGolden(t *testing.T, name, got string) {
	t.Helper()
	path := goldenPath(name)
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0755); err != nil {
			t.Fatalf("create testdata: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0644); err != nil {
			t.Fatalf("write golden %s: %v", path, err)
		}
		t.Logf("updated golden: %s", path)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden file missing: %s (run with -update to create it)", path)
	}
	if got != string(want) {
		t.Errorf("golden mismatch for %s\ngot:\n%s\nwant:\n%s", name, got, string(want))
	}
}

func TestGoldenGenerateSidebarHeader(t *testing.T) {
	cfg := &config.Config{}
	cfg.Sidebar.Header.Text = "TEST"
	cfg.Sidebar.Header.Height = 3
	cfg.Sidebar.Header.PaddingBottom = 1
	c := newMinimalCoordinator(cfg)

	out, _ := c.generateSidebarHeader(30, "test-client")
	checkOrUpdateGolden(t, "generateSidebarHeader", stripAnsi(out))
}

func TestGoldenRenderClockWidget(t *testing.T) {
	cfg := &config.Config{}
	cfg.Widgets.Clock.Format = "TICK"
	cfg.Widgets.Clock.Divider = "─"
	c := newMinimalCoordinator(cfg)

	out := c.renderClockWidget(20)
	checkOrUpdateGolden(t, "renderClockWidget", stripAnsi(out))
}

func TestGoldenRenderGitWidget(t *testing.T) {
	cfg := &config.Config{}
	cfg.Widgets.Git.Divider = "─"
	c := newMinimalCoordinator(cfg)
	c.isGitRepo = true
	c.gitBranch = "main"
	c.gitDirty = 3
	c.gitAhead = 1

	out := c.renderGitWidget(25)
	checkOrUpdateGolden(t, "renderGitWidget", stripAnsi(out))
}

func TestGoldenRenderSessionWidget(t *testing.T) {
	cfg := &config.Config{}
	cfg.Widgets.Session.Enabled = true
	c := newMinimalCoordinator(cfg)
	c.sessionName = "test-session"

	out := c.renderSessionWidget(25)
	checkOrUpdateGolden(t, "renderSessionWidget", stripAnsi(out))
}

func TestGoldenRenderClaudeWidget(t *testing.T) {
	cfg := &config.Config{}
	cfg.Widgets.Claude.Enabled = true
	cfg.Widgets.Claude.DBPath = "/nonexistent/path/claude.db"
	c := newMinimalCoordinator(cfg)

	out := c.renderClaudeWidget(25)
	checkOrUpdateGolden(t, "renderClaudeWidget", stripAnsi(out))
}
