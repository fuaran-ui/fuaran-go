// Package core_test holds the import-boundary test for the Core twins.
//
// internal/core is the one place this host keeps its twins of the Fuaran.Core
// reference: the canonical-JSON primitives (canonical), the Compute-layer model,
// Transform evaluator and codec decode (dataframe), and the function registry
// with the capability model and its InvocationKey (function). The public
// canonical, dataframe and function packages forward to them.
//
// The boundary is held by this test, not by convention: every package under
// internal/core, and every package its production code or tests import, is
// either inside internal/core or in the Go standard library. Anything else — a
// domain package of this module (wire, renderer, ops, …) or a third-party module
// — fails the test with the offending import edge named.
package core_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"sort"
	"strings"
	"testing"
)

const (
	modulePath = "github.com/fuaran-ui/fuaran-go"
	corePath   = modulePath + "/internal/core"
)

// listedPackage is the subset of `go list -json` output the test reads.
type listedPackage struct {
	ImportPath string
	Standard   bool
	Imports    []string
	ForTest    string
}

// basePath maps the package variants `go list -test` adds back to the package
// they test: "p [p.test]" (a test variant), "p.test" (the synthesized test
// main) and "p_test" (an external test package) are all p.
func basePath(p string) string {
	if i := strings.Index(p, " ["); i >= 0 {
		p = p[:i]
	}
	p = strings.TrimSuffix(p, ".test")
	return strings.TrimSuffix(p, "_test")
}

func insideCore(p string) bool {
	p = basePath(p)
	return p == corePath || strings.HasPrefix(p, corePath+"/")
}

// listCoreGraph runs `go list -deps -test -json` over internal/core/... from
// this directory and returns every package in the resulting import graph.
func listCoreGraph(t *testing.T) []listedPackage {
	t.Helper()
	goTool, err := exec.LookPath("go")
	if err != nil {
		// Not a skip: a boundary test that cannot look is not evidence the
		// boundary holds.
		t.Fatalf("the go tool is needed to read the import graph: %v", err)
	}
	cmd := exec.Command(goTool, "list", "-deps", "-test", "-json", "./...")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps -test -json ./... failed: %v\n%s", err, stderr.String())
	}
	var pkgs []listedPackage
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var p listedPackage
		if err := dec.Decode(&p); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("decoding go list output: %v", err)
		}
		pkgs = append(pkgs, p)
	}
	return pkgs
}

// TestCoreImportsNothingOutsideTheBoundary proves, over the real import graph,
// that internal/core imports no non-core package of this module (and nothing
// outside the standard library).
func TestCoreImportsNothingOutsideTheBoundary(t *testing.T) {
	pkgs := listCoreGraph(t)

	standard := map[string]bool{}
	for _, p := range pkgs {
		if p.Standard {
			standard[p.ImportPath] = true
		}
	}

	// The probe must have seen the twins, or an empty listing would pass.
	seen := map[string]bool{}
	for _, p := range pkgs {
		if insideCore(p.ImportPath) {
			seen[basePath(p.ImportPath)] = true
		}
	}
	for _, want := range []string{"canonical", "dataframe", "function"} {
		if !seen[corePath+"/"+want] {
			t.Fatalf("the import graph does not list %s/%s; the probe is not looking at the boundary (saw %v)", corePath, want, keys(seen))
		}
	}

	var violations []string
	for _, p := range pkgs {
		if !insideCore(p.ImportPath) {
			continue
		}
		for _, imp := range p.Imports {
			if insideCore(imp) || standard[basePath(imp)] {
				continue
			}
			kind := "a package outside the standard library"
			if strings.HasPrefix(basePath(imp), modulePath+"/") {
				kind = "a domain package of this module"
			}
			violations = append(violations, basePath(p.ImportPath)+" imports "+basePath(imp)+" ("+kind+")")
		}
	}
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("the internal/core boundary is breached — Core twins may import only internal/core and the standard library:\n  %s",
			strings.Join(dedupe(violations), "\n  "))
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func dedupe(sorted []string) []string {
	out := sorted[:0]
	for i, s := range sorted {
		if i == 0 || s != sorted[i-1] {
			out = append(out, s)
		}
	}
	return out
}
