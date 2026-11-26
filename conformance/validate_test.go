package conformance

// The corpus validate family (WIRE_FORMAT §27.8.2, Phase 2381): which pre-emit
// defects a document carries. Each vector is a canonical document and the
// defects the reference validator reports on it. This host decodes the tree,
// validates it, and asserts that its findings, restricted to the codes its
// validator-coverage.json declares implemented, are exactly the vector's
// defects restricted to the same codes.
//
// A finding of this host carries a $-rooted path and no node id, so each one
// is compared by CODE, NODE and PATH: its path must be a §29.3 path, and the
// deepest node object it passes through must be the stated node at the stated
// path, so a finding below the node (its stateKey, its id) is located at that
// node and never at another one under it.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"testing"

	"github.com/fuaran-ui/fuaran-go/internal/corpusroot"
	"github.com/fuaran-ui/fuaran-go/validator"
	"github.com/fuaran-ui/fuaran-go/wire"
)

type validateVector struct {
	ID       string `json:"id"`
	Tree     string `json:"tree"`
	Expected []struct {
		Code string  `json:"code"`
		Node *string `json:"node"`
		Path string  `json:"path"`
	} `json:"expected"`
}

// pathSegment is one §29.3 segment: `.name`, `['…']`, or `[i]`.
var pathSegment = regexp.MustCompile(`^(?:\.([A-Za-z_$][A-Za-z0-9_$]*)|\['((?:[^'\\]|\\.)*)'\]|\[([0-9]+)\])`)

func isNodeObject(v any) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	_, idOK := m["id"].(string)
	_, kindOK := m["kind"].(map[string]any)
	return idOK && kindOK
}

// locatedNode is the path of the deepest node object path passes through, and
// that node's id; an error when path is not a §29.3 path.
func locatedNode(tree any, path string) (string, string, error) {
	if len(path) == 0 || path[0] != '$' {
		return "", "", fmt.Errorf("%q is not $-rooted", path)
	}
	at := tree
	nodePath, nodeID := "$", ""
	if isNodeObject(tree) {
		nodeID = tree.(map[string]any)["id"].(string)
	}
	consumed, rest := "$", path[1:]
	for rest != "" {
		m := pathSegment.FindStringSubmatchIndex(rest)
		if m == nil {
			return "", "", fmt.Errorf("%q is not a section 29.3 path (stuck at %q)", path, rest)
		}
		step := rest[:m[1]]
		switch {
		case m[6] >= 0:
			i, _ := strconv.Atoi(rest[m[6]:m[7]])
			if arr, ok := at.([]any); ok && i < len(arr) {
				at = arr[i]
			} else {
				at = nil
			}
		default:
			key := ""
			if m[2] >= 0 {
				key = rest[m[2]:m[3]]
			} else {
				key = rest[m[4]:m[5]]
			}
			if obj, ok := at.(map[string]any); ok {
				at = obj[key]
			} else {
				at = nil
			}
		}
		consumed, rest = consumed+step, rest[m[1]:]
		if at == nil {
			break
		}
		if isNodeObject(at) {
			nodePath, nodeID = consumed, at.(map[string]any)["id"].(string)
		}
	}
	return nodePath, nodeID, nil
}

func implementedCodes(t *testing.T) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "validator-coverage.json"))
	if err != nil {
		t.Fatalf("read validator-coverage.json: %v", err)
	}
	var decl struct {
		Implemented []string `json:"implemented"`
	}
	if err := json.Unmarshal(raw, &decl); err != nil {
		t.Fatalf("parse validator-coverage.json: %v", err)
	}
	out := map[string]bool{}
	for _, c := range decl.Implemented {
		out[c] = true
	}
	return out
}

func TestValidateCorpus(t *testing.T) {
	corpus := corpusroot.ForTest(t)
	raw, err := os.ReadFile(filepath.Join(corpus, "validate", "manifest.json"))
	if err != nil {
		t.Fatalf("the validate family is part of the corpus this host certifies against: %v", err)
	}
	var manifest struct {
		Vectors []validateVector `json:"vectors"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("parse validate/manifest.json: %v", err)
	}
	if len(manifest.Vectors) < 6 {
		t.Fatalf("validate/manifest.json carries %d vectors; the family has at least 6", len(manifest.Vectors))
	}
	implemented := implementedCodes(t)

	for _, v := range manifest.Vectors {
		t.Run(v.ID, func(t *testing.T) {
			node, err := wire.DecodeNode(v.Tree)
			if err != nil {
				t.Fatalf("the tree does not decode: %v", err)
			}
			var tree any
			if err := json.Unmarshal([]byte(v.Tree), &tree); err != nil {
				t.Fatalf("parse tree: %v", err)
			}
			var want, got []string
			for _, d := range v.Expected {
				if implemented[d.Code] {
					id := "<none>"
					if d.Node != nil {
						id = *d.Node
					}
					want = append(want, d.Code+" "+id+" "+d.Path)
				}
			}
			for _, f := range validator.ValidateNode(node) {
				if !implemented[f.Code] {
					continue
				}
				nodePath, id, err := locatedNode(tree, f.Path)
				if err != nil {
					t.Fatalf("%s: %v", f.Code, err)
				}
				got = append(got, f.Code+" "+id+" "+nodePath)
			}
			sort.Strings(want)
			sort.Strings(got)
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Errorf("findings %v, want %v", got, want)
			}
		})
	}
}

// The leg's own falsifier: the spelling this host wrote before Phase 2381 is
// not a §29.3 path, and the bracketed one names the node it sits under.
func TestValidateLegRefusesADottedIndex(t *testing.T) {
	var tree any
	if err := json.Unmarshal([]byte(`{"id":"r","kind":{"$type":"Box","children":[{"id":"a","kind":{"$type":"Box"}}]}}`), &tree); err != nil {
		t.Fatal(err)
	}
	if _, _, err := locatedNode(tree, "$.kind.children.0.kind.stateKey"); err == nil {
		t.Fatal("a dotted index was read as a section 29.3 path")
	}
	p, id, err := locatedNode(tree, "$.kind.children[0].kind.stateKey")
	if err != nil || p != "$.kind.children[0]" || id != "a" {
		t.Fatalf("got %q %q %v", p, id, err)
	}
}
