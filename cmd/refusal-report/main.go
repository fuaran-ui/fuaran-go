// Command refusal-report emits this host's refusal class for every reject
// fixture in the shared wire-format corpus — this host's half of the cross-host
// identical-rejection check.
//
// The corpus declares, per reject fixture, the code every conformant host must
// answer with. Each host's own suite asserts its answer against that declaration,
// which makes cross-host agreement true TRANSITIVELY — provided every host's leg
// actually ran. That proviso is the gap: those legs skip when the corpus is
// absent, and a conformance leg that silently asserts nothing while the build
// stays green is a failure mode this repository has recorded before.
//
// A cross-host runner collects one report per host and asserts the answers agree
// with each other AND with the corpus declaration — one artefact, one place, and a
// hard failure when a host is missing rather than a quiet omission.
//
// Per-host error TEXT stays free; the refusal CLASS must agree. The message rides
// along so a reader can see what this host said, but the runner compares only the
// code and the path prefix: pinning a message across five languages would be
// pinning translation, not conformance.
//
// Usage:
//
//	go run ./cmd/refusal-report [-corpus <dir>] [-out <file>]
//
// Writes JSON to stdout by default. Exits non-zero only when the corpus cannot be
// read: judging the answers is the runner's job, not this emitter's.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fuaran-ui/fuaran-go/wire"
)

const host = "fuaran-go"

type manifestFixture struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Decoder   string `json:"decoder"`
	InputFile string `json:"inputFile"`
}

type corpusManifest struct {
	Fixtures []manifestFixture `json:"fixtures"`
}

type caseReport struct {
	ID      string `json:"id"`
	Decoder string `json:"decoder"`
	Skipped string `json:"skipped,omitempty"`
	Refused bool   `json:"refused"`
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

type report struct {
	Host   string       `json:"host"`
	Corpus string       `json:"corpus"`
	Cases  []caseReport `json:"cases"`
}

// findCorpus walks up from the working directory looking for the shared corpus.
func findCorpus() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		manifest := filepath.Join(dir, "wire-format-fixtures", "manifest.json")
		if _, err := os.Stat(manifest); err == nil {
			return filepath.Dir(manifest)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// decodeOne returns this host's answer for one payload under one decoder. A
// panic is recovered and reported as the answer it is: a host that panicked where
// the contract says it returns has failed the totality claim, and saying so in the
// report is more useful than dying mid-collection.
func decodeOne(decoder, text string) (refused bool, code, path, message string) {
	defer func() {
		if rec := recover(); rec != nil {
			refused, code, path, message = true, fmt.Sprintf("ESCAPED-%T", rec), "$", fmt.Sprint(rec)
		}
	}()

	var err error
	if decoder == "op" {
		_, err = wire.DecodeOp(text)
	} else {
		_, err = wire.DecodeNode(text)
	}
	if err == nil {
		return false, "", "", ""
	}
	if de, ok := err.(*wire.DecodeError); ok {
		return true, string(de.Code), de.Path, de.Message
	}
	return true, "NON-STRUCTURED-ERROR", "$", err.Error()
}

func main() {
	corpusFlag := flag.String("corpus", "", "the shared wire-format corpus root")
	outFlag := flag.String("out", "", "write the report here instead of stdout")
	flag.Parse()

	corpus := *corpusFlag
	if corpus == "" {
		corpus = findCorpus()
	}
	if corpus == "" {
		fmt.Fprintf(os.Stderr, "%s: the wire-format corpus was not found. Pass -corpus, or check the repo out beside the corpus.\n", host)
		os.Exit(2)
	}

	raw, err := os.ReadFile(filepath.Join(corpus, "manifest.json"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: reading the corpus manifest: %v\n", host, err)
		os.Exit(2)
	}
	var m corpusManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		fmt.Fprintf(os.Stderr, "%s: parsing the corpus manifest: %v\n", host, err)
		os.Exit(2)
	}

	out := report{Host: host, Corpus: corpus, Cases: []caseReport{}}
	for _, fx := range m.Fixtures {
		if fx.Kind != "reject" {
			continue
		}
		decoder := fx.Decoder
		if decoder == "" {
			decoder = "node"
		}
		if decoder != "node" && decoder != "op" {
			// Envelope / elicitation rejects run through their own decoders and are
			// NOT in scope here. Reported as skipped rather than silently dropped: a
			// runner that could not tell "not applicable" from "not present" would
			// read a shrinking corpus as agreement.
			out.Cases = append(out.Cases, caseReport{ID: fx.ID, Decoder: decoder, Skipped: "decoder not in scope"})
			continue
		}
		payload, err := os.ReadFile(filepath.Join(corpus, fx.InputFile))
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: reading %s: %v\n", host, fx.InputFile, err)
			os.Exit(2)
		}
		refused, code, path, message := decodeOne(decoder, string(payload))
		out.Cases = append(out.Cases, caseReport{
			ID: fx.ID, Decoder: decoder, Refused: refused, Code: code, Path: path, Message: message,
		})
	}

	encoded, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: marshalling the report: %v\n", host, err)
		os.Exit(2)
	}
	encoded = append(encoded, '\n')

	if *outFlag != "" {
		if err := os.MkdirAll(filepath.Dir(*outFlag), 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "%s: creating the output directory: %v\n", host, err)
			os.Exit(2)
		}
		if err := os.WriteFile(*outFlag, encoded, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "%s: writing the report: %v\n", host, err)
			os.Exit(2)
		}
		return
	}
	os.Stdout.Write(encoded)
}
