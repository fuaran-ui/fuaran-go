// Command gen regenerates this host's structural layer from the corpus's
// idl.json: `go generate ./wire/idl` (or `go run ./wire/idl/internal/gen` from
// anywhere in the module).
//
// The corpus is found through internal/corpusroot, so FUARAN_WIRE_FIXTURES
// names an in-flight corpus here exactly as it does for the conformance suite;
// -corpus names one explicitly. -check writes nothing and exits 1 when any
// generated file differs from what idl.json now projects.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fuaran-ui/fuaran-go/internal/corpusroot"
	"github.com/fuaran-ui/fuaran-go/wire/idl/internal/idlgen"
)

func main() {
	corpus := flag.String("corpus", "", "the corpus root (default: FUARAN_WIRE_FIXTURES, then the walk up)")
	check := flag.Bool("check", false, "report drift and exit 1 instead of writing")
	flag.Parse()
	if err := run(*corpus, *check); err != nil {
		fmt.Fprintln(os.Stderr, "idl gen:", err)
		os.Exit(1)
	}
}

func run(explicit string, check bool) error {
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	root, err := corpusroot.Resolve(explicit, os.Getenv, wd)
	if err != nil {
		return err
	}
	if root == "" {
		return fmt.Errorf("no corpus found: set %s to the corpus root or pass -corpus", corpusroot.EnvVar)
	}
	src, err := os.ReadFile(filepath.Join(root, "idl.json"))
	if err != nil {
		return err
	}
	module, err := moduleRoot(wd)
	if err != nil {
		return err
	}
	outs, err := idlgen.Generate(src)
	if err != nil {
		return err
	}
	var stale []string
	for _, o := range outs {
		path := filepath.Join(module, filepath.FromSlash(o.Path))
		current, _ := os.ReadFile(path)
		if bytes.Equal(current, o.Content) {
			continue
		}
		if check {
			stale = append(stale, o.Path)
			continue
		}
		if err := os.WriteFile(path, o.Content, 0o644); err != nil {
			return err
		}
		fmt.Println("wrote", o.Path)
	}
	if len(stale) > 0 {
		return fmt.Errorf("generated files are stale against %s: %v — run go generate ./wire/idl", filepath.Join(root, "idl.json"), stale)
	}
	fmt.Printf("idl.json sha256 %s\n", idlgen.Hash(src))
	return nil
}

func moduleRoot(dir string) (string, error) {
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("not inside the module (no go.mod above the working directory)")
		}
		dir = parent
	}
}
