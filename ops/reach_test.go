package ops

import (
	"strings"
	"testing"

	"github.com/fuaran-ui/fuaran-go/wire"
)

const flex = `"layout":{"$type":"Flex","direction":"Vertical","wrap":false},"role":"Group"`

// reachTreeJSON holds a node in every position class: an ordered list (the
// root Box), an envelope fallback holding a Box with a child (a), the two
// state alternatives and a Switch's case and default (sw), and a Mount's slot
// input beside a value input (m).
const reachTreeJSON = `{"id":"r","kind":{"$type":"Box","children":[` +
	`{"fallback":{"id":"fb","kind":{"$type":"Box","children":[{"id":"fbk","kind":{"$type":"Markdown","text":"k"}}],` + flex + `}},` +
	`"id":"a","kind":{"$type":"Markdown","text":"a"}},` +
	`{"id":"sw","kind":{"$type":"Switch","cases":[{"child":{"id":"c0","kind":{"$type":"Markdown","text":"c0"}},"match":"x"}],` +
	`"default":{"id":"d","kind":{"$type":"Markdown","text":"d"}},"stateKey":"k"},` +
	`"state":{"onEmpty":{"id":"e","kind":{"$type":"Markdown","text":"e"}},"onLoading":{"id":"l","kind":{"$type":"Markdown","text":"l"}}}},` +
	`{"id":"m","kind":{"$type":"Mount","capabilities":[],"channel":{"direction":"OutOnly"},` +
	`"inputs":{"seed":{"$type":"SlotArg","tree":{"id":"seed","kind":{"$type":"Markdown","text":"s"}}},"title":{"$type":"Str","value":"T"}},` +
	`"scopeId":"g"}}` +
	`],` + flex + `}}`

func reachTree(t *testing.T) wire.Node {
	t.Helper()
	return mustDecode(t, reachTreeJSON)
}

// The apply engine's walks reach every position a node holds another in (the
// reference's keyed walk). Each test here failed while they stopped short of
// the envelope fallback. They use only the engine's entry points, so they run
// against the walks they replaced as well.

// Disagreement 1 — the apply engine's lookup never reached the envelope
// fallback, where the reference's keyed walk does, so an op addressed to a
// node held there was refused NodeNotFound.
func TestAnOpAddressedIntoTheEnvelopeFallbackApplies(t *testing.T) {
	tree := reachTree(t)
	op := wire.Obj{Tag: "UpdateTooltip", Fields: map[string]wire.Value{"target": wire.Str("fbk")}}
	if _, err := Apply(op, tree); err != nil {
		t.Fatalf("UpdateTooltip on a node held in a fallback: %v", err)
	}
}

// Disagreement 2 — the structural ops' parent search stopped at the fallback
// too, so a child of a Box held there could not be removed.
func TestRemoveNodeFindsItsParentThroughTheEnvelopeFallback(t *testing.T) {
	tree := reachTree(t)
	op := wire.Obj{Tag: "RemoveNode", Fields: map[string]wire.Value{"target": wire.Str("fbk")}}
	after, err := Apply(op, tree)
	if err != nil {
		t.Fatalf("RemoveNode under a fallback: %v", err)
	}
	if got := mustEncode(t, after); strings.Contains(got, `"fbk"`) || !strings.Contains(got, `"id":"fb","kind":{"$type":"Box","children":[]`) {
		t.Errorf("fbk was not removed from fb's children: %s", got)
	}
}

// Disagreement 3 — the §21 bounds after an op did not count the fallback, so
// a subtree nested past MaxNodeDepth there was installed although no host
// would decode the result.
func TestTheDepthLimitCountsTheEnvelopeFallback(t *testing.T) {
	tree := reachTree(t)
	carrier := wire.Node{ID: "carrier", Kind: wire.Obj{Tag: "Markdown", Fields: map[string]wire.Value{"text": wire.Str("x")}}}
	carrier = setExtra(carrier, "fallback", chain(wire.MaxNodeDepth-1))
	if code := applyErrCode(t, insertChild("r", carrier), tree); code != CodeLimitExceeded {
		t.Errorf("code = %s, want %s", code, CodeLimitExceeded)
	}
}
