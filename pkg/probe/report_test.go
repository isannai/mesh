package probe

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// shotLine finds the table line of a shot by its id.
func shotLine(t *testing.T, out string, id int64) string {
	t.Helper()
	prefix := "  " + strconv.FormatInt(id, 10) + " "
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, prefix) {
			return l
		}
	}
	t.Fatalf("no table line for shot %d:\n%s", id, out)
	return ""
}

// answeredShot records a finished text shot and returns its id.
func answeredShot(t *testing.T, s *Store, node string, round int, epoch int64, q Question, got string, verdict string) int64 {
	t.Helper()
	if _, err := s.AddQuestions([]Question{q}, time.Now()); err != nil {
		t.Fatal(err)
	}
	var qid int64
	if err := s.db.QueryRow(`SELECT id FROM question WHERE q=?`, q.Q).Scan(&qid); err != nil {
		t.Fatal(err)
	}
	id, err := s.RecordShot(Shot{FiredAt: time.Now(), NodeID: node, Outcome: OutcomeSubmitted, Round: round, AssignEpoch: epoch, QuestionID: qid})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteShot(id, time.Now(), 1769, 6, 6, got, OutcomeAnswered, verdict); err != nil {
		t.Fatal(err)
	}
	return id
}

var (
	qVienna = Question{Category: CatGeography, Q: "Vienna is the capital of which country?", Draft: "Austria",
		Fewshot: []QA{{Q: "a", A: "b"}, {Q: "c", A: "d"}}}
	qSum = Question{Category: CatMath, Q: "What is 666 + 704?", Draft: "1370",
		Fewshot: []QA{{Q: "a", A: "b"}, {Q: "c", A: "d"}}}
)

// A text node is asked twice a slot, so the report says which of the two a shot was,
// and the table carries id, time, epoch, node, round, latency, tokens, the answer
// received and the result on one line.
func TestReportTableLine(t *testing.T) {
	s := newTestStore(t)
	id := answeredShot(t, s, "S:0xabcdef0123456789", 2, 165940, qVienna, "Austria", VerdictPass)
	out, err := s.Report(ReportOptions{Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	for _, head := range []string{"ID", "TIME", "EPOCH", "NODE", "ROUND", "LATENCY", "TOKENS", "GOT", "RESULT"} {
		if !strings.Contains(out, head) {
			t.Errorf("the table header lacks %s:\n%s", head, out)
		}
	}
	f := strings.Fields(shotLine(t, out, id))
	// id, date, time, epoch, node, round, latency, tokens, got, result
	want := []string{strconv.FormatInt(id, 10), "", "", "165940", "S:0xabcdef01…", "2", "1769ms", "6", "Austria", "pass"}
	if len(f) != len(want) {
		t.Fatalf("columns = %v, want %d of them", f, len(want))
	}
	for i, w := range want {
		if w != "" && f[i] != w {
			t.Errorf("column %d = %q, want %q (%v)", i, f[i], w, f)
		}
	}
}

// A row from before the epoch column was stamped still shows the slot: the time
// divided into three hour slots.
func TestReportEpochFallsBackToTheTime(t *testing.T) {
	s := newTestStore(t)
	at := time.Unix(165940*10800+3600, 0) // an hour into slot 165940
	id, err := s.RecordShot(Shot{FiredAt: at, NodeID: "S:0xabcdef0123456789", Outcome: OutcomeTimeout, Round: 1})
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.Report(ReportOptions{Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if line := shotLine(t, out, id); !strings.Contains(line, " 165940 ") {
		t.Errorf("the slot of an unstamped shot is not derived from its time:\n%s", line)
	}
}

// With no verdict the result is how the shot ended, so a timeout is not a blank.
func TestReportResultIsTheOutcomeWithoutAVerdict(t *testing.T) {
	s := newTestStore(t)
	id, err := s.RecordShot(Shot{FiredAt: time.Now(), NodeID: "S:0xabcdef0123456789", Outcome: OutcomeTimeout, Round: 1})
	if err != nil {
		t.Fatal(err)
	}
	out, _ := s.Report(ReportOptions{Limit: 5})
	f := strings.Fields(shotLine(t, out, id))
	if got := f[len(f)-1]; got != "timeout" {
		t.Errorf("result = %q, want timeout", got)
	}
}

// --id shows one shot and none of the other sections.
func TestReportOneShotByID(t *testing.T) {
	s := newTestStore(t)
	a := answeredShot(t, s, "S:0xaaaaaaaaaaaaaaaa", 1, 165940, qVienna, "Austria", VerdictPass)
	b := answeredShot(t, s, "S:0xbbbbbbbbbbbbbbbb", 2, 165940, qSum, "1371", VerdictFail)

	out, err := s.Report(ReportOptions{ShotID: b})
	if err != nil {
		t.Fatal(err)
	}
	shotLine(t, out, b)
	if strings.Contains(out, "0xaaaaaaaa") {
		t.Errorf("the report of shot %d also shows shot %d:\n%s", b, a, out)
	}
	for _, section := range []string{"QUEUE", "OBSERVATIONS", "BEST TODAY"} {
		if strings.Contains(out, section) {
			t.Errorf("a single shot report carries the %s section:\n%s", section, out)
		}
	}
	if strings.Contains(out, "expect") {
		t.Errorf("the question is shown without -a:\n%s", out)
	}
}

// -a puts the question, the expected answer and the one received under the shot.
func TestReportDetailShowsQuestionExpectedGot(t *testing.T) {
	s := newTestStore(t)
	id := answeredShot(t, s, "S:0xbbbbbbbbbbbbbbbb", 2, 165940, qSum, "1371", VerdictFail)
	out, err := s.Report(ReportOptions{ShotID: id, Detail: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Q:        What is 666 + 704?", `expect:   "1370"`, `got:      "1371"`} {
		if !strings.Contains(out, want) {
			t.Errorf("the detail lacks %q:\n%s", want, out)
		}
	}
}

// The table still lines up when a detail is written under each row.
func TestReportDetailKeepsTheColumnsAligned(t *testing.T) {
	s := newTestStore(t)
	answeredShot(t, s, "S:0xaaaaaaaaaaaaaaaa", 1, 165940, qVienna, "Austria", VerdictPass)
	answeredShot(t, s, "S:0xbbbbbbbbbbbbbbbb", 2, 165940, qSum, "1371", VerdictFail)
	out, err := s.Report(ReportOptions{Limit: 5, Detail: true})
	if err != nil {
		t.Fatal(err)
	}
	var col []int
	for _, l := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(l, "  ID"):
			col = append(col, strings.Index(l, "NODE"))
		case strings.Contains(l, " S:0x"):
			col = append(col, strings.Index(l, "S:0x"))
		}
	}
	if len(col) != 3 {
		t.Fatalf("expected the header and two rows, got %v:\n%s", col, out)
	}
	if col[0] != col[1] || col[1] != col[2] {
		t.Errorf("the NODE column moved between rows %v:\n%s", col, out)
	}
}

// An id that is not there is an error, not an empty report.
func TestReportUnknownShotID(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Report(ReportOptions{ShotID: 99}); err == nil || !strings.Contains(err.Error(), "99") {
		t.Errorf("err = %v, want it to name the missing id", err)
	}
}

// --node finds the shots of a node by any part of its id: a prefix is enough, and
// an id copied out of the table, which ends in the cut mark, works too. Other
// sections are left out of a search.
func TestReportFiltersByNode(t *testing.T) {
	s := newTestStore(t)
	a := answeredShot(t, s, "S:0xaaaaaaaa11112222", 1, 165940, qVienna, "Austria", VerdictPass)
	b := answeredShot(t, s, "S:0xBBBBBBBB33334444", 2, 165940, qSum, "1371", VerdictFail)

	for _, needle := range []string{"0xbbbbbbbb", "S:0xBBBBBBBB…", "  3334  "} {
		out, err := s.Report(ReportOptions{Node: needle, Limit: 20})
		if err != nil {
			t.Fatalf("node %q: %v", needle, err)
		}
		shotLine(t, out, b)
		if strings.Contains(out, "0xaaaaaaaa") {
			t.Errorf("node %q also lists the other node:\n%s", needle, out)
		}
		if strings.Contains(out, "QUEUE") {
			t.Errorf("a node search carries the queue section:\n%s", out)
		}
	}
	_ = a
}

// The limit counts the node's own shots, newest first.
func TestReportNodeLimit(t *testing.T) {
	s := newTestStore(t)
	for i := 0; i < 3; i++ {
		answeredShot(t, s, "S:0xaaaaaaaa11112222", 1, 165940, Question{Category: CatMath, Q: "q" + strconv.Itoa(i), Draft: "1",
			Fewshot: []QA{{Q: "a", A: "b"}, {Q: "c", A: "d"}}}, "1", VerdictPass)
		answeredShot(t, s, "S:0xbbbbbbbb33334444", 1, 165940, Question{Category: CatMath, Q: "r" + strconv.Itoa(i), Draft: "1",
			Fewshot: []QA{{Q: "a", A: "b"}, {Q: "c", A: "d"}}}, "1", VerdictPass)
	}
	out, err := s.Report(ReportOptions{Node: "aaaaaaaa", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(out, "S:0xaaaaaaaa"); n != 2 {
		t.Errorf("%d lines of the node, want 2 (the limit):\n%s", n, out)
	}
	if strings.Contains(out, "0xbbbbbbbb") {
		t.Errorf("the other node is listed:\n%s", out)
	}
}

// % and _ are text in a node search, not wildcards: "%" must not list every node.
func TestReportNodeSearchTakesWildcardsLiterally(t *testing.T) {
	s := newTestStore(t)
	answeredShot(t, s, "S:0xaaaaaaaa11112222", 1, 165940, qVienna, "Austria", VerdictPass)
	for _, needle := range []string{"%", "_", "0xa_a"} {
		out, err := s.Report(ReportOptions{Node: needle})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out, "S:0xaaaaaaaa") {
			t.Errorf("node %q matched as a wildcard:\n%s", needle, out)
		}
		if !strings.Contains(out, "none - no shot matches the search") {
			t.Errorf("node %q: no empty-result line:\n%s", needle, out)
		}
	}
}

// --epoch finds the shots of one slot, and combines with --node. A row from before
// the epoch was stamped is found by the slot its time falls in.
func TestReportFiltersByEpoch(t *testing.T) {
	s := newTestStore(t)
	a := answeredShot(t, s, "S:0xaaaaaaaa11112222", 1, 165940, qVienna, "Austria", VerdictPass)
	b := answeredShot(t, s, "S:0xaaaaaaaa11112222", 2, 165939, qSum, "1370", VerdictPass)
	c := answeredShot(t, s, "S:0xbbbbbbbb33334444", 1, 165940, Question{Category: CatMath, Q: "What is 1 + 1?", Draft: "2",
		Fewshot: []QA{{Q: "a", A: "b"}, {Q: "c", A: "d"}}}, "2", VerdictPass)
	// fired an hour into slot 165939 and never stamped
	old, err := s.RecordShot(Shot{FiredAt: time.Unix(165939*10800+3600, 0), NodeID: "S:0xcccccccc55556666", Outcome: OutcomeTimeout, Round: 1})
	if err != nil {
		t.Fatal(err)
	}

	has := func(out string, id int64) bool {
		prefix := "  " + strconv.FormatInt(id, 10) + " "
		for _, l := range strings.Split(out, "\n") {
			if strings.HasPrefix(l, prefix) {
				return true
			}
		}
		return false
	}
	out, err := s.Report(ReportOptions{Epoch: 165940})
	if err != nil {
		t.Fatal(err)
	}
	if !has(out, a) || !has(out, c) || has(out, b) || has(out, old) {
		t.Errorf("epoch 165940 should list shots %d and %d only:\n%s", a, c, out)
	}
	if strings.Contains(out, "QUEUE") {
		t.Errorf("an epoch search carries the queue section:\n%s", out)
	}

	out, _ = s.Report(ReportOptions{Epoch: 165939})
	if !has(out, b) || !has(out, old) || has(out, a) {
		t.Errorf("epoch 165939 should list shots %d and %d (the unstamped one by its time):\n%s", b, old, out)
	}

	out, _ = s.Report(ReportOptions{Epoch: 165940, Node: "aaaaaaaa"})
	if !has(out, a) || has(out, c) {
		t.Errorf("epoch and node together should list shot %d only:\n%s", a, out)
	}

	out, err = s.Report(ReportOptions{Epoch: 1})
	if err != nil || !strings.Contains(out, "none - no shot matches the search") {
		t.Errorf("an empty search: err=%v\n%s", err, out)
	}
}
