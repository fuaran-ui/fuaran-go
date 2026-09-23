package renderer

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/fuaran-ui/fuaran-go/wire"
)

// `style.direction`'s five §3.1 rules, asserted on EVERY kind the roster names
// (Phase 1838).
//
// The trait's scope in `render-fidelity.json` is `allKinds`: the member rides the
// node envelope, so every kind this host renders owes all five claims. The
// checkers in render_obligations_test.go prove the rules on one leaf (`Badge`)
// and one container (`Box`), which is sound only while every kind reaches the
// wrapper through `renderNode` — the one place `dir` and the isolation class are
// emitted. That is true today by construction, and nothing held it: a kind given
// its own wrapper tomorrow would drop the direction and leave every checker
// green. This sweep is what holds it. It reads the roster for the owed kinds and
// the corpus for a real tree of each, so a kind added to either arrives here
// without anyone editing this file.
//
// Per root fixture, the root's own `style.direction` is set, re-rendered and
// compared:
//
//   - rule 1 + 2 — `rtl` puts `dir="rtl"` AND the `fuaran-dir-rtl` isolation
//     class on the root's own wrapper tag;
//   - rule 4 — `auto` renders byte-identically to the member omitted;
//   - rule 5 — the `rtl` render, less exactly those two tokens on the root tag,
//     is byte-identical to the omitted render (no side, alignment, locale or
//     descendant direction derived from it);
//   - rule 3 — the same tree declaring `ltr`, nested in an `rtl` `Box`, carries
//     its own `dir="ltr"` rather than inheriting the block's.
//
// A roster kind with no fixture this host can decode and render is a FAILURE,
// not a skip: "every kind" is the claim, and a kind the sweep never reached is
// one it cannot speak for.

// directionProbeBlockID is the container the rule-3 probe nests a tree in.
// Chosen to collide with no corpus id.
const directionProbeBlockID = "phase-1838-direction-probe-block"

// withRootDirection returns node with its OWN `style.direction` set to
// direction, or removed when direction is "". Extras and style are copied, never
// mutated, so the variants of one decoded tree are independent. A style left
// empty by the removal is dropped entirely, so "omitted" means the member is
// absent from the node exactly as it is on a tree that never declared a style.
func withRootDirection(node wire.Node, direction string) wire.Node {
	extras := make(map[string]wire.Value, len(node.Extras)+1)
	for k, v := range node.Extras {
		extras[k] = v
	}
	style, _ := extras["style"].(wire.Obj)
	fields := make(map[string]wire.Value, len(style.Fields)+1)
	for k, v := range style.Fields {
		fields[k] = v
	}
	if direction == "" {
		delete(fields, "direction")
	} else {
		fields["direction"] = wire.Str(direction)
	}
	if len(fields) == 0 {
		delete(extras, "style")
	} else {
		extras["style"] = wire.Obj{Tag: style.Tag, Fields: fields}
	}
	node.Extras = extras
	return node
}

// nodeOpeningTag is the opening tag of the element carrying data-fuaran-node-id=id,
// or "" when the output holds no such element.
func nodeOpeningTag(html, id string) string {
	at := strings.Index(html, `data-fuaran-node-id="`+escapeAttr(id)+`"`)
	if at < 0 {
		return ""
	}
	start := strings.LastIndex(html[:at], "<")
	end := strings.Index(html[at:], ">")
	if start < 0 || end < 0 {
		return ""
	}
	return html[start : at+end+1]
}

// hasClassToken reports whether an opening tag's class attribute carries token
// as a whole class. Position is deliberately not asserted: the isolation class
// follows the kind and style classes, and the reference host appends
// `fuaran-has-tooltip` after it on a node with a hint, as this host does.
func hasClassToken(tag, token string) bool {
	at := strings.Index(tag, ` class="`)
	if at < 0 {
		return false
	}
	value := tag[at+len(` class="`):]
	if end := strings.Index(value, `"`); end >= 0 {
		value = value[:end]
	}
	for _, class := range strings.Fields(value) {
		if class == token {
			return true
		}
	}
	return false
}

// ownedDirectionKinds is the set of kinds the roster says owe `style.direction`.
func ownedDirectionKinds(t *testing.T, m renderFidelityManifest) []string {
	t.Helper()
	for _, row := range m.Traits {
		if row.Trait != "style.direction" {
			continue
		}
		switch row.AppliesTo.Scope {
		case "allKinds":
			var kinds []string
			for _, k := range m.Kinds {
				kinds = append(kinds, k.Kind)
			}
			return kinds
		case "namedKinds":
			return row.AppliesTo.Kinds
		default:
			t.Fatalf("style.direction: uninterpretable scope %q", row.AppliesTo.Scope)
		}
	}
	t.Fatal("the roster declares no style.direction trait — this sweep has nothing to hold the host to")
	return nil
}

func TestStyleDirectionRulesHoldOnEveryRosterKind(t *testing.T) {
	manifest := loadRenderFidelityManifest(t)
	owed := ownedDirectionKinds(t, manifest)
	if len(owed) == 0 {
		t.Fatal("style.direction is owed by no kind — the sweep would assert nothing")
	}

	corpus := findCorpusDir(t)
	paths, err := filepath.Glob(filepath.Join(corpus, "nodes", "*.json"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no node fixtures under %s (err %v)", corpus, err)
	}
	sort.Strings(paths)

	reached := make(map[string]int)
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		fixture := strings.TrimSuffix(filepath.Base(path), ".json")
		node, err := wire.DecodeNode(strings.TrimRight(string(raw), "\r\n"))
		if err != nil {
			// Not this sweep's question: the codec legs own decode, and a fixture
			// this host cannot decode yet reaches no renderer either. Coverage is
			// what catches a kind left unreached as a result.
			continue
		}
		render := func(n wire.Node) (string, bool) {
			html, err := RenderHTML(n, BindingSources{})
			if err != nil || nodeOpeningTag(html, n.ID) == "" {
				return "", false
			}
			return html, true
		}

		omitted, ok := render(withRootDirection(node, ""))
		if !ok {
			// A root that resolves to nothing (a false `visible` predicate, a
			// refusal) has no wrapper to carry a direction on.
			continue
		}
		rtl, ok := render(withRootDirection(node, "rtl"))
		if !ok {
			t.Errorf("%s: declaring rtl made the root's wrapper disappear", fixture)
			continue
		}
		auto, _ := render(withRootDirection(node, "auto"))

		kind := node.Kind.Tag
		root := nodeOpeningTag(rtl, node.ID)
		failed := false
		if !strings.Contains(root, ` dir="rtl"`) {
			t.Errorf("%s (%s): rule 1 — a declared rtl is not emitted on the root's own wrapper: %s",
				fixture, kind, root)
			failed = true
		}
		if !hasClassToken(root, "fuaran-dir-rtl") {
			t.Errorf("%s (%s): rule 2 — the declared run is not isolated (no fuaran-dir-rtl class) on the "+
				"root's own wrapper: %s", fixture, kind, root)
			failed = true
		}
		if auto != omitted {
			t.Errorf("%s (%s): rule 4 — `auto` rendered differently from the member omitted", fixture, kind)
			failed = true
		}
		stripped := strings.Replace(rtl, root, strings.Replace(
			strings.Replace(root, ` dir="rtl"`, "", 1), " fuaran-dir-rtl", "", 1), 1)
		if stripped != omitted {
			t.Errorf("%s (%s): rule 5 — declaring rtl changed something besides the root's dir and its "+
				"isolation class\nstripped:\n%s\nomitted:\n%s", fixture, kind, stripped, omitted)
			failed = true
		}

		// Rule 3: the same tree declaring ltr, inside an rtl block. The block is
		// decoded around a placeholder and the decoded tree swapped in, rather
		// than re-decoded from text: the corpus carries trees AT the depth limit,
		// and one more level of nesting would be refused by the codec before the
		// renderer is asked anything.
		block, err := wire.DecodeNode(`{"id":"` + directionProbeBlockID + `","kind":{"$type":"Box","children":[` +
			`{"id":"` + directionProbeBlockID + `-slot","kind":{"$type":"Badge","label":"x","variant":"Neutral"}}` +
			`],"layout":{"$type":"Flex","direction":"Vertical","wrap":false},"role":"Group"},` +
			`"style":{"direction":"rtl"}}`)
		if err != nil {
			t.Fatalf("decoding the probe block: %v", err)
		}
		if _, ok := block.Kind.Fields["children"].(wire.Arr); !ok {
			t.Fatalf("the probe block's children did not decode as an array: %T", block.Kind.Fields["children"])
		}
		block.Kind.Fields["children"] = wire.Arr{withRootDirection(node, "ltr")}
		nested, err := RenderHTML(block, BindingSources{})
		if err != nil {
			t.Errorf("%s (%s): rule 3 — the nested probe failed to render: %v", fixture, kind, err)
			failed = true
		} else if inner := nodeOpeningTag(nested, node.ID); !strings.Contains(inner, ` dir="ltr"`) {
			t.Errorf("%s (%s): rule 3 — a declared ltr inside an rtl block did not win over the "+
				"inherited direction: %q", fixture, kind, inner)
			failed = true
		}

		if !failed {
			reached[kind]++
		}
	}

	var unreached []string
	for _, kind := range owed {
		if reached[kind] == 0 {
			unreached = append(unreached, kind)
		}
	}
	if len(unreached) > 0 {
		t.Errorf("style.direction is owed by every roster kind, and these were not shown to meet it "+
			"(no decodable, renderable corpus root, or a rule failed above): %v", unreached)
	}
	t.Logf("style.direction rules 1-5 held on %d of %d owed kinds", len(owed)-len(unreached), len(owed))
}
