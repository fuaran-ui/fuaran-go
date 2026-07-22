package conformance

import (
	"strings"
	"testing"

	"github.com/fuaran-ui/fuaran-go/ops"
	"github.com/fuaran-ui/fuaran-go/wire"
)

// The apply-conformance leg: each corpus op fixture is decoded through the
// real DecodeOp and folded over a base tree carrying the ids it references;
// the applied tree re-encodes to a separately-authored expected tree — so a
// pass proves the apply fold produces the same canonical tree the sibling
// hosts produce. The canApply ≡ apply-success law is asserted on every case.

const (
	metricFull = `{"id":"metric-1","kind":{"$type":"Metric","format":{"$type":"Currency","code":"GBP"},"icon":"trending-up","label":"Revenue","subtext":"vs last month","tone":"Brand","trend":{"$type":"Static","value":0.07},"trendFormat":{"$type":"Percent","decimals":1},"value":{"$type":"Static","value":1234.5}}}`
	markdown1  = `{"id":"markdown-1","kind":{"$type":"Markdown","text":"Updated hourly."}}`
)

func dashRoot(children ...string) string {
	return `{"id":"root","kind":{"$type":"Box","children":[` + strings.Join(children, ",") +
		`],"layout":{"$type":"Auto"},"role":"Dashboard"}}`
}

func stack1(children ...string) string {
	return `{"id":"stack-1","kind":{"$type":"Box","children":[` + strings.Join(children, ",") +
		`],"layout":{"$type":"Flex","direction":"Vertical","wrap":false},"role":"Group"}}`
}

func card1(children ...string) string {
	return `{"id":"card-1","kind":{"$type":"Box","children":[` + strings.Join(children, ",") +
		`],"layout":{"$type":"Flex","direction":"Vertical","wrap":false},"role":"Card"}}`
}

// withNodeExtra appends an extras section (style / state) to a node's JSON.
// The result stays canonical because "state" / "style" sort after "id"/"kind".
func withNodeExtra(nodeJSON, extra string) string {
	return nodeJSON[:len(nodeJSON)-1] + "," + extra + "}"
}

func gridColumn(label string) string {
	return `{"kind":{"$type":"Text"},"label":"` + label + `","value":"<closure>"}`
}

// gridColumnFmt is gridColumn with a non-default (kept-on-the-wire) format.
func gridColumnFmt(label, format string) string {
	return `{"format":` + format + `,"kind":{"$type":"Text"},"label":"` + label + `","value":"<closure>"}`
}

func gridBase(columns ...string) string {
	return `{"id":"grid-1","kind":{"$type":"DataGrid","columns":[` + strings.Join(columns, ",") +
		`],"rowKey":"<closure>","source":{"$type":"Static","value":"<opaque>"}}}`
}

const chartBase = `{"id":"chart-1","kind":{"$type":"Chart","kind":"Bar","source":{"$type":"Static","value":"<opaque>"},"stacked":false,"xField":"month","yFields":["revenue","cost"]}}`

func formBase(nameRequired, ageRequired string) string {
	return `{"id":"form-1","kind":{"$type":"Form","fields":[` +
		`{"id":"name","kind":{"$type":"Text","value":{"$type":"Static","value":""}},"label":"Name","required":` + nameRequired + `},` +
		`{"id":"age","kind":{"$type":"Number","value":{"$type":"Static","value":0}},"label":"Age","required":` + ageRequired + `}` +
		`],"onSubmit":{"$type":"Chain","ops":[]},"submitLabel":"Save"}}`
}

func decodeOpFixture(t *testing.T, corpus, name string) wire.Obj {
	t.Helper()
	op, err := wire.DecodeOp(readFixture(t, corpus, "ops/"+name+".json"))
	if err != nil {
		t.Fatalf("decoding op fixture %s: %v", name, err)
	}
	return op
}

func decodeTree(t *testing.T, canonicalJSON string) wire.Node {
	t.Helper()
	node, err := wire.DecodeNode(canonicalJSON)
	if err != nil {
		t.Fatalf("decoding base tree: %v", err)
	}
	return node
}

func encodeTree(t *testing.T, n wire.Node) string {
	t.Helper()
	s, err := wire.EncodeNode(n)
	if err != nil {
		t.Fatalf("encoding tree: %v", err)
	}
	return s
}

// TestApplyCorpusFixtures folds each corpus op fixture over its base tree and
// asserts the canonical expected tree, plus the dry-run law.
func TestApplyCorpusFixtures(t *testing.T) {
	corpus, _ := loadCorpus(t)

	metricEdited := `{"id":"metric-1","kind":{"$type":"Markdown","text":"Edited"}}`
	metricRelabelled := strings.Replace(metricFull,
		`"label":"Revenue"`,
		`"label":"Updated revenue"`, 1)
	metricRebound := strings.Replace(metricFull,
		`"value":{"$type":"Static","value":1234.5}`,
		`"value":{"$type":"Static","value":99.5}`, 1)
	metricStyled := withNodeExtra(metricFull, `"style":{"emphasis":"Loud","tone":"Success","weight":"Spacious"}`)
	metricLoading := withNodeExtra(metricFull, `"state":{"onLoading":{"id":"skel-1","kind":{"$type":"Skeleton","rows":3}}}`)

	dashEmpty := readFixture(t, corpus, "nodes/dash-empty.json")
	dashFilled := strings.Replace(dashEmpty, `"children":[]`, `"children":[`+metricFull+`]`, 1)

	cases := []struct {
		fixture  string
		base     string
		expected string
	}{
		{"op-editnode", dashRoot(metricFull), dashRoot(metricEdited)},
		{"op-updateprop", dashRoot(metricFull), dashRoot(metricRelabelled)},
		{"op-replacebinding", dashRoot(metricFull), dashRoot(metricRebound)},
		{"op-updatestyle", dashRoot(metricFull), dashRoot(metricStyled)},
		{"op-updatestate", dashRoot(metricFull), dashRoot(metricLoading)},
		{"op-insertchild", dashEmpty, dashFilled},
		{"op-removenode", stack1(metricFull, markdown1), stack1(markdown1)},
		{"op-movenode",
			dashRoot(stack1(metricFull, markdown1), card1()),
			dashRoot(stack1(markdown1), card1(metricFull))},
		{"op-reorderchildren", stack1(metricFull, markdown1), stack1(markdown1, metricFull)},
		{"op-replaceroot", markdown1, readFixture(t, corpus, "nodes/composite-root.json")},
		{"op-batch", stack1(metricFull, markdown1), stack1(markdown1)},
		// Nested paths (WIRE_FORMAT.md §3.4 — typed-traversal parity)
		{"op-updateprop-nested-column0-label",
			gridBase(gridColumn("Channel"), gridColumn("Spend")),
			gridBase(gridColumn("Channel name"), gridColumn("Spend"))},
		{"op-updateprop-nested-column1-label",
			gridBase(gridColumn("Channel"), gridColumn("Spend")),
			gridBase(gridColumn("Channel"), gridColumn("Spend (GBP)"))},
		{"op-updateprop-nested-object-value",
			gridBase(gridColumn("Channel"), gridColumn("Spend")),
			gridBase(gridColumnFmt("Channel", `{"$type":"Currency","code":"GBP"}`), gridColumn("Spend"))},
		{"op-updateprop-nested-yfield0", chartBase, strings.Replace(chartBase, `"revenue"`, `"sales"`, 1)},
		{"op-updateprop-nested-yfield1", chartBase, strings.Replace(chartBase, `"cost"`, `"profit"`, 1)},
		{"op-updateprop-nested-field0-required", formBase("false", "false"), formBase("true", "false")},
		{"op-updateprop-nested-field1-required", formBase("false", "true"), formBase("false", "false")},
	}

	for _, c := range cases {
		t.Run(c.fixture, func(t *testing.T) {
			op := decodeOpFixture(t, corpus, c.fixture)
			base := decodeTree(t, c.base)
			if !ops.CanApply(op, base) {
				t.Error("CanApply = false for a succeeding fixture (law: canApply ≡ apply success)")
			}
			applied, err := ops.Apply(op, base)
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if got := encodeTree(t, applied); got != c.expected {
				t.Errorf("applied tree diverged: %s", firstDiff(got, c.expected))
			}
		})
	}
}

// TestApplyCorpusErrorFixtures asserts the typed ApplyError codes on the
// corpus fixtures authored to fail at apply time (codes per the sibling
// engines' apply-envelope contract), plus the dry-run law.
func TestApplyCorpusErrorFixtures(t *testing.T) {
	corpus, _ := loadCorpus(t)
	base := decodeTree(t, gridBase(gridColumn("Channel"), gridColumn("Spend")))

	cases := []struct {
		fixture string
		want    ops.ApplyErrorCode
	}{
		{"op-updateprop-nested-badindex", ops.CodePositionOutOfRange},
		{"op-updateprop-nested-badfield", ops.CodeFieldNotFound},
		{"op-updateprop-nested-malformed", ops.CodePathInvalid},
	}
	for _, c := range cases {
		t.Run(c.fixture, func(t *testing.T) {
			op := decodeOpFixture(t, corpus, c.fixture)
			if ops.CanApply(op, base) {
				t.Error("CanApply = true for a failing fixture (law: canApply ≡ apply success)")
			}
			after, err := ops.Apply(op, base)
			aerr, ok := err.(*ops.ApplyError)
			if !ok {
				t.Fatalf("expected an *ops.ApplyError, got %v", err)
			}
			if aerr.Code != c.want {
				t.Errorf("code = %s, want %s", aerr.Code, c.want)
			}
			if got, want := encodeTree(t, after), encodeTree(t, base); got != want {
				t.Errorf("failed apply mutated the tree: %s", firstDiff(got, want))
			}
		})
	}
}
