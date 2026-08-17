package probe

// store.go — the prober's own record of what it fired and what came back.
//
// SQLite rather than JSON because a row is appended every time a shot is fired
// or answered, and rewriting a whole file for each would get worse as history
// grows. The driver is pure Go (modernc.org/sqlite) and already a direct
// dependency — the gate uses it — so this adds nothing to the build.
//
// WHAT IS DELIBERATELY NOT HERE: counters. "shots fired", "answers received"
// and "passes" are all derived by counting rows. A stored counter and the rows
// it summarises drift apart eventually, and when they disagree there is no way
// to tell which one is lying.
//
// The `verdict` column holds pass/fail, written in the same statement as the
// answer (see CompleteShot). It stays NULL for a shot that never produced one.

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Outcome classifies how a shot ended. Kept as distinct values rather than a
// bool because "the node refused", "the queue was full" and "it never answered"
// are three different statements about a node, and collapsing them would make
// the history unable to answer why a node looks bad.
const (
	OutcomeSubmitted = "submitted"  // accepted, result not fetched yet
	OutcomeAnswered  = "answered"   // result retrieved
	OutcomeQueueFull = "queue_full" // 429 — the node is working, just busy
	OutcomeRefused   = "refused"    // rejected or unreachable
	OutcomeTimeout   = "timeout"    // accepted but never finished in time

	// OutcomeSkipped is OUR failure, not the node's: the prober restarted mid
	// flight, a collection call faulted, the station's result retention expired
	// before we came back for it.
	//
	// 🔴 Every one of those used to land as `refused` or `timeout` — a mark
	// against a node that had done nothing wrong and had usually drawn the
	// picture correctly. Once tickets hang off this history that mark becomes a
	// missing payment, so the distinction has to live in the data and not only
	// in a log line. Scoring ignores these rows.
	OutcomeSkipped = "skipped"
)

// Store is the prober's SQLite database.
type Store struct{ db *sql.DB }

// OpenStore opens (creating if needed) the database at path.
//
// WAL so a separate reader — a status command, an operator with sqlite3 — can
// look at history while the prober is writing. busy_timeout so those readers
// make the writer wait rather than fail.
func OpenStore(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("probe db dir: %w", err)
		}
	}
	db, err := sql.Open("sqlite", path+sqlitePragmas)
	if err != nil {
		return nil, fmt.Errorf("open probe db: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// schema is applied on every open. Every statement is CREATE ... IF NOT EXISTS,
// so opening an existing database is a no-op.
const schema = `
CREATE TABLE IF NOT EXISTS question (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  category     TEXT    NOT NULL,
  q            TEXT    NOT NULL,
  draft_answer TEXT    NOT NULL,
  fewshot      TEXT    NOT NULL,   -- JSON [{q,a},{q,a}]
  created_at   INTEGER NOT NULL,
  consumed_at  INTEGER              -- NULL while still in the queue
);

CREATE TABLE IF NOT EXISTS shot (
  id                INTEGER PRIMARY KEY AUTOINCREMENT,
  fired_at          INTEGER NOT NULL,
  node_id           TEXT    NOT NULL,
  node_addr         TEXT    NOT NULL,
  slash24           TEXT    NOT NULL,
  engine            TEXT,
  service           TEXT,
  model             TEXT,
  model_hash        TEXT,
  job_id            TEXT,
  question_id       INTEGER,
  submit_status     INTEGER,
  fetched_at        INTEGER,
  latency_ms        INTEGER,
  prompt_tokens     INTEGER,
  completion_tokens INTEGER,
  answer_raw        TEXT,
  outcome           TEXT    NOT NULL,
  verdict           TEXT,
  appointment       TEXT,
  deferred          INTEGER, -- rounds we held off before firing this one
  order_json        TEXT     -- image orders only: the checks a later round judges against
);

CREATE TABLE IF NOT EXISTS observation (
  id        INTEGER PRIMARY KEY AUTOINCREMENT,
  seen_at   INTEGER NOT NULL,
  node_id   TEXT    NOT NULL,
  auth_mode TEXT,
  online    INTEGER,
  max_queue INTEGER,
  version   TEXT
);

CREATE INDEX IF NOT EXISTS shot_node_day ON shot(node_id, fired_at);
CREATE INDEX IF NOT EXISTS shot_open     ON shot(outcome, fired_at);
CREATE INDEX IF NOT EXISTS obs_node      ON observation(node_id, seen_at);
CREATE INDEX IF NOT EXISTS q_unconsumed  ON question(consumed_at, category);
-- Backs the duplicate check in AddQuestions. Not UNIQUE: a database that
-- already holds duplicates from before that check must still open.
CREATE INDEX IF NOT EXISTS q_text        ON question(category, q);
`

// addedColumns are columns that arrived after the first release. CREATE TABLE
// IF NOT EXISTS does nothing to a table that already exists, so a database made
// before them would silently keep the old shape and every INSERT naming a new
// column would fail — on an operator's node, with a week of history in it.
//
// ALTER TABLE ADD COLUMN is the whole migration. Re-running it errors with
// "duplicate column name", which is the success case on the second open, so the
// error is swallowed rather than checked: SQLite has no ADD COLUMN IF NOT
// EXISTS, and matching on the message text would break with a driver update.
var addedColumns = []string{
	`ALTER TABLE shot ADD COLUMN deferred INTEGER`,
	`ALTER TABLE shot ADD COLUMN order_json TEXT`,
}

func (s *Store) migrate() error {
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("probe db schema: %w", err)
	}
	for _, stmt := range addedColumns {
		_, _ = s.db.Exec(stmt)
	}
	return nil
}

// ---------------------------------------------------------------------------
// questions
// ---------------------------------------------------------------------------

// AddQuestions appends a generated batch, skipping questions already on record.
// It returns how many were actually new.
//
// 🔴 DUPLICATES ARE THE NORMAL CASE, not an edge case. A model asked twice for
// capital cities returns France and Japan both times — the well-known ones are
// a small set and temperature does not change that. Without this check the
// queue fills with the same twenty questions and the same node gets asked one
// it has already answered, which is exactly the reuse that consuming a question
// once is meant to prevent.
//
// The check spans CONSUMED rows too, not just the queue: a question asked last
// week is no fresher than one asked this morning. Consumed rows are pruned on
// the observation-day horizon, so the space does free up eventually.
//
// Matched on (category, q) rather than on the answer — two different questions
// may share an answer ("What is the capital of France?" and a colour question
// answered "Paris" would not, but numbers collide constantly in arithmetic).
func (s *Store) AddQuestions(qs []Question, now time.Time) (int, error) {
	if len(qs) == 0 {
		return 0, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO question(category,q,draft_answer,fewshot,created_at)
		SELECT ?,?,?,?,?
		 WHERE NOT EXISTS (SELECT 1 FROM question WHERE category = ? AND q = ?)`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	added := 0
	for _, q := range qs {
		res, err := stmt.Exec(string(q.Category), q.Q, q.Draft, encodeFewshot(q.Fewshot), now.UnixMilli(),
			string(q.Category), q.Q)
		if err != nil {
			return added, err
		}
		if n, err := res.RowsAffected(); err == nil {
			added += int(n)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return added, nil
}

// TakeQuestion removes one unconsumed question of the given category from the
// queue and returns it. ok=false when that category is empty.
//
// Marking it consumed in the same transaction as reading it is the whole point:
// a question must never be asked twice, because a repeat is a question whose
// answer may already be known to the node being asked.
func (s *Store) TakeQuestion(cat Category, now time.Time) (Question, bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Question{}, false, err
	}
	defer tx.Rollback()

	var (
		q       Question
		fewshot string
	)
	row := tx.QueryRow(
		`SELECT id,category,q,draft_answer,fewshot FROM question
		  WHERE consumed_at IS NULL AND category = ? ORDER BY id LIMIT 1`, string(cat))
	var catStr string
	if err := row.Scan(&q.ID, &catStr, &q.Q, &q.Draft, &fewshot); err != nil {
		if err == sql.ErrNoRows {
			return Question{}, false, nil
		}
		return Question{}, false, err
	}
	q.Category = Category(catStr)
	q.Fewshot = decodeFewshot(fewshot)

	if _, err := tx.Exec(`UPDATE question SET consumed_at = ? WHERE id = ?`, now.UnixMilli(), q.ID); err != nil {
		return Question{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return Question{}, false, err
	}
	return q, true, nil
}

// QueueDepth counts unconsumed questions per category.
func (s *Store) QueueDepth() (map[Category]int, error) {
	rows, err := s.db.Query(`SELECT category, COUNT(*) FROM question WHERE consumed_at IS NULL GROUP BY category`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[Category]int{}
	for rows.Next() {
		var c string
		var n int
		if err := rows.Scan(&c, &n); err != nil {
			return nil, err
		}
		out[Category(c)] = n
	}
	return out, rows.Err()
}

// PruneConsumed drops questions consumed before cutoff. Their text is preserved
// on the shot row's own columns for as long as scoring needs it.
func (s *Store) PruneConsumed(cutoff time.Time) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM question WHERE consumed_at IS NOT NULL AND consumed_at < ?`, cutoff.UnixMilli())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ---------------------------------------------------------------------------
// shots
// ---------------------------------------------------------------------------

// Shot is one fired probe.
type Shot struct {
	ID               int64
	FiredAt          time.Time
	NodeID           string
	NodeAddr         string
	Slash24          string
	Engine           string
	Service          string
	Model            string
	ModelHash        string
	JobID            string
	QuestionID       int64
	SubmitStatus     int
	FetchedAt        time.Time
	LatencyMs        int64
	PromptTokens     int
	CompletionTokens int
	AnswerRaw        string
	Outcome          string
	Appointment      string

	// Deferred is how many rounds we held off before firing this shot because
	// the node's queue was not empty. Read it next to LatencyMs: a fast time
	// with a high count is a busy node that happens to be quick, while a slow
	// time at zero is a node that was idle and still took that long.
	Deferred int

	// OrderJSON carries the image order (prompt + checks) so a LATER round can
	// judge a picture this one only ordered. The order lived in memory when
	// collection happened inline; it has to outlive the round now, and it has
	// to survive a prober restart or the shot can never be judged at all.
	OrderJSON string
}

// RecordShot writes the submit half of a shot and returns its row id.
func (s *Store) RecordShot(sh Shot) (int64, error) {
	res, err := s.db.Exec(
		// answer_raw is written at INSERT for the image track, which has no
		// question row to recover the order from afterwards. CompleteShot
		// overwrites it with the judged version; a shot that never completes
		// keeps what was ASKED for, which is the only thing that makes a
		// timeout diagnosable at all.
		`INSERT INTO shot(fired_at,node_id,node_addr,slash24,engine,service,model,model_hash,
		                  job_id,question_id,submit_status,outcome,appointment,answer_raw,
		                  deferred,order_json)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		sh.FiredAt.UnixMilli(), sh.NodeID, sh.NodeAddr, sh.Slash24, sh.Engine, sh.Service,
		sh.Model, sh.ModelHash, sh.JobID, sh.QuestionID, sh.SubmitStatus, sh.Outcome, sh.Appointment,
		sh.AnswerRaw, sh.Deferred, sh.OrderJSON)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// CompleteShot fills in the retrieval half.
//
// latencyMs is the prober's own observation — from submit to result in hand. It
// is NOT the node's generation time, and the two must not be conflated: fetching
// late inflates it by however long the prober waited before asking. Kept anyway
// because it bounds the real value from above, and labelled as observed so a
// later reader does not mistake it for something the node reported.
// The verdict is written in the SAME statement as the answer, never by a later
// pass. A ticket is signed at fire time, so a verdict arriving afterwards has no
// ticket left to affect (ticket-model.md). The raw answer is stored alongside it
// so a disputed question stays re-examinable without firing again.
func (s *Store) CompleteShot(id int64, fetchedAt time.Time, latencyMs int64,
	promptTokens, completionTokens int, answer, outcome, verdict string) error {
	_, err := s.db.Exec(
		`UPDATE shot SET fetched_at=?, latency_ms=?, prompt_tokens=?, completion_tokens=?,
		                 answer_raw=?, outcome=?, verdict=? WHERE id=?`,
		fetchedAt.UnixMilli(), latencyMs, promptTokens, completionTokens, answer, outcome, verdict, id)
	return err
}

// FailShot marks a shot that never produced a result.
func (s *Store) FailShot(id int64, at time.Time, outcome string) error {
	_, err := s.db.Exec(`UPDATE shot SET fetched_at=?, outcome=? WHERE id=?`, at.UnixMilli(), outcome, id)
	return err
}

// PassesToday counts each node's earned tickets for the day.
//
// This is the whole of what a ticket carries: how many of the day's five a node
// earned. What they are WORTH is the voucher's business, decided from this
// count when it is signed, so nothing here needs redeploying to change a rate.
//
// Counted from the rows rather than kept as a running total. A stored counter
// would be a second copy of what the rows already say, and the two drift on the
// first correction — a shot re-judged, a row cleaned up.
func (s *Store) PassesToday(dayStart time.Time) (map[string]int, error) {
	rows, err := s.db.Query(
		`SELECT node_id, COUNT(*) FROM shot
		  WHERE fired_at>=? AND verdict=? GROUP BY node_id`,
		dayStart.UnixMilli(), VerdictPass)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// ShotsToday counts shots fired at a node since the start of the current day.
//
// This enforces the per-node daily cap, so it deliberately counts EVERY shot
// including refusals and timeouts. Counting only successes would let an
// unreachable node be hammered without limit — the cap is there to bound what
// the prober costs a node, not to measure whether the node is good.
func (s *Store) ShotsToday(nodeID string, dayStart time.Time) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM shot WHERE node_id=? AND fired_at>=?`,
		nodeID, dayStart.UnixMilli()).Scan(&n)
	return n, err
}

// ShotCountsToday returns shots-so-far per node, for the whole day at once.
// One query instead of one per candidate — the selection pass runs every minute
// over the full directory.
func (s *Store) ShotCountsToday(dayStart time.Time) (map[string]int, error) {
	rows, err := s.db.Query(`SELECT node_id, COUNT(*) FROM shot WHERE fired_at>=? GROUP BY node_id`,
		dayStart.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// PendingShots lists shots that were accepted but whose result is not in hand
// yet — the ones a later round comes back for.
//
// 🔴 NOT FILTERED BY DAY. A shot fired at 23:58 is collected after midnight, and
// cutting the query at the day boundary would strand it as an eternal open row
// that also blocks its node from ever being fired at again. The daily cap reads
// fired_at separately (ShotCountsToday), so yesterday's collection cannot leak
// into today's count.
//
// Ordered oldest first: those are the ones closest to the station's retention
// limit, so they are the ones worth asking about while the answer still exists.
func (s *Store) PendingShots() ([]Shot, error) {
	rows, err := s.db.Query(
		`SELECT id,fired_at,node_id,node_addr,job_id,service,model,
		        COALESCE(question_id,0), COALESCE(order_json,''), COALESCE(answer_raw,'')
		   FROM shot
		  WHERE outcome=? AND job_id<>'' ORDER BY fired_at`, OutcomeSubmitted)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Shot
	for rows.Next() {
		var sh Shot
		var firedMs int64
		if err := rows.Scan(&sh.ID, &firedMs, &sh.NodeID, &sh.NodeAddr, &sh.JobID, &sh.Service,
			&sh.Model, &sh.QuestionID, &sh.OrderJSON, &sh.AnswerRaw); err != nil {
			return nil, err
		}
		sh.FiredAt = time.UnixMilli(firedMs)
		out = append(out, sh)
	}
	return out, rows.Err()
}

// DailyBestMs returns each node's FASTEST answered shot of the day, per service.
//
// 🔴 The fastest, not the average, and that is the whole point. Capability is
// "this node CAN produce in N seconds", which one clean shot proves; a queue
// behind the other four inflates their times without saying anything about the
// hardware. Since a measurement can be inflated by waiting but never deflated,
// the minimum is the tightest honest bound on what the node can do — and a node
// with no GPU cannot fake its way under it however many shots it gets.
//
// Only `answered` rows count. A skipped row is our failure and a refused one
// never ran, so neither is evidence either way.
func (s *Store) DailyBestMs(dayStart time.Time) (map[string]int64, error) {
	rows, err := s.db.Query(
		`SELECT node_id, service, MIN(latency_ms) FROM shot
		  WHERE fired_at>=? AND outcome=? AND latency_ms>0
		  GROUP BY node_id, service`, dayStart.UnixMilli(), OutcomeAnswered)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var node, service string
		var ms int64
		if err := rows.Scan(&node, &service, &ms); err != nil {
			return nil, err
		}
		out[shotKey(node, service)] = ms
	}
	return out, rows.Err()
}

// shotKey is the (node, service) map key.
//
// Lowercased because node ids arrive in mixed case — the RV echoes the
// checksummed EOA on some rows and the lowercase form on others, in the same
// response. A case-sensitive key silently misses half of them, and the symptom
// is nothing at all: the lookup just never finds a match.
func shotKey(nodeID, service string) string {
	return strings.ToLower(strings.TrimSpace(nodeID)) + "|" + strings.ToLower(strings.TrimSpace(service))
}

// ---------------------------------------------------------------------------
// observations
// ---------------------------------------------------------------------------

// Observation is one directory sighting of a node.
type Observation struct {
	NodeID   string
	AuthMode string
	Online   bool
	MaxQueue int
	Version  string
}

// RecordObservations appends a directory snapshot.
//
// Every node seen is recorded, INCLUDING ones never fired at. The rule this
// feeds — "was it public all day" — cannot be answered from shot rows alone,
// because a node that was protected all morning has no shots to show for it.
func (s *Store) RecordObservations(obs []Observation, at time.Time) error {
	if len(obs) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT INTO observation(seen_at,node_id,auth_mode,online,max_queue,version) VALUES(?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, o := range obs {
		online := 0
		if o.Online {
			online = 1
		}
		if _, err := stmt.Exec(at.UnixMilli(), o.NodeID, o.AuthMode, online, o.MaxQueue, o.Version); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// PruneObservations drops sightings older than cutoff.
func (s *Store) PruneObservations(cutoff time.Time) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM observation WHERE seen_at < ?`, cutoff.UnixMilli())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// NodeSighting is one row of observation history, for continuity accounting.
type NodeSighting struct {
	NodeID string
	SeenAt time.Time
}

// SightingsToday returns every observation since dayStart, ordered by node then
// time so a caller can walk each node's run in one pass.
//
// The whole day is loaded rather than queried per node: with a few thousand
// nodes this is one scan an hour instead of a few thousand point lookups, and
// the continuity walk needs the sequence anyway.
func (s *Store) SightingsToday(dayStart time.Time) ([]NodeSighting, error) {
	rows, err := s.db.Query(
		`SELECT node_id, seen_at FROM observation WHERE seen_at >= ? ORDER BY node_id, seen_at`,
		dayStart.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []NodeSighting
	for rows.Next() {
		var id string
		var ms int64
		if err := rows.Scan(&id, &ms); err != nil {
			return nil, err
		}
		out = append(out, NodeSighting{NodeID: id, SeenAt: time.UnixMilli(ms)})
	}
	return out, rows.Err()
}

// sqlitePragmas turns on WAL and a busy timeout.
//
// 🔴 THE SPELLING IS DRIVER-SPECIFIC AND THE WRONG ONE FAILS SILENTLY. This was
// written as "?_journal=WAL&_busy_timeout=5000", which is mattn/go-sqlite3's
// syntax. We use modernc.org/sqlite, which reads "?_pragma=name(value)" and
// IGNORES anything else — no error, no warning. The result was journal_mode
// stuck on "delete" and busy_timeout on 0, so every reader blocked every writer
// and any contention returned SQLITE_BUSY instantly instead of waiting.
//
// Verify with: SELECT * FROM pragma_busy_timeout, pragma_journal_mode.
const sqlitePragmas = "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
