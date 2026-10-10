package corpusroot

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// spelling matches a string literal that IS a corpus path: "wire-format-fixtures",
// "../wire-format-fixtures", "wire-format-fixtures/nodes" — but not prose such as
// a skip message that merely mentions the directory.
var spelling = regexp.MustCompile(`"(\.\./)*wire-format-fixtures(/[^"\s]*)?"`)

// TestNoLegSpellsTheCorpusPath fails when any Go file in the module outside this
// package spells the corpus path for itself (Phase 2204). Every corpus-reading
// leg resolves the corpus here, so one variable (FUARAN_WIRE_FIXTURES) points
// all of them at an in-flight corpus; a leg with its own walk reads the primary
// clone from a worktree and certifies against the wrong oracle, silently.
func TestNoLegSpellsTheCorpusPath(t *testing.T) {
	root := moduleRoot(t)
	self, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	scanned := 0
	var offenders []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == ".git" || name == "node_modules" || name == "testdata" {
				return filepath.SkipDir
			}
			if abs, _ := filepath.Abs(path); abs == self {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		scanned++
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") || !spelling.MatchString(line) {
				continue
			}
			rel, _ := filepath.Rel(root, path)
			offenders = append(offenders, filepath.ToSlash(rel)+":"+strconv.Itoa(i+1)+": "+strings.TrimSpace(line))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if scanned < 50 {
		t.Fatalf("scanned only %d Go files under %s — the guard is not looking at the module", scanned, root)
	}
	if len(offenders) > 0 {
		t.Errorf("resolve the corpus with internal/corpusroot instead of spelling its path:\n%s", strings.Join(offenders, "\n"))
	}
}

// TestTheGuardIsNotVacuous proves the pattern catches the shapes it exists for.
func TestTheGuardIsNotVacuous(t *testing.T) {
	for _, line := range []string{
		`candidate := filepath.Join(dir, "wire-format-fixtures", "nodes")`,
		`root := "../wire-format-fixtures/manifest.json"`,
	} {
		if !spelling.MatchString(line) {
			t.Errorf("the guard misses %s", line)
		}
	}
	if spelling.MatchString(`t.Skip("wire-format-fixtures corpus not found")`) {
		t.Error("the guard flags prose")
	}
}
