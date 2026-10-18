package wire

import (
	"strings"
	"testing"
)

// The completed `Tabs` / `Stepper` specs (Phase 1677) — the residue Phase 1064
// named and deliberately did not take.
//
// Every member of both kinds is now typed to the corpus IDL's `kinds` entry.
// The corpus carries accept fixtures for both, so what this file adds is the
// REFUSAL side, which no fixture pins: before this, a header with no label, a
// non-string tag and a wrong-typed `activeTag` all round-tripped byte-perfectly.
//
// It is also where the `Stepper.activeStep` REQUIREDNESS decision is recorded as
// behaviour rather than as a comment. The IDL declares it `required` where
// `Tabs.activeIndex` is `omitDefault`, and the reference and Python hosts both
// require it; no corpus fixture covers it, which is why it needed deciding.

func decodeErrOf(t *testing.T, doc string) *DecodeError {
	t.Helper()
	_, err := DecodeNode(doc)
	if err == nil {
		t.Fatalf("expected a refusal, got none for:\n%s", doc)
	}
	de, ok := err.(*DecodeError)
	if !ok {
		t.Fatalf("expected a *DecodeError, got %T: %v", err, err)
	}
	return de
}

func mustRefuse(t *testing.T, doc string, code DecodeErrorCode, path string) {
	t.Helper()
	de := decodeErrOf(t, doc)
	if de.Code != code || !strings.HasPrefix(de.Path, path) {
		t.Errorf("got %s at %q, want %s at %q", de.Code, de.Path, code, path)
	}
}

func mustAccept(t *testing.T, doc string) Node {
	t.Helper()
	node, err := DecodeNode(doc)
	if err != nil {
		t.Fatalf("expected acceptance, got %v for:\n%s", err, doc)
	}
	return node
}

const tabs1677Child = `{"id":"c","kind":{"$type":"Markdown","text":"one"}}`

func tabs1677Doc(extra string) string {
	return `{"id":"t","kind":{"$type":"Tabs","children":[` + tabs1677Child + `]` + extra + `}}`
}

func TestTabsChildrenAreRequiredAndDecodeAsNodes(t *testing.T) {
	// A node-valued position left structural is the one omission that costs more
	// than it saves: the nodes inside it never reach the node decoder, so nothing
	// in them is counted, bounded or typed.
	mustRefuse(t, `{"id":"t","kind":{"$type":"Tabs"}}`, CodeMissingField, "$.kind.children")
	mustRefuse(t,
		`{"id":"t","kind":{"$type":"Tabs","children":[{"id":"c","kind":{"$type":"Markdown"}}]}}`,
		CodeMissingField, "$.kind.children[0].kind.text")
}

func TestTabHeadersAreTypedByIndex(t *testing.T) {
	mustRefuse(t, tabs1677Doc(`,"tabHeaders":[{"label":"A"},{"icon":"x"}]`),
		CodeMissingField, "$.kind.tabHeaders[1].label")
	mustRefuse(t, tabs1677Doc(`,"tabHeaders":[{"icon":7,"label":"A"}]`),
		CodeWrongType, "$.kind.tabHeaders[0].icon")
	// `disabled` is a Binding<bool>, so a tab may be disabled by state.
	mustAccept(t, tabs1677Doc(`,"tabHeaders":[{"disabled":{"$type":"State","key":"busy"},"label":"A"}]`))
	mustRefuse(t, tabs1677Doc(`,"tabHeaders":[{"disabled":"yes","label":"A"}]`),
		CodeWrongType, "$.kind.tabHeaders[0].disabled")
}

func TestTabTagsAndActiveTagAreTyped(t *testing.T) {
	mustRefuse(t, tabs1677Doc(`,"tabTags":["a",7]`), CodeWrongType, "$.kind.tabTags[1]")
	mustRefuse(t, tabs1677Doc(`,"activeTag":7`), CodeWrongType, "$.kind.activeTag")
	mustAccept(t, tabs1677Doc(`,"activeTag":{"$type":"State","key":"tab"},"tabTags":["a","b"]`))
}

func TestTabsOrientationIsOmittedAtHorizontalAndAliased(t *testing.T) {
	// An explicit default normalises to the omitted canonical form, or the
	// pre-phase spelling becomes a SECOND canonical form.
	node := mustAccept(t, tabs1677Doc(`,"orientation":"Horizontal"`))
	if _, present := node.Kind.Fields["orientation"]; present {
		t.Errorf("an explicit Horizontal must normalise away, got %v", node.Kind.Fields["orientation"])
	}
	// The §3.6 aliases every other orientation slot in this host accepts.
	aliased := mustAccept(t, tabs1677Doc(`,"orientation":"Column"`))
	if got := aliased.Kind.Fields["orientation"]; !isStr(got, "Vertical") {
		t.Errorf("orientation = %v, want Vertical", got)
	}
	mustRefuse(t, tabs1677Doc(`,"orientation":"Sideways"`), CodeUnknownDUCase, "$.kind.orientation")
}

func TestTabsHandlerSlotsNormaliseToTheSentinel(t *testing.T) {
	node := mustAccept(t, tabs1677Doc(`,"onSelect":"<closure>","onSelectTag":"<closure>"`))
	for _, slot := range []string{"onSelect", "onSelectTag"} {
		if got := node.Kind.Fields[slot]; !isStr(got, "<closure>") {
			t.Errorf("%s = %v, want the closure sentinel", slot, got)
		}
	}
}

func TestTabsStillToleratesAnUnknownKeyOnTheKind(t *testing.T) {
	// §2 rule 2. Strictness is applied one level in, inside `TabHeader`, which is
	// a closed record in the IDL — the kind itself stays tolerant of what a later
	// spec version adds.
	node := mustAccept(t, tabs1677Doc(`,"futureSlot":"x"`))
	if got := node.Kind.Fields["futureSlot"]; !isStr(got, "x") {
		t.Errorf("an unknown key on the kind was not preserved: %v", got)
	}
}

func TestStepperActiveStepOmitsAtTheDeclaredDefault(t *testing.T) {
	// Phase 2300 — the declared-default rule: `Stepper.activeStep` is
	// `omitDefault Static{value=0}` in the IDL (the `Tabs.activeIndex` shape),
	// read from the generated row. An absent member decodes (the row restores
	// the first step), an explicit member at the default normalises away, and a
	// non-default one is the document's own value and survives. Until Phase
	// 2300 this host required the member, on the argument that a stepper with
	// no declared step has no position; the vocabulary now declares the
	// position, so the decision is the row's and not this host's.
	absent := mustAccept(t, `{"id":"s","kind":{"$type":"Stepper","children":[`+tabs1677Child+`]}}`)
	if _, present := absent.Kind.Fields["activeStep"]; present {
		t.Error("an absent Stepper.activeStep must stay absent on the model (the omitted form is canonical)")
	}
	atDefault := mustAccept(t,
		`{"id":"s","kind":{"$type":"Stepper","activeStep":{"$type":"Static","value":0},"children":[`+tabs1677Child+`]}}`)
	if _, present := atDefault.Kind.Fields["activeStep"]; present {
		t.Error("Stepper.activeStep at the declared default Static 0 must normalise away")
	}
	kept := mustAccept(t,
		`{"id":"s","kind":{"$type":"Stepper","activeStep":{"$type":"Static","value":1},"children":[`+tabs1677Child+`]}}`)
	if _, present := kept.Kind.Fields["activeStep"]; !present {
		t.Error("a non-default Stepper.activeStep is the document's own value and must survive")
	}
	tabs := mustAccept(t, tabs1677Doc(`,"activeIndex":{"$type":"Static","value":0}`))
	if _, present := tabs.Kind.Fields["activeIndex"]; present {
		t.Error("Tabs.activeIndex at the identity Static 0 must normalise away")
	}
}

func TestStepperChildrenAreRequiredAndTyped(t *testing.T) {
	mustRefuse(t,
		`{"id":"s","kind":{"$type":"Stepper","activeStep":{"$type":"Static","value":1}}}`,
		CodeMissingField, "$.kind.children")
}
