package probe

// report.go — reading back what the prober did, without a sqlite client.
//
// The database is the record; the log only narrates. But a Windows node has no
// sqlite3 on PATH, and "install a sqlite client" is a poor answer to "did my
// probe fire". This renders the same rows the log summarises.

import (
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/isannai/mesh/pkg/faucet"
)

// queueKinds is every kind the queue can hold, in the order the report lists them.
func queueKinds() []Category {
	out := []Category{CatMath}
	for _, s := range codeSpecs {
		out = append(out, s.cat)
	}
	for _, g := range genSpecs {
		out = append(out, g.cat)
	}
	return out
}

// ReportOptions says what Report prints.
type ReportOptions struct {
	Limit  int    // shots to list; 20 when unset
	ShotID int64  // when set, only that shot
	Node   string // when set, only the shots of the nodes whose id contains this
	Epoch  int64  // when set, only the shots fired in this slot
	Detail bool   // under each shot: the question, the expected answer and the one received
}

// Report renders recent activity as a plain-text block. The shots are a table, one
// line each: id, time, epoch (the slot), node, round, latency, tokens, the answer
// received, and the result. ShotID narrows it to one shot, Node to one node's shots,
// Epoch to one slot's shots (they combine), Detail adds the question. A search
// prints the shots and none of the other sections, which would only push the
// answer off the screen.
//
// Three sections because three different questions get asked: is the queue
// stocked (generation), did anything go out (firing), and is the node still
// being seen (continuity). A blank section is printed as such rather than
// omitted — "no shots" is the answer to a question, not an absence of one.
func (s *Store) Report(o ReportOptions) (string, error) {
	var b strings.Builder
	if o.search() {
		if err := s.writeShots(&b, o); err != nil {
			return "", err
		}
		return b.String(), nil
	}

	depth, err := s.QueueDepth()
	if err != nil {
		return "", err
	}
	fmt.Fprintf(&b, "QUEUE (unconsumed)\n")
	if len(depth) == 0 {
		fmt.Fprintf(&b, "  empty — nothing has been generated yet\n")
	}
	for _, c := range queueKinds() {
		if n := depth[c]; n > 0 {
			fmt.Fprintf(&b, "  %-10s %d\n", c, n)
		}
	}

	if err := s.writeShots(&b, o); err != nil {
		return "", err
	}

	// What a node CAN do, which is not the same question as what it did on any
	// one shot. Queueing inflates a time and nothing deflates it, so the day's
	// fastest is the tightest honest reading of the hardware — and the number to
	// look at when deciding where a pass threshold belongs.
	fmt.Fprintf(&b, "\nBEST TODAY (fastest answered shot per service)\n")
	if best, err := s.DailyBestMs(dayStart(time.Now())); err == nil {
		if len(best) == 0 {
			fmt.Fprintf(&b, "  none — nothing has been answered today\n")
		}
		for key, ms := range best {
			node, service, _ := strings.Cut(key, "|")
			fmt.Fprintf(&b, "  %-14s %-10s %s\n", short(node), service,
				(time.Duration(ms) * time.Millisecond).Round(time.Millisecond))
		}
	}

	fmt.Fprintf(&b, "\nOBSERVATIONS\n")
	var (
		n               int
		firstMs, lastMs int64
		distinct        int
	)
	if err := s.db.QueryRow(`SELECT COUNT(*), COALESCE(MIN(seen_at),0), COALESCE(MAX(seen_at),0),
	                                COUNT(DISTINCT node_id) FROM observation`).
		Scan(&n, &firstMs, &lastMs, &distinct); err != nil {
		return "", err
	}
	if n == 0 {
		fmt.Fprintf(&b, "  none — the directory has not been read yet\n")
	} else {
		fmt.Fprintf(&b, "  %d rows, %d nodes, %s → %s\n", n, distinct,
			time.UnixMilli(firstMs).UTC().Format("15:04:05"), time.UnixMilli(lastMs).UTC().Format("15:04:05 UTC"))
		// Time present is what the schedule measures against, so it belongs next
		// to the raw counts. It is a TOTAL across gaps, not a run length: a node
		// that was up all morning, off for lunch and back since is credited with
		// both halves.
		if sightings, err := s.SightingsToday(dayStart(time.Now())); err == nil {
			for id, present := range presenceFrom(sightings) {
				fmt.Fprintf(&b, "  %s  present %s today\n", short(id),
					present.Round(time.Minute))
			}
		}
	}
	return b.String(), nil
}

// shotRow is one shot as the report shows it.
type shotRow struct {
	id                         int64
	firedMs, latency           int64
	epoch                      int64
	node, outcome, verdict     string
	round, tokens, deferred    int
	category, question, expect string
	got                        string
}

// writeShots prints the shot table, newest first, or the single shot o.ShotID.
//
// The table is laid out as a whole before any detail line is added under a row: a
// line with no columns ends a tabwriter block, so details written between the rows
// would re-measure the columns and the table would no longer line up.
func (s *Store) writeShots(b *strings.Builder, o ReportOptions) error {
	limit := o.Limit
	if limit < 1 {
		limit = 20
	}
	const cols = `SELECT s.id, s.fired_at, COALESCE(s.assign_epoch,0), s.node_id, COALESCE(s.round,0),
		       s.outcome, COALESCE(s.verdict,''), COALESCE(s.latency_ms,0), COALESCE(s.completion_tokens,0),
		       COALESCE(q.category,''), COALESCE(q.q,''), COALESCE(q.draft_answer,''),
		       COALESCE(s.answer_raw,''), COALESCE(s.deferred,0)
		  FROM shot s LEFT JOIN question q ON q.id = s.question_id `
	var (
		where []string
		args  []any
	)
	if o.ShotID > 0 {
		where = append(where, "s.id = ?")
		args = append(args, o.ShotID)
	}
	needle := nodeNeedle(o.Node)
	if needle != "" {
		where = append(where, `s.node_id LIKE ? ESCAPE '\'`)
		args = append(args, "%"+likeEscape(needle)+"%")
	}
	if o.Epoch > 0 {
		// A row from before the epoch was stamped has none; its slot is its time
		// divided into slots, the same as slotOf shows.
		where = append(where, `COALESCE(NULLIF(s.assign_epoch,0), s.fired_at/1000/?) = ?`)
		args = append(args, int64(faucet.SlotSeconds), o.Epoch)
	}
	query := cols
	if len(where) > 0 {
		query += "WHERE " + strings.Join(where, " AND ") + " "
	}
	if o.ShotID == 0 {
		title := fmt.Sprintf("SHOTS (most recent %d", limit)
		if needle != "" {
			title += " of nodes matching " + needle
		}
		if o.Epoch > 0 {
			title += fmt.Sprintf(" in epoch %d", o.Epoch)
		}
		fmt.Fprintf(b, "\n%s)\n", title)
		query += "ORDER BY s.id DESC LIMIT ?"
		args = append(args, limit)
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	var shots []shotRow
	for rows.Next() {
		var r shotRow
		if err := rows.Scan(&r.id, &r.firedMs, &r.epoch, &r.node, &r.round, &r.outcome, &r.verdict,
			&r.latency, &r.tokens, &r.category, &r.question, &r.expect, &r.got, &r.deferred); err != nil {
			return err
		}
		shots = append(shots, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(shots) == 0 {
		if o.ShotID > 0 {
			return fmt.Errorf("no shot with id %d", o.ShotID)
		}
		if needle != "" || o.Epoch > 0 {
			fmt.Fprintf(b, "  none - no shot matches the search\n")
			return nil
		}
		fmt.Fprintf(b, "  none - nothing has been fired yet\n")
		return nil
	}

	var table strings.Builder
	tw := tabwriter.NewWriter(&table, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tTIME(UTC)\tEPOCH\tNODE\tROUND\tLATENCY\tTOKENS\tGOT\tRESULT")
	for _, r := range shots {
		// UTC, not the machine's zone: the slots (EPOCH) are counted in UTC, and an
		// operator reading this next to another node's report, or the RV's, should
		// see the same clock.
		fmt.Fprintf(tw, "%d\t%s\t%d\t%s\t%s\t%s\t%s\t%s\t%s\n",
			r.id, time.UnixMilli(r.firedMs).UTC().Format("01-02 15:04:05"), r.slotOf(), short(r.node),
			dashIf(r.round > 0, fmt.Sprint(r.round)), dashIf(r.latency > 0, fmt.Sprintf("%dms", r.latency)),
			dashIf(r.tokens > 0, fmt.Sprint(r.tokens)), r.gotCell(), r.result())
	}
	tw.Flush()

	lines := strings.Split(strings.TrimRight(table.String(), "\n"), "\n")
	b.WriteString("  " + lines[0] + "\n")
	for i, r := range shots {
		b.WriteString("  " + lines[i+1] + "\n")
		if o.Detail {
			r.writeDetail(b)
		}
	}
	return nil
}

// slotOf is the slot (epoch) the shot was fired in. The prober stamps it when it
// fires; a row from before that column existed has none, and the slot is then the
// time divided into slots, which is how the RV numbers them.
func (r shotRow) slotOf() int64 {
	if r.epoch > 0 {
		return r.epoch
	}
	return r.firedMs / 1000 / int64(faucet.SlotSeconds)
}

// result is the verdict when there is one (pass, fail, truncated), else how the
// shot ended (timeout, refused, queue_full, skipped, submitted).
func (r shotRow) result() string {
	if r.verdict != "" {
		return r.verdict
	}
	return r.outcome
}

// gotCell is the answer received, cut short for the table. An image shot has no
// answer, only the order it was given; the table says so instead of printing it.
func (r shotRow) gotCell() string {
	if r.question == "" && r.got != "" {
		return "(image)"
	}
	t := strings.TrimSpace(strings.ReplaceAll(r.got, "\n", " "))
	if len(t) > 24 {
		t = t[:24] + "…"
	}
	return dashIf(t != "", t)
}

// writeDetail prints what the table cannot hold: the question, the expected answer
// and the one received side by side, which is the whole diagnosis when a verdict
// looks wrong.
func (r shotRow) writeDetail(b *strings.Builder) {
	if r.question == "" {
		if r.got != "" {
			fmt.Fprintf(b, "      image:    %s\n", trim(r.got))
		}
		return
	}
	fmt.Fprintf(b, "      Q:        %s  [%s]\n", r.question, r.category)
	fmt.Fprintf(b, "      expect:   %q\n", r.expect)
	fmt.Fprintf(b, "      got:      %q\n", trim(r.got))
	if r.deferred > 0 {
		// Next to the latency in the table it explains it: a slow time with a count
		// is a busy node, a slow time without one is a node that was idle and
		// still took that long.
		fmt.Fprintf(b, "      deferred: %d round(s) held off before firing\n", r.deferred)
	}
}

func dashIf(ok bool, v string) string {
	if ok {
		return v
	}
	return "-"
}

// search reports whether the report is a search (a shot, a node or a slot) rather
// than the overview.
func (o ReportOptions) search() bool {
	return o.ShotID > 0 || o.Epoch > 0 || nodeNeedle(o.Node) != ""
}

// nodeNeedle is what to look for in node ids. The table cuts an id to twelve
// characters and marks the cut with an ellipsis, so an id copied from it ends in
// one; that mark is not part of the id.
func nodeNeedle(s string) string {
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "…"))
}

// likeEscape makes s match literally in a LIKE pattern written with ESCAPE '\'.
func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
