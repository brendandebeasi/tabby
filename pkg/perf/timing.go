package perf

import (
	"fmt"
	"os"
	"sync"
	"time"
)

var (
	// Set TABBY_PERF=1 to enable performance logging
	enabled   = os.Getenv("TABBY_PERF") == "1"
	logFile   *os.File
	logMutex  sync.Mutex
	initOnce  sync.Once
)

func init() {
	if enabled {
		initOnce.Do(func() {
			var err error
			logFile, err = os.OpenFile("/tmp/tabby-perf.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
			if err != nil {
				enabled = false
			}
		})
	}
}

// Timer tracks elapsed time for a named operation
type Timer struct {
	name  string
	start time.Time
}

// Start begins timing an operation
func Start(name string) *Timer {
	return &Timer{
		name:  name,
		start: time.Now(),
	}
}

// Stop ends timing and logs the result
func (t *Timer) Stop() time.Duration {
	elapsed := time.Since(t.start)
	if enabled && logFile != nil {
		logMutex.Lock()
		fmt.Fprintf(logFile, "%s: %s: %v\n", time.Now().Format("15:04:05.000"), t.name, elapsed)
		logMutex.Unlock()
	}
	return elapsed
}

// Track is a convenience function that times a function call
func Track(name string, fn func()) time.Duration {
	t := Start(name)
	fn()
	return t.Stop()
}

// Log writes a custom message to the perf log
func Log(format string, args ...interface{}) {
	if enabled && logFile != nil {
		logMutex.Lock()
		fmt.Fprintf(logFile, "%s: ", time.Now().Format("15:04:05.000"))
		fmt.Fprintf(logFile, format+"\n", args...)
		logMutex.Unlock()
	}
}

// IsEnabled returns whether performance logging is enabled
func IsEnabled() bool {
	return enabled
}
