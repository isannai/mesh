package main

import (
	"flag"
	"strings"
	"testing"
)

// A flag the help never mentions has not shipped: every option has to be in the text,
// with the dash count its kind takes (two for a value, one for a switch).
func TestHelpMentionsEveryFlag(t *testing.T) {
	fs := flag.NewFlagSet("probe", flag.ContinueOnError)
	defineFlags(fs)
	n := 0
	fs.VisitAll(func(f *flag.Flag) {
		n++
		dashed := "-" + f.Name
		if b, ok := f.Value.(interface{ IsBoolFlag() bool }); !ok || !b.IsBoolFlag() {
			dashed = "--" + f.Name
		}
		if !strings.Contains(helpText, dashed) {
			t.Errorf("the help never mentions %s", dashed)
		}
	})
	if n == 0 {
		t.Fatal("no flags were defined")
	}
}

// The columns of the table are explained where the table is described.
func TestHelpNamesTheTableColumns(t *testing.T) {
	for _, col := range []string{"ID", "TIME(UTC)", "EPOCH", "NODE", "ROUND", "LATENCY", "TOKENS", "GOT", "RESULT"} {
		if !strings.Contains(helpText, "  "+col+" ") {
			t.Errorf("the help does not explain the %s column", col)
		}
	}
}
