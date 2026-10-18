package station

import (
	"path/filepath"
	"testing"
)

// /provider/file serves images below the node's home_dir and nothing else. The check
// has to compare path elements: a sibling folder that merely starts with the same
// letters is outside.
func TestPathInsideDir(t *testing.T) {
	root := filepath.Join(t.TempDir(), "isann")
	cases := []struct {
		name string
		path string
		want bool
	}{
		{"the dir itself", root, true},
		{"a file below", filepath.Join(root, "out", "a.png"), true},
		{"a dot-dot that stays inside", filepath.Join(root, "out", "..", "a.png"), true},
		{"a sibling with the same prefix", root + "-other" + string(filepath.Separator) + "a.png", false},
		{"a sibling named like the prefix plus 2", root + "2", false},
		{"the parent", filepath.Dir(root), false},
		{"a dot-dot out of the dir", filepath.Join(root, "..", "x.png"), false},
	}
	for _, c := range cases {
		if got := pathInsideDir(root, c.path); got != c.want {
			t.Errorf("%s: pathInsideDir(%q, %q) = %v, want %v", c.name, root, c.path, got, c.want)
		}
	}
}
