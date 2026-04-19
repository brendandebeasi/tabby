// Package renderdispatch handles the `tabby render <sidebar|window-header|
// pane-header|sidebar-popup>` second-level subcommand dispatch. Each of the
// four renderers lives in its own package; this file routes by name.
package renderdispatch

import (
	"fmt"
	"os"

	"github.com/brendandebeasi/tabby/cmd/tabby/internal/paneheader"
	"github.com/brendandebeasi/tabby/cmd/tabby/internal/sidebar"
	"github.com/brendandebeasi/tabby/cmd/tabby/internal/sidebarpopup"
	"github.com/brendandebeasi/tabby/cmd/tabby/internal/windowheader"
)

func Run(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: tabby render <sidebar|window-header|pane-header|sidebar-popup> [args...]")
		return 2
	}
	rest := args[1:]
	switch args[0] {
	case "sidebar":
		return sidebar.Run(rest)
	case "window-header":
		return windowheader.Run(rest)
	case "pane-header":
		return paneheader.Run(rest)
	case "sidebar-popup":
		return sidebarpopup.Run(rest)
	default:
		fmt.Fprintf(os.Stderr, "tabby render: unknown renderer %q\n", args[0])
		return 2
	}
}
