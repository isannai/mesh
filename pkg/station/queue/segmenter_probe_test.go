package queue

import (
	"strings"
	"testing"
)

// TestSegmenterReconstructsOriginal is the core guarantee: streaming a response
// through the segmenter and concatenating the emitted chunks (+ final Flush)
// must reproduce the engine's original text byte-for-byte — delimiters
// (terminators, spaces, newlines) included. Regression guard for the bug where
// dropped newlines flattened markdown tables / code blocks onto one line.
func TestSegmenterReconstructsOriginal(t *testing.T) {
	inputs := []string{
		"Hello. World! How are you?",
		"첫 문장이다. 둘째 문장!",
		"Line1\nLine2\nLine3",
		"- item one\n- item two\n",
		"| a | b |\n|---|---|\n| 1 | 2 |\n",
		"```go\nfunc main() {}\n```\n",
		"  leading and trailing.   ",
		"nofinalpunct",
	}
	for _, mode := range []string{ChunkModeStrict, ChunkModeSentence, ChunkModeLowLatency} {
		for _, in := range inputs {
			seg := NewSegmenter(mode, 0)
			var b strings.Builder
			for _, r := range in { // feed rune-by-rune to mimic token streaming
				for _, ch := range seg.Feed(string(r)) {
					b.WriteString(ch)
				}
			}
			b.WriteString(seg.Flush())
			if got := b.String(); got != in {
				t.Errorf("mode=%s reconstruction mismatch:\n  in : %q\n  got: %q", mode, in, got)
			}
		}
	}
}
