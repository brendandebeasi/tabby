package renderer

import (
	"io"
	"os"
)

// FrameDropper is an io.Writer that never blocks. Each Write hands the bytes
// to a forwarder goroutine over a bounded queue; once the queue is full the
// newest bytes are dropped instead of blocking the caller.
//
// Why: a pane whose pty stops being drained (tmux backing up behind a slow
// client — classically a phone on mosh) turns any stdout write into a parked
// goroutine. Bubble Tea's renderer holds its mutex across that write, so the
// whole program freezes — events, reconnect, even Kill, whose shutdown path
// itself writes to stdout. That was the "dead sidebar that ignores SIGTERM"
// bug: unkillable without -9, frozen on screen.
//
// (SetWriteDeadline looked like the fix but is a no-op on a pty: "file type
// does not support deadline".)
//
// The queue keeps the byte stream ordered, so when draining resumes the
// screen converges to a consistent state; intermediate frames are just stale.
type FrameDropper struct {
	out    io.Writer
	frames chan []byte
	done   chan struct{}
}

// NewFrameDropper wraps out with a never-blocking writer. queueLen bounds how
// many frames can wait for a drained-out pty before new ones get dropped.
func NewFrameDropper(out io.Writer, queueLen int) *FrameDropper {
	d := &FrameDropper{
		out:    out,
		frames: make(chan []byte, queueLen),
		done:   make(chan struct{}),
	}
	go d.forward()
	return d
}

func (d *FrameDropper) Write(p []byte) (int, error) {
	buf := make([]byte, len(p))
	copy(buf, p)
	select {
	case d.frames <- buf:
	default:
	}
	return len(p), nil
}

func (d *FrameDropper) forward() {
	for p := range d.frames {
		d.out.Write(p)
	}
	close(d.done)
}

// Close drains the queue and stops the forwarder. Only meaningful at shutdown;
// callers that exit right after may drop the tail of the queue by design.
func (d *FrameDropper) Close() {
	close(d.frames)
	<-d.done
}

// StdoutSink is the process-wide dropper in front of stdout. Tea programs
// should render into it (tea.WithOutput) and ResetTerminal writes through it,
// so no code path in a renderer ever blocks on the pty.
var StdoutSink = NewFrameDropper(os.Stdout, 256)
