package probe

// refstore.go: the reference check's two tables (refcheck.go).
//
//	refcheck  one row per node, slot and sampling round: how the node did. A
//	          checked node (role tested) has the result of its question; a
//	          reference node (role ref) has whether its answers agreed with the
//	          other references in that round.
//	refcache  every question a reference answered, with the reference value built
//	          from the answers. When a group's references are all offline the
//	          prober asks these again (workspace docs/todo/probe-기준노드.md 5절).
//	          Kept 15 days; one row per setting and prompt.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

const refSchema = `
CREATE TABLE IF NOT EXISTS refcheck (
  epoch      INTEGER NOT NULL,
  node_id    TEXT    NOT NULL,
  round      INTEGER NOT NULL,
  group_idx  INTEGER NOT NULL,
  role       TEXT    NOT NULL,              -- tested | ref
  mode       TEXT    NOT NULL,              -- ref | cache: how the question was got
  attempts   INTEGER NOT NULL DEFAULT 0,    -- questions asked (a text mismatch asks a second)
  asked_at   INTEGER NOT NULL,
  outcome    TEXT    NOT NULL,              -- submitted while in flight, then answered | timeout | refused | queue_full
  verdict    TEXT    NOT NULL DEFAULT '',   -- pass | fail; '' while in flight
  reason     TEXT    NOT NULL DEFAULT '',   -- why it failed (refcheck.go reason*)
  detail     TEXT,                          -- the answer and the reference, for a later look
  cached     INTEGER NOT NULL DEFAULT 0,    -- 1 when the question came from refcache
  PRIMARY KEY (epoch, node_id, round)
);
CREATE INDEX IF NOT EXISTS refcheck_at ON refcheck(asked_at);

CREATE TABLE IF NOT EXISTS refcache (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  budget_id   INTEGER NOT NULL,
  meta_hash   TEXT    NOT NULL,
  gpu         TEXT    NOT NULL,
  profile     TEXT    NOT NULL,
  engine      TEXT    NOT NULL,
  model_hash  TEXT    NOT NULL,
  prompt      TEXT    NOT NULL,
  answers     TEXT    NOT NULL,             -- each reference node's answer
  reference   TEXT    NOT NULL,             -- the reference value built from them
  created_at  INTEGER NOT NULL,
  used_at     INTEGER NOT NULL DEFAULT 0,
  used_count  INTEGER NOT NULL DEFAULT 0,
  UNIQUE (budget_id, gpu, profile, engine, model_hash, prompt)
);
CREATE INDEX IF NOT EXISTS refcache_pick ON refcache(gpu, profile, engine, model_hash, used_at);
`

// RefRow is one refcheck row.
type RefRow struct {
	Epoch    int64
	NodeID   string
	Round    int
	Group    int
	Role     string
	Mode     string
	Attempts int
	AskedAt  time.Time
	Outcome  string
	Verdict  string
	Reason   string
	Detail   string
	Cached   bool
}

// The roles of a refcheck row.
const (
	RoleTested = "tested"
	RoleRef    = "ref"
)

// BeginRefCheck writes a tested node's row as in flight. false when the node
// already has a row for that round: it is asked once a round, whatever came back.
func (s *Store) BeginRefCheck(r RefRow) (bool, error) {
	res, err := s.db.Exec(`
		INSERT OR IGNORE INTO refcheck (epoch, node_id, round, group_idx, role, mode, asked_at, outcome)
		VALUES (?,?,?,?,?,?,?,?)`,
		r.Epoch, r.NodeID, r.Round, r.Group, RoleTested, r.Mode, r.AskedAt.UnixMilli(), OutcomeSubmitted)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// FinishRefCheck writes a tested node's result.
func (s *Store) FinishRefCheck(r RefRow) error {
	_, err := s.db.Exec(`
		UPDATE refcheck SET mode = ?, attempts = ?, outcome = ?, verdict = ?, reason = ?, detail = ?, cached = ?
		 WHERE epoch = ? AND node_id = ? AND round = ?`,
		r.Mode, r.Attempts, r.Outcome, r.Verdict, r.Reason, r.Detail, boolInt(r.Cached),
		r.Epoch, r.NodeID, r.Round)
	return err
}

// RecordRefEval adds one question's verdict on a reference node to its row for
// the round. A failure sticks: one answer that disagreed fails the round.
func (s *Store) RecordRefEval(r RefRow) error {
	_, err := s.db.Exec(`
		INSERT INTO refcheck (epoch, node_id, round, group_idx, role, mode, attempts, asked_at, outcome, verdict, reason, detail)
		VALUES (?,?,?,?,?,?,1,?,?,?,?,?)
		ON CONFLICT (epoch, node_id, round) DO UPDATE SET
		  attempts = refcheck.attempts + 1,
		  outcome  = CASE WHEN refcheck.verdict = 'fail' THEN refcheck.outcome ELSE excluded.outcome END,
		  reason   = CASE WHEN refcheck.verdict = 'fail' THEN refcheck.reason ELSE excluded.reason END,
		  detail   = CASE WHEN refcheck.verdict = 'fail' THEN refcheck.detail ELSE excluded.detail END,
		  verdict  = CASE WHEN refcheck.verdict = 'fail' THEN 'fail' ELSE excluded.verdict END`,
		r.Epoch, r.NodeID, r.Round, r.Group, RoleRef, ModeRef, r.AskedAt.UnixMilli(),
		r.Outcome, r.Verdict, r.Reason, r.Detail)
	return err
}

// RefRows returns the slot's rows, by node then round.
func (s *Store) RefRows(epoch int64) (map[string][2]*RefRow, error) {
	rows, err := s.db.Query(`
		SELECT node_id, round, group_idx, role, mode, attempts, asked_at, outcome, verdict, reason, COALESCE(detail,''), cached
		  FROM refcheck WHERE epoch = ?`, epoch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][2]*RefRow{}
	for rows.Next() {
		r := RefRow{Epoch: epoch}
		var at int64
		var cached int
		if err := rows.Scan(&r.NodeID, &r.Round, &r.Group, &r.Role, &r.Mode, &r.Attempts, &at,
			&r.Outcome, &r.Verdict, &r.Reason, &r.Detail, &cached); err != nil {
			return nil, err
		}
		if r.Round < 1 || r.Round > 2 {
			continue
		}
		r.AskedAt, r.Cached = time.UnixMilli(at), cached == 1
		pair := out[r.NodeID]
		pair[r.Round-1] = &r
		out[r.NodeID] = pair
	}
	return out, rows.Err()
}

// PruneRefChecks drops rows older than cutoff.
func (s *Store) PruneRefChecks(cutoff time.Time) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM refcheck WHERE asked_at < ?`, cutoff.UnixMilli())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// RefSetting is what a stored question was asked under. A question is reused
// only under the same GPU, profile, engine and model.
type RefSetting struct {
	BudgetID uint64
	MetaHash string
	GPU      string
	Profile  string
	Engine   string
	Model    string
}

// RefCached is one stored question.
type RefCached struct {
	ID        int64
	Prompt    string
	Reference Reference
}

// SaveRefCache stores a question the references answered. The same setting and
// prompt again replaces the answers and starts its 15 days over.
func (s *Store) SaveRefCache(set RefSetting, prompt string, answers map[string]ProbAnswer, ref Reference, at time.Time) error {
	a, err := json.Marshal(answers)
	if err != nil {
		return err
	}
	r, err := json.Marshal(ref)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
		INSERT INTO refcache (budget_id, meta_hash, gpu, profile, engine, model_hash, prompt, answers, reference, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT (budget_id, gpu, profile, engine, model_hash, prompt) DO UPDATE SET
		  meta_hash = excluded.meta_hash, answers = excluded.answers, reference = excluded.reference,
		  created_at = excluded.created_at`,
		set.BudgetID, set.MetaHash, set.GPU, set.Profile, set.Engine, set.Model, prompt, string(a), string(r), at.UnixMilli())
	return err
}

// TakeRefCache hands out the stored question unused for longest under this
// setting, skipping the ids in skip (given to another node of the group this
// round). The question stays stored: it is marked used, not removed.
func (s *Store) TakeRefCache(set RefSetting, skip map[int64]bool, at time.Time) (RefCached, bool, error) {
	rows, err := s.db.Query(`
		SELECT id, prompt, reference FROM refcache
		 WHERE gpu = ? AND profile = ? AND engine = ? AND model_hash = ?
		 ORDER BY used_at ASC, id ASC`, set.GPU, set.Profile, set.Engine, set.Model)
	if err != nil {
		return RefCached{}, false, err
	}
	var pick *RefCached
	for rows.Next() {
		var c RefCached
		var ref string
		if err := rows.Scan(&c.ID, &c.Prompt, &ref); err != nil {
			rows.Close()
			return RefCached{}, false, err
		}
		if skip[c.ID] {
			continue
		}
		if json.Unmarshal([]byte(ref), &c.Reference) != nil {
			continue
		}
		pick = &c
		break
	}
	rows.Close()
	if pick == nil {
		return RefCached{}, false, nil
	}
	if _, err := s.db.Exec(`UPDATE refcache SET used_at = ?, used_count = used_count + 1 WHERE id = ?`,
		at.UnixMilli(), pick.ID); err != nil {
		return RefCached{}, false, err
	}
	return *pick, true, nil
}

// PruneRefCache drops stored questions older than cutoff (15 days).
func (s *Store) PruneRefCache(cutoff time.Time) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM refcache WHERE created_at < ?`, cutoff.UnixMilli())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// RefCacheCount is how many questions are stored under a setting (for a log line
// and tests).
func (s *Store) RefCacheCount(set RefSetting) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM refcache WHERE gpu = ? AND profile = ? AND engine = ? AND model_hash = ?`,
		set.GPU, set.Profile, set.Engine, set.Model).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return n, err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
