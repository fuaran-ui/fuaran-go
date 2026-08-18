package validator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"
)

// The declaration in validator-coverage.json against what the validator raises.
//
// Phase 669's cross-host gate compares each host's declaration to the canonical
// vocabulary, but it cannot compare a declaration to an IMPLEMENTATION — it reads
// JSON, not code. So a host could declare a rule it does not implement, or
// implement one it never declared, and that gate would pass.
//
// This closes it for this host, which is what `machineChecked: true` asserts. It is
// only possible because the FUARAN code is now a first-class field on Finding
// rather than prose inside the message: when the code and the message are the same
// string there is no pair to check.
//
// The implemented set is recovered by SOURCE SCAN rather than by running the
// validator, because running it would only find the rules some fixture happens to
// trigger — and the rules no fixture reaches are exactly the ones a coverage check
// must not miss.

var raisedCode = regexp.MustCompile(`Code:\s*"(FUARAN[0-9A-Z-]+)"`)

func raisedCodes(t *testing.T) map[string]bool {
	t.Helper()
	src, err := os.ReadFile("validate.go")
	if err != nil {
		t.Fatalf("read validate.go: %v", err)
	}
	out := map[string]bool{}
	for _, m := range raisedCode.FindAllStringSubmatch(string(src), -1) {
		out[m[1]] = true
	}
	return out
}

type coverageDecl struct {
	Implemented    []string            `json:"implemented"`
	Abstained      map[string]string   `json:"abstained"`
	OtherFamilies  map[string][]string `json:"otherFamilies"`
	MachineChecked bool                `json:"machineChecked"`
}

func declaration(t *testing.T) coverageDecl {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "validator-coverage.json"))
	if err != nil {
		t.Fatalf("read validator-coverage.json: %v", err)
	}
	var d coverageDecl
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("parse validator-coverage.json: %v", err)
	}
	return d
}

// If this drops to false, the two checks below are no longer load-bearing.
func TestDeclarationClaimsMachineChecked(t *testing.T) {
	if !declaration(t).MachineChecked {
		t.Error("validator-coverage.json no longer claims machineChecked; the checks below assert nothing")
	}
}

// Implemented-but-undeclared: the drift that makes the coverage matrix understate.
func TestEveryRaisedCodeIsDeclared(t *testing.T) {
	d := declaration(t)
	declared := map[string]bool{}
	for _, c := range d.Implemented {
		declared[c] = true
	}
	for _, codes := range d.OtherFamilies {
		for _, c := range codes {
			declared[c] = true
		}
	}
	var undeclared []string
	for c := range raisedCodes(t) {
		if !declared[c] {
			undeclared = append(undeclared, c)
		}
	}
	sort.Strings(undeclared)
	if len(undeclared) > 0 {
		t.Errorf("the validator raises %v but the declaration does not list them — "+
			"add them to `implemented`, or stop raising them", undeclared)
	}
}

// Declared-but-unimplemented: the drift that makes the matrix OVERSTATE, and the
// more dangerous direction — it claims coverage this host does not have.
func TestEveryDeclaredCodeIsRaised(t *testing.T) {
	raised := raisedCodes(t)
	var unimplemented []string
	for _, c := range declaration(t).Implemented {
		if !raised[c] {
			unimplemented = append(unimplemented, c)
		}
	}
	sort.Strings(unimplemented)
	if len(unimplemented) > 0 {
		t.Errorf("the declaration lists %v but no Finding raises them — "+
			"implement them, or move them to `abstained` with a reason", unimplemented)
	}
}

// A code this host invented would otherwise look like coverage.
func TestRaisedCodesAreInTheCanonicalVocabulary(t *testing.T) {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	var vocabPath string
	for {
		candidate := filepath.Join(dir, "wire-format-fixtures", "validator", "defect-vocabulary.json")
		if _, err := os.Stat(candidate); err == nil {
			vocabPath = candidate
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Skip("wire-format-fixtures/validator not found")
		}
		dir = parent
	}
	raw, err := os.ReadFile(vocabPath)
	if err != nil {
		t.Fatalf("read vocabulary: %v", err)
	}
	var vocab struct {
		Codes []struct {
			Code string `json:"code"`
		} `json:"codes"`
	}
	if err := json.Unmarshal(raw, &vocab); err != nil {
		t.Fatalf("parse vocabulary: %v", err)
	}
	known := map[string]bool{}
	for _, e := range vocab.Codes {
		known[e.Code] = true
	}
	// Other-family codes are declared as belonging to a family the vocabulary does
	// not enumerate, so they are exempt by declaration rather than by omission.
	exempt := map[string]bool{}
	for _, codes := range declaration(t).OtherFamilies {
		for _, c := range codes {
			exempt[c] = true
		}
	}
	for c := range raisedCodes(t) {
		if !known[c] && !exempt[c] {
			t.Errorf("%s is not in the canonical vocabulary and is not declared as another family — "+
				"this host invented it", c)
		}
	}
}
