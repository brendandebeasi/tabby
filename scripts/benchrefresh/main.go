// Command benchrefresh times the real tmux round-trips behind RefreshWindows
// against a private tmux server, so the cost can be attributed by window count
// without waiting on a live session to produce log samples.
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/brendandebeasi/tabby/pkg/tmux"
)

func pct(d []time.Duration, p float64) time.Duration {
	if len(d) == 0 {
		return 0
	}
	i := int(float64(len(d)) * p)
	if i >= len(d) {
		i = len(d) - 1
	}
	return d[i]
}

func summarize(name string, d []time.Duration) {
	sort.Slice(d, func(a, b int) bool { return d[a] < d[b] })
	fmt.Printf("  %-22s n=%d p50=%-7v p90=%-7v max=%v\n",
		name, len(d), pct(d, 0.5).Round(time.Millisecond),
		pct(d, 0.9).Round(time.Millisecond), d[len(d)-1].Round(time.Millisecond))
}

func main() {
	iters := flag.Int("iters", 20, "iterations")
	label := flag.String("label", "", "label for this run")
	flag.String("socket", "", "tmux socket (applied via PATH shim by the driver)")
	flag.Parse()

	var full, list, panes []time.Duration
	for i := 0; i < *iters; i++ {
		t0 := time.Now()
		if _, err := tmux.ListWindows(); err != nil {
			fmt.Fprintf(os.Stderr, "ListWindows: %v\n", err)
			os.Exit(1)
		}
		t1 := time.Now()
		if _, err := tmux.ListAllPanes(); err != nil {
			fmt.Fprintf(os.Stderr, "ListAllPanes: %v\n", err)
			os.Exit(1)
		}
		t2 := time.Now()
		if _, err := tmux.ListWindowsWithPanes(); err != nil {
			fmt.Fprintf(os.Stderr, "ListWindowsWithPanes: %v\n", err)
			os.Exit(1)
		}
		t3 := time.Now()

		list = append(list, t1.Sub(t0))
		panes = append(panes, t2.Sub(t1))
		full = append(full, t3.Sub(t2))
	}

	fmt.Printf("%s\n", *label)
	summarize("ListWindows", list)
	summarize("ListAllPanes", panes)
	summarize("ListWindowsWithPanes", full)
}
