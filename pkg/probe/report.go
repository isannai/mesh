package probe

// report.go — reading back what the prober did, without a sqlite client.
//
// The database is the record; the log only narrates. But a Windows node has no
// sqlite3 on PATH, and "install a sqlite client" is a poor answer to "did my
// probe fire". This renders the same rows the log summarises.

import (
	"fmt"
	"strings"
	"time"
)

// Report renders recent activity as a plain-text block.
//
// Three sections because three different questions get asked: is the queue
// stocked (generation), did anything go out (firing), and is the node still
// being seen (continuity). A blank section is printed as such rather than
// omitted — "no shots" is the answer to a question, not an absence of one.
func (s *Store) Report(limit int) (string, error) {
	if limit < 1 {
		limit = 20
	}
	var b strings.Builder

	depth, err := s.QueueDepth()
	if err != nil {
		return "", err
	}
	fmt.Fprintf(&b, "QUEUE (unconsumed)\n")
	if len(depth) == 0 {
		fmt.Fprintf(&b, "  empty — nothing has been generated yet\n")
	}
	for _, c := range []Category{CatMath, CatSequence, CatUnits, CatGeography, CatElement, CatCurrency} {
		if n := depth[c]; n > 0 {
			fmt.Fprintf(&b, "  %-10s %d\n", c, n)
		}
	}

	fmt.Fprintf(&b, "\nSHOTS (most recent %d)\n", limit)
	rows, err := s.db.Query(`
		SELECT s.fired_at, s.node_id, s.outcome, COALESCE(s.verdict,''),
		       COALESCE(s.latency_ms,0), COALESCE(s.completion_tokens,0),
		       COALESCE(q.category,''), COALESCE(q.q,''), COALESCE(q.draft_answer,''),
		       COALESCE(s.answer_raw,'')
		  FROM shot s LEFT JOIN question q ON q.id = s.question_id
		 ORDER BY s.id DESC LIMIT ?`, limit)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	any := false
	for rows.Next() {
		var (
			firedMs, latency          int64
			node, outcome, verdict    string
			tokens                    int
			cat, q, expected, gotText string
		)
		if err := rows.Scan(&firedMs, &node, &outcome, &verdict, &latency, &tokens,
			&cat, &q, &expected, &gotText); err != nil {
			return "", err
		}
		any = true
		fmt.Fprintf(&b, "  %s  %s  %s", time.UnixMilli(firedMs).Format("15:04:05"), short(node), outcome)
		if verdict != "" {
			fmt.Fprintf(&b, "/%s", verdict)
		}
		if latency > 0 {
			fmt.Fprintf(&b, "  %dms", latency)
		}
		if tokens > 0 {
			fmt.Fprintf(&b, "  %dtok", tokens)
		}
		b.WriteString("\n")
		if q == "" && gotText != "" {
			// An image shot. It has no question row — the order is drawn from
			// the slot table at fire time, not consumed from the queue — so
			// without this branch the stored order stayed invisible and a
			// timeout printed one word and nothing else. That is precisely the
			// case the record exists for.
			fmt.Fprintf(&b, "      %-9s %s\n", "image", trim(gotText))
		}
		if q != "" {
			fmt.Fprintf(&b, "      %-9s Q: %s\n", cat, q)
			// Expected and got on adjacent lines: when a verdict looks wrong,
			// the two strings side by side is the whole diagnosis.
			fmt.Fprintf(&b, "                expect %q\n", expected)
			fmt.Fprintf(&b, "                got    %q\n", trim(gotText))
		}
	}
	if !any {
		fmt.Fprintf(&b, "  none — nothing has been fired yet\n")
	}
	if err := rows.Err(); err != nil {
		return "", err
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
			time.UnixMilli(firstMs).Format("15:04:05"), time.UnixMilli(lastMs).Format("15:04:05"))
		// The anchor is what the schedule actually measures against, so it is
		// worth showing next to the raw counts — a node observed for an hour
		// but re-anchored ten minutes ago is due for nothing.
		if sightings, err := s.SightingsToday(dayStart(time.Now())); err == nil {
			for id, anchor := range anchorsFrom(sightings) {
				fmt.Fprintf(&b, "  %s  up %s (anchor %s)\n", short(id),
					time.Since(anchor).Round(time.Second), anchor.Format("15:04:05"))
			}
		}
	}
	return b.String(), nil
}
