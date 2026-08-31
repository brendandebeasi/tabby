package hook

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

type seen struct {
	prefix  string
	payload string
}

func scanAll(t *testing.T, in io.Reader) []seen {
	t.Helper()
	var got []seen
	scanOSCStream(in, func(prefix, payload string) {
		got = append(got, seen{prefix, payload})
	})
	return got
}

func TestScanOSCStreamFindsEachForm(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prefix  string
		payload string
	}{
		{"raw indicator", tabbyOSCPrefix, "busy;1"},
		{"dcs indicator", tabbyOSCPrefixDCS, "busy;1"},
		{"raw cwd", tabbyCWDPrefix, "buildbox\x1f/srv/app"},
		{"dcs cwd", tabbyCWDPrefixDCS, "buildbox\x1f/srv/app"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := "noise before\r\n" + tc.prefix + tc.payload + "\x07" + "noise after\r\n"
			got := scanAll(t, strings.NewReader(in))
			if len(got) != 1 {
				t.Fatalf("got %d sequences, want 1: %+v", len(got), got)
			}
			if got[0].prefix != tc.prefix || got[0].payload != tc.payload {
				t.Errorf("got %q/%q, want %q/%q", got[0].prefix, got[0].payload, tc.prefix, tc.payload)
			}
		})
	}
}

// A DCS-wrapped sequence contains its raw counterpart one byte in. Matching the
// raw prefix there would take the payload from the wrong offset and, worse,
// report the wrong form to the dispatcher.
func TestScanOSCStreamPrefersTheDCSForm(t *testing.T) {
	got := scanAll(t, strings.NewReader(tabbyOSCPrefixDCS+"busy;1\x07"))
	if len(got) != 1 {
		t.Fatalf("got %d sequences, want 1: %+v", len(got), got)
	}
	if got[0].prefix != tabbyOSCPrefixDCS {
		t.Errorf("matched the raw prefix inside a DCS sequence: %q", got[0].prefix)
	}
	if got[0].payload != "busy;1" {
		t.Errorf("payload = %q, want %q", got[0].payload, "busy;1")
	}
}

// The byte-at-a-time loop this replaced cleared its whole window on a match, so
// a second sequence already sitting in the buffer was thrown away with it. A
// shell prompt hook that emits an indicator and a cwd back to back hit this
// every time.
func TestScanOSCStreamAppliesEverySequenceInOneRead(t *testing.T) {
	in := tabbyOSCPrefix + "busy;1\x07" + "some output\r\n" + tabbyCWDPrefix + "host\x1f/srv\x07"
	got := scanAll(t, strings.NewReader(in))
	if len(got) != 2 {
		t.Fatalf("got %d sequences, want 2: %+v", len(got), got)
	}
	if got[0].payload != "busy;1" || got[1].payload != "host\x1f/srv" {
		t.Errorf("wrong payloads: %+v", got)
	}
}

// pipe-pane hands over whatever the pty had ready, so a sequence is routinely
// split across reads. iotest.OneByteReader is the worst case of that.
func TestScanOSCStreamSurvivesSplitReads(t *testing.T) {
	in := "prelude\r\n" + tabbyOSCPrefix + "busy;1\x07" + strings.Repeat("x", 40) + tabbyCWDPrefixDCS + "h\x1f/p\x07"
	got := scanAll(t, iotest.OneByteReader(strings.NewReader(in)))
	if len(got) != 2 {
		t.Fatalf("got %d sequences, want 2: %+v", len(got), got)
	}
	if got[0].prefix != tabbyOSCPrefix || got[1].prefix != tabbyCWDPrefixDCS {
		t.Errorf("wrong prefixes: %+v", got)
	}
}

// A pane printing megabytes between sequences must not accumulate them. The
// carried window is bounded by the shortest prefix when nothing is in flight.
func TestScanOSCStreamDoesNotRetainOrdinaryOutput(t *testing.T) {
	noise := bytes.Repeat([]byte("plain terminal output\r\n"), 4096)
	window := consumeOSC(noise, func(string, string) {
		t.Error("matched a sequence in output that has none")
	})
	if len(window) > oscKeepTail {
		t.Errorf("carried %d bytes of ordinary output, want at most %d", len(window), oscKeepTail)
	}
}

// A prefix whose terminator never arrives — a pane cat-ing a log that contains
// one, a truncated write — must not wedge the scanner. Before the bound, the
// partial was carried forever and every subsequent byte was rescanned against
// it for the life of the pane.
func TestScanOSCStreamAbandonsAnUnterminatedPrefix(t *testing.T) {
	in := tabbyOSCPrefix + strings.Repeat("z", oscMaxPending+64) +
		tabbyCWDPrefix + "host\x1f/srv\x07"
	got := scanAll(t, strings.NewReader(in))
	if len(got) != 1 {
		t.Fatalf("got %d sequences, want the one real sequence: %+v", len(got), got)
	}
	if got[0].prefix != tabbyCWDPrefix || got[0].payload != "host\x1f/srv" {
		t.Errorf("got %q/%q", got[0].prefix, got[0].payload)
	}
}

// An unterminated prefix at the end of the stream is a partial sequence, not a
// dropped one: it is carried, and simply never completes.
func TestScanOSCStreamIgnoresATruncatedSequence(t *testing.T) {
	got := scanAll(t, strings.NewReader("output\r\n"+tabbyOSCPrefix+"busy;1"))
	if len(got) != 0 {
		t.Errorf("applied a sequence with no terminator: %+v", got)
	}
}

// An empty payload is a real case (a bare terminator), and reslicing it wrong
// would panic rather than misbehave quietly.
func TestScanOSCStreamHandlesAnEmptyPayload(t *testing.T) {
	got := scanAll(t, strings.NewReader(tabbyOSCPrefix+"\x07"))
	if len(got) != 1 || got[0].payload != "" {
		t.Fatalf("got %+v, want one empty payload", got)
	}
}

func oscBenchCorpus() []byte {
	var b bytes.Buffer
	line := "\x1b[32m2026-08-31 14:53:21\x1b[0m \x1b[1mINFO\x1b[0m building package foo/bar/baz ... ok (0.42s)\r\n"
	for b.Len() < 1<<20 {
		b.WriteString(line)
	}
	return b.Bytes()
}

// Every byte a piped pane prints goes through this, so the number that matters
// is throughput on output containing no sequence at all. The byte-at-a-time
// loop this replaced managed 0.19 MB/s and allocated 2.58GB per megabyte.
func BenchmarkScanOSCStream(b *testing.B) {
	corpus := oscBenchCorpus()
	b.SetBytes(int64(len(corpus)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		scanOSCStream(bytes.NewReader(corpus), func(string, string) {})
	}
}

// The scan runs on every byte of every piped pane, so the steady state has to
// be allocation-free. The version this replaced allocated a copy of its whole
// window per input byte, which is the entire reason it cost what it did.
func TestConsumeOSCDoesNotAllocateOnOrdinaryOutput(t *testing.T) {
	noise := bytes.Repeat([]byte("plain terminal output\r\n"), 1024)
	window := make([]byte, 0, len(noise)+oscKeepTail)
	allocs := testing.AllocsPerRun(50, func() {
		consumeOSC(append(window[:0], noise...), func(string, string) {})
	})
	if allocs != 0 {
		t.Errorf("consumeOSC allocated %v times per scan, want 0", allocs)
	}
}

// A sequence that does arrive costs two strings, the prefix and the payload,
// and nothing else. Sequences are rare enough that this is not worth removing,
// but it is worth knowing if it grows.
func TestConsumeOSCAllocatesOnlyForAMatch(t *testing.T) {
	in := []byte("output\r\n" + tabbyOSCPrefix + "busy;1\x07" + "more output\r\n")
	window := make([]byte, 0, len(in)+oscKeepTail)
	allocs := testing.AllocsPerRun(50, func() {
		consumeOSC(append(window[:0], in...), func(string, string) {})
	})
	if allocs > 2 {
		t.Errorf("consumeOSC allocated %v times for one sequence, want at most 2", allocs)
	}
}
