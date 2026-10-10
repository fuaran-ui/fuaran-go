// Package corpusroot is the one place this module finds the shared
// wire-format corpus (Phase 2204).
//
// Every suite and command here resolves it in one order:
//
//  1. an explicit argument, when the caller has one;
//  2. FUARAN_WIRE_FIXTURES, the variable every host in the estate reads;
//  3. a wire-format-fixtures directory holding manifest.json, found by walking
//     up from the working directory (the sibling clone beside this repo).
//
// A NAMED root (1 or 2) must hold a manifest.json or it is REFUSED with an
// error naming the variable and the path — never ignored. Walking past it would
// reach, from a git worktree, the shared primary clone: the very corpus the
// override exists to leave alone, so a run would certify against an oracle
// nobody named and report it as green. An empty or whitespace-only value counts
// as unset, as it does in the other hosts.
//
// Nothing named and nothing found is "" with no error: a standalone checkout,
// where each caller keeps its own skip.
//
// TestNoLegSpellsTheCorpusPath (guard_test.go) fails the build when any other
// file in the module spells the corpus path for itself.
package corpusroot

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EnvVar names the corpus root — the estate's own spelling.
const EnvVar = "FUARAN_WIRE_FIXTURES"

// DirName is the corpus clone's directory name.
const DirName = "wire-format-fixtures"

func holdsCorpus(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "manifest.json"))
	return err == nil
}

// Resolve applies the order above: explicit, then getenv(EnvVar), then the walk
// up from start. It returns "" and no error when nothing is named and the walk
// finds nothing, and an error when a named root holds no manifest.json.
func Resolve(explicit string, getenv func(string) string, start string) (string, error) {
	if named := strings.TrimSpace(explicit); named != "" {
		if !holdsCorpus(named) {
			return "", fmt.Errorf("the corpus root given explicitly (%q) does not name a conformance corpus "+
				"(no manifest.json under it); it is refused rather than ignored — point it at the corpus root itself", named)
		}
		return filepath.Abs(named)
	}
	if declared := strings.TrimSpace(getenv(EnvVar)); declared != "" {
		if !holdsCorpus(declared) {
			return "", fmt.Errorf("%s=%q does not name a conformance corpus (no manifest.json under it). "+
				"Point it at the corpus root, or unset it. It is refused rather than ignored: falling back would "+
				"reach ../%s, which from a worktree is the shared primary clone the override exists to leave alone",
				EnvVar, declared, DirName)
		}
		return filepath.Abs(declared)
	}
	dir := start
	for {
		candidate := filepath.Join(dir, DirName)
		if holdsCorpus(candidate) {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", nil
		}
		dir = parent
	}
}

// Find resolves from the process environment and the working directory, with
// no explicit argument.
func Find() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("reading the working directory: %w", err)
	}
	return Resolve("", os.Getenv, wd)
}

// TB is the slice of testing.TB that ForTest needs, so this package does not
// import testing into non-test builds.
type TB interface {
	Helper()
	Fatalf(format string, args ...any)
}

// ForTest is Find for a test: a refused override fails the test (never a skip),
// and "" means a standalone checkout, which the caller skips as before.
func ForTest(t TB) string {
	t.Helper()
	root, err := Find()
	if err != nil {
		t.Fatalf("%v", err)
	}
	return root
}

// MustFind is Find for a helper with no test handle: a refused override panics
// with the refusal — failing the calling test loudly, never skipping it — and
// "" means a standalone checkout.
func MustFind() string {
	root, err := Find()
	if err != nil {
		panic(err)
	}
	return root
}
