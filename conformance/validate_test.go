package conformance

// The corpus validate family (WIRE_FORMAT §27.8.2, Phase 2381): which pre-emit
// defects a document carries. Each vector is a canonical document and the
// defects the reference validator reports on it. This host decodes the tree,
// validates it, and asserts that its findings, restricted to the codes its
// validator-coverage.json declares implemented, are exactly the vector's
// defects restricted to the same codes.
//
// A finding of this host carries a code, a severity and a $-rooted path, and
// no node id, so each one is compared by CODE, SEVERITY, NODE and PATH: its
// path must address the document in the §29.3 grammar, and the deepest node
// it passes through must be the stated node at the stated path, so a
// finding below the node (its stateKey, its id) is located at that node and
// never at another one under it. What the leg does NOT compare is stated in
// README.md ("Pre-emit findings and decode paths — Phase 2381").

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
		Code     string  `json:"code"`
		Severity string  `json:"severity"`
		Node     *string `json:"node"`
		Path     string  `json:"path"`
	} `json:"expected"`
}

// pathSegment is one §29.3 segment: `[digits]` (an index), or a name — `.`
// and then everything up to the next `.` or `[`, however the key is spelled
// (`.aria-hidden`, `.On time`, `.404`). There is no quoted form: a bracketed
// run that is not all digits reads as a name, bracket included, and so names
// no member of the document.
var pathSegment = regexp.MustCompile(`^(?:\[([0-9]+)\]|\.([^.\[]*)|(\[[^.\[]*))`)

// nodePositions is every node of the document, path to id: an object at a
// position the vocabulary (idl.json) declares to hold a node — the root; a
// node's envelope `fallback` and its `state.onEmpty` / `state.onLoading`; and,
// in its kind, `children[i]`, `cases[i].child`, `child`, `fallback`, `body`,
// `default` and a slot argument's `args.<name>.tree` / `inputs.<name>.tree`.
// An object elsewhere that happens to carry a string `id` and an object `kind`
// (a form field, a filter chip) is not a node.
func nodePositions(tree any) map[string]string {
	out := map[string]string{}
	var node func(path string, v any)
	node = func(path string, v any) {
		m, ok := v.(map[string]any)
		if !ok {
			return
		}
		if id, ok := m["id"].(string); ok {
			out[path] = id
		}
		if fb, ok := m["fallback"].(map[string]any); ok {
			node(path+".fallback", fb)
		}
		if kind, ok := m["kind"].(map[string]any); ok {
			at := path + ".kind"
			if children, ok := kind["children"].([]any); ok {
				for i, c := range children {
					node(fmt.Sprintf("%s.children[%d]", at, i), c)
				}
			}
			if cases, ok := kind["cases"].([]any); ok {
				for i, c := range cases {
					if cm, ok := c.(map[string]any); ok {
						node(fmt.Sprintf("%s.cases[%d].child", at, i), cm["child"])
					}
				}
			}
			for _, member := range []string{"child", "fallback", "body", "default"} {
				node(at+"."+member, kind[member])
			}
			for _, member := range []string{"args", "inputs"} {
				if args, ok := kind[member].(map[string]any); ok {
					for name, a := range args {
						if am, ok := a.(map[string]any); ok {
							node(at+"."+member+"."+name+".tree", am["tree"])
						}
					}
				}
			}
		}
		if st, ok := m["state"].(map[string]any); ok {
			for _, alt := range []string{"onEmpty", "onLoading"} {
				node(path+".state."+alt, st[alt])
			}
		}
	}
	node("$", tree)
	return out
}

// locatedNode is the path of the deepest node path passes through, and
// that node's id; an error when path is not a §29.3 path, or when a segment
// other than the last names nothing in the document (a name on an array, an
// index on an object, a member the object does not hold). The last segment may
// name an absent member: a missing field is reported where it belongs.
func locatedNode(tree any, path string) (string, string, error) {
	if len(path) == 0 || path[0] != '$' {
		return "", "", fmt.Errorf("%q is not $-rooted", path)
	}
	nodes := nodePositions(tree)
	at := tree
	nodePath, nodeID := "$", nodes["$"]
	consumed, rest := "$", path[1:]
	for rest != "" {
		m := pathSegment.FindStringSubmatchIndex(rest)
		if m == nil {
			return "", "", fmt.Errorf("%q is not a section 29.3 path (stuck at %q)", path, rest)
		}
		step := rest[:m[1]]
		switch {
		case m[2] >= 0:
			i, _ := strconv.Atoi(rest[m[2]:m[3]])
			if arr, ok := at.([]any); ok && i < len(arr) {
				at = arr[i]
			} else {
				at = nil
			}
		case m[4] >= 0:
			if obj, ok := at.(map[string]any); ok {
				at = obj[rest[m[4]:m[5]]]
			} else {
				at = nil
			}
		default:
			at = nil
		}
		consumed, rest = consumed+step, rest[m[1]:]
		if at == nil {
			if rest != "" {
				return "", "", fmt.Errorf("%q names nothing in the document at %q", path, consumed)
			}
			break
		}
		if id, ok := nodes[consumed]; ok {
			nodePath, nodeID = consumed, id
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
					want = append(want, d.Code+" "+d.Severity+" "+id+" "+d.Path)
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
				got = append(got, f.Code+" "+string(f.Severity)+" "+id+" "+nodePath)
			}
			sort.Strings(want)
			sort.Strings(got)
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Errorf("findings %v, want %v", got, want)
			}
		})
	}
}

// The leg's own falsifiers. The spelling this host wrote before Phase 2381
// (`children.0`) names a member an array does not have, and the quoted segment
// §29.3 withdrew names a member no object has; the bracketed index names the
// node it sits under, and a member is written as its key stands, digits and
// punctuation included (§6, §29.3).
func TestValidateLegReadsTheSection29Grammar(t *testing.T) {
	var tree any
	doc := `{"id":"r","accessibility":{"aria-hidden":true},"kind":{"$type":"Box","children":[` +
		`{"id":"a","kind":{"$type":"Box"}},` +
		`{"id":"f","kind":{"$type":"FragmentRef","args":{"my-slot":{"tree":{"id":"s","kind":{"$type":"Box"}}},` +
		`"404":{"tree":{"id":"n","kind":{"$type":"Box"}}}}}},` +
		`{"id":"m","kind":{"$type":"TonedPill","map":{"On time":"Good"}}},` +
		`{"id":"form","kind":{"$type":"Form","fields":[{"id":"f","kind":{"$type":"Checkbox"},"label":"x"}]}}]}}`
	if err := json.Unmarshal([]byte(doc), &tree); err != nil {
		t.Fatal(err)
	}
	for _, refused := range []string{
		"$.kind.children.0.kind.stateKey",
		"$.kind.children[1].kind.args['my-slot'].tree.kind",
		"$.kind[0].id",
		"kind",
	} {
		if p, id, err := locatedNode(tree, refused); err == nil {
			t.Errorf("%q was read as a path of the document (node %q at %q)", refused, id, p)
		}
	}
	for path, want := range map[string]string{
		"$.kind.children[0].kind.stateKey":                 "a $.kind.children[0]",
		"$.accessibility.aria-hidden":                      "r $",
		"$.kind.children[1].kind.args.my-slot.tree.kind":   "s $.kind.children[1].kind.args.my-slot.tree",
		"$.kind.children[1].kind.args.404.tree.kind.$type": "n $.kind.children[1].kind.args.404.tree",
		"$.kind.children[2].kind.map.On time":              "m $.kind.children[2]",
		"$.kind.children[2].kind.absentField":              "m $.kind.children[2]",
		// A form field carries an id and a kind and is not a node: the vocabulary
		// declares no node position in `fields[]`.
		"$.kind.children[3].kind.fields[0].kind.$type": "form $.kind.children[3]",
	} {
		p, id, err := locatedNode(tree, path)
		if err != nil || id+" "+p != want {
			t.Errorf("%q: got %q %q %v, want %s", path, id, p, err, want)
		}
	}
}
