package probe

// opt.go: the code a node has to write in front of its answer.
//
// Each quiz shot carries a six digit code of its own, and the node is told to
// answer "<code>: <answer>", for example "483917: Paris". A shot passes only when
// the code that comes back is the one that went out AND the answer is right.
//
// WHAT IT SHOWS. The code is new for every shot, so an answer cannot be written
// beforehand, copied from another shot, or replayed from an earlier one: it has to
// be made for this question, and the code in it proves the answer is for this one.
// It also makes the node follow a written format, which a node answering by
// lookup or with a much smaller model than it declares tends to do worse.
//
// WHAT IT DOES NOT SHOW. A node that reads the code out of the prompt and writes
// it back in front of an answer it got elsewhere passes this check; the answer
// itself is what the quiz is scored on (score.go), and the live look at the node's
// own report before a ticket (settle.go) is what holds it to the budget's model.

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const (
	// optDigits is the length of the code: six digits.
	optDigits = 6
	// optMin and optSpan keep the code in 100000..999999. A code never starts with
	// a zero: a leading zero is the one thing a node or a tokeniser may drop, and
	// a correct answer would then fail the comparison.
	optMin  = 100000
	optSpan = 900000
)

// newOpt returns a fresh code. It is drawn from the system's random source, not
// from a generator the prober seeds, so the next code cannot be worked out from
// the ones already seen.
func newOpt() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return strconv.Itoa(optMin + int(binary.BigEndian.Uint32(b[:])%optSpan))
}

// exampleOpt is the code shown in the i-th worked example of a prompt. It is
// derived from the shot's own code, so it differs from shot to shot (a constant
// would tell any node which prompts are probes) and is the same every time the
// same prompt is built. It never equals the shot's code or the other example's.
func exampleOpt(opt string, i int) string {
	for salt := 0; ; salt++ {
		sum := sha256.Sum256([]byte(fmt.Sprintf("probe-opt-example:%s:%d:%d", opt, i, salt)))
		c := strconv.Itoa(optMin + int(binary.BigEndian.Uint32(sum[:4])%optSpan))
		if c == opt {
			continue
		}
		if i > 0 && c == exampleOpt(opt, i-1) {
			continue
		}
		return c
	}
}

// optHead matches the code at the front of a reply: exactly six digits, then a
// colon (the full width one too, which an engine writing CJK punctuation uses),
// then the answer. A seventh digit, or digits that are not followed by a colon,
// is not a code.
var optHead = regexp.MustCompile(`^(\d{6})\s*[:：]\s*(.*)$`)

// splitOpt reads "<code>: <answer>" off the front of a node's reply and returns
// the code and the answer after it. ok is false when the reply does not start
// that way. Only the first line is read, as everywhere else in the scoring.
func splitOpt(reply string) (opt, answer string, ok bool) {
	t := strings.TrimSpace(reply)
	if i := strings.IndexAny(t, "\r\n"); i >= 0 {
		t = t[:i]
	}
	// An echoed "A:" is our own label coming back; see stripAnswerLabel.
	t = strings.TrimSpace(stripAnswerLabel(t))
	m := optHead.FindStringSubmatch(t)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}
