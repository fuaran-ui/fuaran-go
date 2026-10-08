package ops

import (
	"errors"
	"testing"

	"github.com/fuaran-ui/fuaran-go/wire"
)

// Phase 2172: every op addresses its target by id alone, so a tree that holds
// one id twice makes every later id-addressed op ambiguous. The decoder accepts
// a repeated id, and an apply could BUILD one from parts that each decoded
// cleanly. The check reads the op's RESULT and charges it only for the ids it
// installed. editNode, updateLoading and insertChild are limits_test.go's
// helpers.

func replaceRoot(n wire.Node) wire.Obj {
	return wire.Obj{Tag: "ReplaceRoot", Fields: map[string]wire.Value{"node": n}}
}

func batch(ops ...wire.Obj) wire.Obj {
	items := make(wire.Arr, len(ops))
	for i, o := range ops {
		items[i] = o
	}
	return wire.Obj{Tag: "Batch", Fields: map[string]wire.Value{"ops": items}}
}

func codeOf(t *testing.T, err error) ApplyErrorCode {
	t.Helper()
	var ae *ApplyError
	if !errors.As(err, &ae) {
		t.Fatalf("err = %v (%T), want a *ApplyError", err, err)
	}
	return ae.Code
}

func TestApplyRefusesAnInstalledDuplicateID(t *testing.T) {
	base := box("r", box("a"), box("b"))
	cases := map[string]wire.Obj{
		"ReplaceRoot repeats an id":               replaceRoot(box("r2", box("x"), box("x"))),
		"ReplaceRoot repeats its root id below":   replaceRoot(box("r", box("r"))),
		"EditNode collides with the tree":         editNode("a", box("carrier", box("b"))),
		"EditNode repeats an id within its kind":  editNode("a", box("carrier", box("x"), box("x"))),
		"EditNode repeats the edited node's id":   editNode("a", box("carrier", box("a"))),
		"UpdateState collides with the tree":      updateLoading("a", box("b")),
		"InsertChild repeats an id within itself": insertChild("r", box("c", box("x"), box("x"))),
		"InsertChild collides with the tree":      insertChild("r", box("b")),
		"Batch result repeats an installed id":    batch(editNode("a", box("carrier", box("b")))),
	}
	for name, op := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := Apply(op, base)
			if err == nil {
				t.Fatal("the op applied and left a duplicate id in the tree")
			}
			if code := codeOf(t, err); code != CodeDuplicateNodeID {
				t.Errorf("code = %q, want %q", code, CodeDuplicateNodeID)
			}
			if got.ID != base.ID {
				t.Errorf("a refusal must return the input tree")
			}
		})
	}
}

func TestApplyAcceptsWhatOnlyLooksLikeADuplicate(t *testing.T) {
	cases := map[string]struct {
		tree wire.Node
		op   wire.Obj
	}{
		"ReplaceRoot reuses the replaced tree's ids": {box("r", box("a"), box("b")), replaceRoot(box("r", box("a"), box("b")))},
		"EditNode restates the children it replaces": {box("r", box("a", box("c"))), editNode("a", box("carrier", box("c"), box("d")))},
		"UpdateState replaces its own alternative": {
			box("r", setExtra(box("a"), "state", wire.Obj{Fields: map[string]wire.Value{"onLoading": box("l")}})),
			updateLoading("a", box("l", box("l2"))),
		},
		"a duplicate the op did not install is not charged to it": {
			box("r", box("x"), box("y", box("x")), box("z")), editNode("z", box("carrier", box("w"))),
		},
		"a Batch whose intermediate state duplicates but whose result does not": {
			box("r", box("a"), box("s", box("b"))), batch(editNode("a", box("carrier", box("b"))), editNode("s", box("carrier"))),
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Apply(c.op, c.tree); err != nil {
				t.Fatalf("refused: %v", err)
			}
		})
	}
}

func TestLimitExceededTakesPrecedenceOverADuplicate(t *testing.T) {
	// limitsApply's editnode-repeated-id-past-maxdepth pins this order.
	_, err := Apply(editNode("n1", box("carrier", box("n1"))), chain(wire.MaxNodeDepth))
	if code := codeOf(t, err); code != CodeLimitExceeded {
		t.Errorf("code = %q, want %q", code, CodeLimitExceeded)
	}
}
