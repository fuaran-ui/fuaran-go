// Command conformance-residue is the blocking conformance gate for this host.
//
// It runs the WHOLE test suite with no exclusions and compares the set of tests
// that actually failed against the named set in conformance/RESIDUE.txt. It
// exits non-zero when the two differ IN EITHER DIRECTION:
//
//   - a failure that is not on the list is a regression;
//   - a listed entry that now passes is a stale cap.
//
// The second direction is the reason this exists. It replaces a `go test -skip`
// quarantine, and a skip pattern is structurally incapable of telling you it has
// stopped matching anything: the patterns this replaced named three fixtures the
// decoder had adopted weeks earlier, and excluded eight whole tests to suppress
// them — hiding nine real failures the list never mentioned. An exclusion that
// cannot go stale-red reads as a measured cap while being a blindfold.
//
// Usage:
//
//	go run ./cmd/conformance-residue            # the gate
//	go run ./cmd/conformance-residue -selftest  # the go-red proof (see below)
//
// The suite is invoked with -count=1 so a cached PASS from an earlier corpus can
// never stand in for a run against the current one — the corpus is a sibling
// checkout outside the module, so Go's test cache does not see it change.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// residuePath is resolved relative to the module root, which is the working
// directory `go run ./cmd/...` uses.
const residuePath = "conformance/RESIDUE.txt"

// loadResidue reads the named set, ignoring blank lines and '#' comments.
func loadResidue(path string) (map[string]bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out := map[string]bool{}
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out[line] = true
	}
	return out, s.Err()
}

// testEvent is the subset of `go test -json` output this gate reads.
type testEvent struct {
	Action string `json:"Action"`
	Test   string `json:"Test"`
}

// runSuite runs the whole suite and returns the set of failing test names.
//
// It reports an error only when the harness itself could not run (a build
// failure, a missing toolchain). A non-zero exit from `go test` because tests
// FAILED is the ordinary case here and is not an error — the failures are the
// answer.
func runSuite(extraArgs ...string) (map[string]bool, error) {
	args := append([]string{"test", "-count=1", "-json", "./..."}, extraArgs...)
	cmd := exec.Command("go", args...)
	out, err := cmd.Output()
	if err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			return nil, fmt.Errorf("could not run `go %s`: %w", strings.Join(args, " "), err)
		}
	}

	failed := map[string]bool{}
	dec := json.NewDecoder(strings.NewReader(string(out)))
	for dec.More() {
		var ev testEvent
		if err := dec.Decode(&ev); err != nil {
			// A non-JSON line is build output. `go test -json` interleaves it as
			// an Output event, so a decode error here means the stream is not
			// what this gate expects — say so rather than silently reporting no
			// failures, which would be a green gate over an unread run.
			return nil, fmt.Errorf("could not read `go test -json` output: %w", err)
		}
		if ev.Action == "fail" && ev.Test != "" {
			failed[ev.Test] = true
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("`go test -json` produced no output at all — the suite did not run")
	}
	return failed, nil
}

// leaves keeps only the most specific failing names: a parent whose child also
// failed carries no information the child does not, and listing both would make
// the residue file a tree rather than a set.
func leaves(failed map[string]bool) map[string]bool {
	out := map[string]bool{}
	for name := range failed {
		hasFailingChild := false
		for other := range failed {
			if other != name && strings.HasPrefix(other, name+"/") {
				hasFailingChild = true
				break
			}
		}
		if !hasFailingChild {
			out[name] = true
		}
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func main() {
	selfTest := flag.Bool("selftest", false,
		"prove this gate goes red: run the comparison against a deliberately wrong residue set")
	flag.Parse()

	residue, err := loadResidue(residuePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "conformance-residue: cannot read %s: %v\n", residuePath, err)
		fmt.Fprintf(os.Stderr, "  The residue set is REQUIRED. A missing list is not an empty one:\n")
		fmt.Fprintf(os.Stderr, "  it means this gate does not know what it is capping, and a gate\n")
		fmt.Fprintf(os.Stderr, "  that cannot say what it excludes must not report success.\n")
		os.Exit(1)
	}
	if len(residue) == 0 {
		// Legitimate once the decoder catches up — but say so, so an empty file
		// is never mistaken for an unread one.
		fmt.Println("conformance-residue: the residue set is EMPTY — every test is expected to pass.")
	}

	fmt.Printf("conformance-residue: running the whole suite (no exclusions), %d entry(ies) on the residue list.\n", len(residue))
	failed, err := runSuite()
	if err != nil {
		fmt.Fprintf(os.Stderr, "conformance-residue: %v\n", err)
		os.Exit(1)
	}
	failing := leaves(failed)

	if *selfTest {
		// THE GO-RED PROOF. Two perturbations of the comparison, run against the
		// same real result set, each of which MUST be caught:
		//
		//   1. a residue set missing one currently-failing entry — the shape a
		//      new regression takes;
		//   2. a residue set carrying an entry that passes — the shape a stale
		//      cap takes, and the one a `-skip` pattern could never report.
		//
		// Without this, a bug that made `failing` come back empty would leave the
		// gate green forever, and nothing would ever notice.
		ok := true

		short := map[string]bool{}
		for k := range residue {
			short[k] = true
		}
		if len(failing) == 0 {
			fmt.Println("  self-test 1 NOT RUN — nothing is failing, so there is no entry to withhold.")
		} else {
			withheld := sortedKeys(failing)[0]
			delete(short, withheld)
			if reg, _ := compare(failing, short); len(reg) == 0 {
				fmt.Printf("  SELF-TEST FAILED — withholding %q from the residue set did not read as a regression.\n", withheld)
				ok = false
			} else {
				fmt.Printf("  self-test 1 OK — withholding %q reads as a regression.\n", withheld)
			}
		}

		wide := map[string]bool{"ZZTestThatCannotExist/probe": true}
		for k := range residue {
			wide[k] = true
		}
		if _, stale := compare(failing, wide); len(stale) == 0 {
			fmt.Println("  SELF-TEST FAILED — an entry that cannot possibly fail did not read as stale.")
			ok = false
		} else {
			fmt.Println("  self-test 2 OK — a residue entry that does not fail reads as stale.")
		}

		if !ok {
			os.Exit(1)
		}
		fmt.Println("conformance-residue: self-test passed — the gate goes red in both directions.")
		return
	}

	regressions, stale := compare(failing, residue)

	for _, name := range sortedKeys(failing) {
		if residue[name] {
			fmt.Printf("  residue (expected failure): %s\n", name)
		}
	}

	if len(regressions) == 0 && len(stale) == 0 {
		fmt.Printf("conformance-residue: OK — %d failure(s), every one of them named in %s.\n", len(failing), residuePath)
		return
	}

	if len(regressions) > 0 {
		fmt.Fprintf(os.Stderr, "\nREGRESSION — %d test(s) failed that %s does not name:\n", len(regressions), residuePath)
		for _, name := range regressions {
			fmt.Fprintf(os.Stderr, "  %s\n", name)
		}
		fmt.Fprintf(os.Stderr, "Fix it, or revert. Adding it to the residue list to go green retires the\ncoverage, which is exactly what the list this replaced was doing.\n")
	}
	if len(stale) > 0 {
		fmt.Fprintf(os.Stderr, "\nSTALE RESIDUE — %d entry(ies) in %s now PASS:\n", len(stale), residuePath)
		for _, name := range stale {
			fmt.Fprintf(os.Stderr, "  %s\n", name)
		}
		fmt.Fprintf(os.Stderr, "Delete those lines. A cap that no longer caps anything reads as measured\nand is a blindfold — the defect this gate exists to make impossible.\n")
	}
	fmt.Fprintf(os.Stderr, "\n(residue file: %s)\n", filepath.FromSlash(residuePath))
	os.Exit(1)
}

// compare returns the failures the residue does not name, and the residue
// entries that did not fail.
func compare(failing, residue map[string]bool) (regressions, stale []string) {
	for name := range failing {
		if !residue[name] {
			regressions = append(regressions, name)
		}
	}
	for name := range residue {
		if !failing[name] {
			stale = append(stale, name)
		}
	}
	sort.Strings(regressions)
	sort.Strings(stale)
	return
}
