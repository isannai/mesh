package queue

// sentence.go — streaming sentence segmenter. The streaming dispatcher feeds
// engine token deltas in; the segmenter emits completed *sentence* chunks
// (req: 문장 단위, TTS 연동). See docs/TODO/infer-streaming.md.
//
// Boundary rule (common to all modes): a terminator char, then optional
// closing quotes/brackets, then **whitespace or EOF**. The trailing-whitespace
// requirement is what protects decimals (`3.14`) and keeps numbers intact.
// Consecutive terminators (`?!`, `...`) collapse into one boundary. A newline
// is always a hard boundary regardless of punctuation. If no boundary is found
// and the buffer exceeds maxLen, a chunk is force-flushed (guards against
// punctuation-free output like code/lists growing unbounded).
//
// Faithful reconstruction: every emitted chunk retains its trailing delimiter
// run (the terminator and the whitespace/newlines that follow it) — each rune
// is emitted in exactly one chunk, so concatenating all chunks reproduces the
// engine's original text verbatim. This is required for free-form output where
// newlines are load-bearing (markdown tables, code blocks, lists). A consumer
// that wants clean sentence text (e.g. TTS) trims each chunk on its side.

import (
	"unicode"
)

// Chunk segmentation modes (request/manifest value). Default = sentence.
const (
	ChunkModeStrict     = "strict"      // . ! ? 。 ！ ？ … + newline
	ChunkModeSentence   = "sentence"    // strict + ;
	ChunkModeLowLatency = "low_latency" // sentence + , : (clause-level)
	DefaultChunkMode    = ChunkModeSentence
)

// defaultMaxChunkLen caps a single chunk (runes) when no boundary appears, so a
// punctuation-free stream still produces chunks instead of buffering forever.
const defaultMaxChunkLen = 400

// terminatorSet returns the sentence-ending runes for a mode.
func terminatorSet(mode string) map[rune]bool {
	set := map[rune]bool{
		'.': true, '!': true, '?': true, // ASCII enders
		'。': true, '！': true, '？': true, '…': true, // CJK enders + ellipsis
	}
	switch mode {
	case ChunkModeSentence:
		set[';'] = true
	case ChunkModeLowLatency:
		set[';'] = true
		set[','] = true
		set[':'] = true
		set['、'] = true // CJK comma
		set['，'] = true // fullwidth comma
	}
	return set
}

// isClosing reports a closing quote/bracket that may trail a terminator before
// the boundary whitespace (e.g. `said "done." Next`).
func isClosing(r rune) bool {
	switch r {
	case '"', '\'', ')', ']', '}', '」', '』', '）', '”', '’':
		return true
	}
	return false
}

// Segmenter accumulates engine tokens and emits completed sentence chunks.
// Not safe for concurrent use — one Segmenter per job/worker.
type Segmenter struct {
	term   map[rune]bool
	maxLen int
	buf    []rune
}

// NewSegmenter builds a segmenter for the mode (unknown → DefaultChunkMode) and
// maxLen (<=0 → defaultMaxChunkLen).
func NewSegmenter(mode string, maxLen int) *Segmenter {
	switch mode {
	case ChunkModeStrict, ChunkModeSentence, ChunkModeLowLatency:
	default:
		mode = DefaultChunkMode
	}
	if maxLen <= 0 {
		maxLen = defaultMaxChunkLen
	}
	return &Segmenter{term: terminatorSet(mode), maxLen: maxLen}
}

// Feed appends a token and returns any sentence chunks that completed. Empty
// (whitespace-only) chunks are dropped.
func (s *Segmenter) Feed(token string) []string {
	if token == "" {
		return nil
	}
	s.buf = append(s.buf, []rune(token)...)
	var out []string
	for {
		chunk, consumed := s.nextBoundary()
		if consumed == 0 {
			break
		}
		s.buf = s.buf[consumed:]
		if chunk != "" {
			out = append(out, chunk)
		}
	}
	return out
}

// Flush returns the remaining (final, possibly unterminated) buffer verbatim
// and clears it. Call once at end of stream. Not trimmed — a trailing newline
// or spaces are part of the original text and must survive reconstruction.
func (s *Segmenter) Flush() string {
	out := string(s.buf)
	s.buf = nil
	return out
}

// nextBoundary scans the buffer for the earliest confirmed boundary. Returns
// the chunk text (verbatim, delimiter retained) and the number of leading runes
// to consume. emit == consume, so no rune is dropped or duplicated. consumed==0
// means "no boundary yet — wait".
func (s *Segmenter) nextBoundary() (string, int) {
	n := len(s.buf)
	for i := 0; i < n; i++ {
		r := s.buf[i]

		// Newline = hard boundary regardless of punctuation. The newline run is
		// kept in this chunk (emit == consume) so it survives reconstruction.
		if r == '\n' || r == '\r' {
			j := i
			for j < n && (s.buf[j] == '\n' || s.buf[j] == '\r') {
				j++
			}
			return string(s.buf[:j]), j
		}

		if s.term[r] {
			// Extend over a terminator run (?!, ...).
			t := i
			for t+1 < n && s.term[s.buf[t+1]] {
				t++
			}
			// Skip trailing closing quotes/brackets.
			k := t + 1
			for k < n && isClosing(s.buf[k]) {
				k++
			}
			if k < n && unicode.IsSpace(s.buf[k]) {
				// Confirmed boundary. Keep the terminator AND the trailing
				// whitespace run in this chunk (emit == consume) so the exact
				// separator survives reconstruction.
				w := k
				for w < n && unicode.IsSpace(s.buf[w]) {
					w++
				}
				return string(s.buf[:w]), w
			}
			if k >= n {
				break // terminator at end, unconfirmed — wait (or force at maxLen below)
			}
			// Followed by non-space (e.g. decimal 3.14) — not a boundary.
			i = t // loop's i++ resumes after the run
			continue
		}
	}

	// No boundary, but buffer too long → force-flush a chunk. Cut at the last
	// space before maxLen and emit exactly up to it (emit == consume); any
	// trailing whitespace stays buffered as the next chunk's leading run, so
	// reconstruction is preserved and no chunk exceeds maxLen.
	if len(s.buf) >= s.maxLen {
		cut := s.maxLen
		for p := s.maxLen - 1; p > 0; p-- {
			if unicode.IsSpace(s.buf[p]) {
				cut = p
				break
			}
		}
		return string(s.buf[:cut]), cut
	}
	return "", 0
}
