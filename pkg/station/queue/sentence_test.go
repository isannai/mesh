package queue

import (
	"strings"
	"testing"
)

// segAll feeds tokens through a segmenter and returns all emitted chunks plus
// the final flush (if non-empty).
func segAll(mode string, maxLen int, tokens ...string) []string {
	s := NewSegmenter(mode, maxLen)
	var out []string
	for _, tk := range tokens {
		out = append(out, s.Feed(tk)...)
	}
	if f := s.Flush(); f != "" {
		out = append(out, f)
	}
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSegmenterSentence(t *testing.T) {
	cases := []struct {
		name   string
		mode   string
		tokens []string
		want   []string
	}{
		{
			// Delimiters (terminator + trailing space) are retained so chunks
			// reconstruct the original verbatim.
			name:   "basic sentences (one token)",
			mode:   ChunkModeSentence,
			tokens: []string{"Hello world. How are you? Fine!"},
			want:   []string{"Hello world. ", "How are you? ", "Fine!"},
		},
		{
			name:   "token by token (deltas)",
			mode:   ChunkModeSentence,
			tokens: []string{"Hello", " world.", " How", " are you?", " Fine!"},
			want:   []string{"Hello world. ", "How are you? ", "Fine!"},
		},
		{
			name:   "decimal not split",
			mode:   ChunkModeSentence,
			tokens: []string{"Pi is 3.14 today."},
			want:   []string{"Pi is 3.14 today."},
		},
		{
			name:   "consecutive terminators collapse",
			mode:   ChunkModeSentence,
			tokens: []string{"Really?! Yes... ok"},
			want:   []string{"Really?! ", "Yes... ", "ok"},
		},
		{
			// Newlines are retained — load-bearing for tables/code/lists.
			name:   "newline is a hard boundary",
			mode:   ChunkModeSentence,
			tokens: []string{"line one\nline two\n\nlast"},
			want:   []string{"line one\n", "line two\n\n", "last"},
		},
		{
			name:   "CJK fullwidth terminators",
			mode:   ChunkModeSentence,
			tokens: []string{"안녕하세요。 반갑습니다！ 잘가요？ 끝"},
			want:   []string{"안녕하세요。 ", "반갑습니다！ ", "잘가요？ ", "끝"},
		},
		{
			name:   "closing quote trails terminator",
			mode:   ChunkModeSentence,
			tokens: []string{`He said "stop." Then go.`},
			want:   []string{`He said "stop." `, "Then go."},
		},
		{
			name:   "surrounding whitespace retained (faithful)",
			mode:   ChunkModeSentence,
			tokens: []string{"  Hello.   World.  "},
			want:   []string{"  Hello.   ", "World.  "},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := segAll(tc.mode, 0, tc.tokens...)
			if !eq(got, tc.want) {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSegmenterModes(t *testing.T) {
	// Semicolon: boundary in sentence/low_latency, not in strict.
	if got := segAll(ChunkModeStrict, 0, "a; b. c"); !eq(got, []string{"a; b. ", "c"}) {
		t.Errorf("strict semicolon: got %q", got)
	}
	if got := segAll(ChunkModeSentence, 0, "a; b. c"); !eq(got, []string{"a; ", "b. ", "c"}) {
		t.Errorf("sentence semicolon: got %q", got)
	}
	// Comma: boundary only in low_latency.
	if got := segAll(ChunkModeSentence, 0, "Hello, world. Bye"); !eq(got, []string{"Hello, world. ", "Bye"}) {
		t.Errorf("sentence comma: got %q", got)
	}
	if got := segAll(ChunkModeLowLatency, 0, "Hello, world. Bye"); !eq(got, []string{"Hello, ", "world. ", "Bye"}) {
		t.Errorf("low_latency comma: got %q", got)
	}
	// Unknown mode falls back to default (sentence).
	if got := segAll("bogus", 0, "a; b. c"); !eq(got, []string{"a; ", "b. ", "c"}) {
		t.Errorf("default mode: got %q", got)
	}
}

func TestSegmenterMaxLen(t *testing.T) {
	// No punctuation, maxLen=100, 1000 chars → ten 100-char chunks.
	long := strings.Repeat("a", 1000)
	got := segAll(ChunkModeSentence, 100, long)
	if len(got) != 10 {
		t.Fatalf("force-flush chunks = %d, want 10", len(got))
	}
	total := 0
	for _, c := range got {
		if len([]rune(c)) > 100 {
			t.Errorf("chunk longer than maxLen: %d", len([]rune(c)))
		}
		total += len([]rune(c))
	}
	if total != 1000 {
		t.Errorf("total chars = %d, want 1000", total)
	}
	// With spaces, force-flush cuts at the last space before maxLen.
	got = segAll(ChunkModeSentence, 20, "aaaa bbbb cccc dddd eeee ffff")
	for _, c := range got {
		if len([]rune(c)) > 20 {
			t.Errorf("chunk %q longer than maxLen 20", c)
		}
	}
}

func TestSegmenterFlushOnly(t *testing.T) {
	// A single unterminated fragment comes out only via Flush.
	s := NewSegmenter(ChunkModeSentence, 0)
	if out := s.Feed("partial sentence no end"); len(out) != 0 {
		t.Fatalf("unterminated should not emit: %q", out)
	}
	if f := s.Flush(); f != "partial sentence no end" {
		t.Fatalf("flush = %q", f)
	}
}
