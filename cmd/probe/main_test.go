package main

import (
	"flag"
	"strings"
	"testing"
)

func parse(t *testing.T, args ...string) error {
	t.Helper()
	fs := flag.NewFlagSet("probe", flag.ContinueOnError)
	o := defineFlags(fs)
	if err := fs.Parse(args); err != nil {
		t.Fatalf("parse %v: %v", args, err)
	}
	return checkOptions(fs, o)
}

// A read option whose value is "not given" must not fall through to running the
// prober: each of these used to start it.
func TestReadOptionsRefuseValuesThatMeanNothing(t *testing.T) {
	for _, args := range [][]string{
		{"--report", "0"}, {"--report", "-3"},
		{"--id", "0"}, {"--epoch", "0"}, {"--epoch", "-1"},
		{"--slots", "0"}, {"--slots", "-2"},
		{"--node="}, {"--node", "  "},
		{"-a"},
	} {
		if err := parse(t, args...); err == nil {
			t.Errorf("probe %s was accepted, it would start the prober", strings.Join(args, " "))
		}
	}
}

func TestReadOptionsAcceptRealValues(t *testing.T) {
	for _, args := range [][]string{
		{}, {"-once"}, {"--config", "x.json"},
		{"--report", "5"}, {"--report", "5", "-a"}, {"--id", "220", "-a"},
		{"--node", "0xbada8be8"}, {"--epoch", "165954", "--node", "0xbada"},
		{"--slots", "3"}, {"-version"},
	} {
		if err := parse(t, args...); err != nil {
			t.Errorf("probe %s was refused: %v", strings.Join(args, " "), err)
		}
	}
}
