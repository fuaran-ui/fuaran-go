// Package merge is the deterministic, author-agnostic 3-way tree merge — the Go
// conformant host of the merge the F#/TS/Python hosts run, certified
// byte-for-byte against wire-format-fixtures/merge-conformance/.
//
// A node decomposes into independent facets, each merged on its own: kind (the
// node's own kind-fields, children neutralised), the SemanticStyle sub-fields
// (tone/weight/emphasis/role/voice, merged INDEPENDENTLY so A's tone + B's voice
// auto-blend), state, accessibility, and children (the ordered child-id list).
// When a facet changed on at most one side, that side's value is taken; when
// both changed it differently it is a conflict (returned, not silently picked).
// The one structural case auto-merged across both sides is disjoint pure inserts
// into the same parent, ordered by NodeId code-point (Ordinal) — the
// deterministic, wall-clock-free tie-break. Facet equality is canonical-JSON
// bytes, the same oracle the corpus commits to.
package merge

import (
	"sort"
	"strings"

	"github.com/fuaran-ui/fuaran-go/wire"
)

// styleDefaults — an absent style ⟺ all of these (WIRE_FORMAT §3.1).
var styleDefaults = map[string]string{"emphasis": "Normal", "tone": "Default", "weight": "Standard"}

var facetExtras = map[string]bool{"style": true, "state": true, "accessibility": true}

// ── Conflict + result vocabulary ────────────────────────────────────────────

const (
	classConcurrentEdit      = "ConcurrentEdit"
	classReorderVsStructural = "ReorderVsStructural"

	choiceKeepPrimary   = "KeepPrimary"
	choiceKeepSecondary = "KeepSecondary"
	choiceKeepBase      = "KeepBase"
)

// Conflict is a conflicting (NodeID, Facet) cell. NodeID + Facet are the minimal
// identity (the author-agnostic surface); the remaining fields carry the
// DAG-layer resolution detail.
type Conflict struct {
	NodeID        string
	Facet         string
	ConflictClass string
	Base          *string
	Primary       *string
	Secondary     *string
	SecondaryTag  *string
	PrimacyHeld   bool
	Choices       []string
}

// Result is a merge outcome: a usable Tree (OK) with any human-primacy-resolved
// conflicts, or a set of blocking Conflicts (OK false, trunk unchanged).
type Result struct {
	OK        bool
	Tree      wire.Node
	Resolved  []Conflict // conflicts human-primacy auto-resolved (pin held)
	Conflicts []Conflict // blocking conflicts (when OK is false)
}

// ── merge authorship (the human-primacy layer) ──────────────────────────────

// Author tags a branch: Primary (the human, wins conflicted cells) or Secondary
// (an agent, with an opaque host tag). The author-agnostic merge uses two
// Secondaries.
type Author struct {
	Primary bool
	Tag     *string
}

// Primary is the precedence-holding branch (the human).
func Primary() Author { return Author{Primary: true} }

// Secondary is a non-precedence branch (an agent), with an opaque host tag.
func Secondary(tag *string) Author { return Author{Primary: false, Tag: tag} }

type resolution struct {
	aIsPrimary   bool
	pinHeld      bool
	choices      []string
	secondaryTag *string
}

func resolveAuthor(a, b Author) resolution {
	switch {
	case a.Primary && !b.Primary:
		return resolution{true, true, []string{choiceKeepPrimary, choiceKeepSecondary, choiceKeepBase}, b.Tag}
	case !a.Primary && b.Primary:
		return resolution{false, true, []string{choiceKeepPrimary, choiceKeepSecondary, choiceKeepBase}, a.Tag}
	case !a.Primary && !b.Primary:
		return resolution{false, false, []string{choiceKeepBase, choiceKeepSecondary}, a.Tag}
	default: // two primaries — no precedence, host decides
		return resolution{false, false, []string{choiceKeepBase}, nil}
	}
}

var agnostic = resolveAuthor(Secondary(nil), Secondary(nil))

// ── structural helpers ──────────────────────────────────────────────────────

func nonFacetExtras(n wire.Node) map[string]wire.Value {
	out := make(map[string]wire.Value)
	for k, v := range n.Extras {
		if !facetExtras[k] {
			out[k] = v
		}
	}
	return out
}

func childrenOf(n wire.Node) []wire.Node {
	arr, ok := n.Kind.Fields["children"].(wire.Arr)
	if !ok {
		return nil
	}
	var out []wire.Node
	for _, item := range arr {
		if c, ok := item.(wire.Node); ok {
			out = append(out, c)
		}
	}
	return out
}

func childlessKind(kind wire.Obj) wire.Obj {
	if _, ok := kind.Fields["children"].(wire.Arr); !ok {
		return kind
	}
	fields := make(map[string]wire.Value, len(kind.Fields))
	for k, v := range kind.Fields {
		fields[k] = v
	}
	fields["children"] = wire.Arr{}
	return wire.Obj{Tag: kind.Tag, Fields: fields}
}

func withKindChildren(kind wire.Obj, children []wire.Node) wire.Obj {
	if _, ok := kind.Fields["children"].(wire.Arr); !ok {
		return kind
	}
	arr := make(wire.Arr, len(children))
	for i, c := range children {
		arr[i] = c
	}
	fields := make(map[string]wire.Value, len(kind.Fields))
	for k, v := range kind.Fields {
		fields[k] = v
	}
	fields["children"] = arr
	return wire.Obj{Tag: kind.Tag, Fields: fields}
}

// mkNode rebuilds a node with controlled facets, omitting an absent
// style/state/accessibility (the wire's absent ⟺ default). Non-facet extras
// (motion, extraAttributes) carry over from src.
func mkNode(src wire.Node, kind wire.Obj, style, state, acc wire.Value) wire.Node {
	extras := nonFacetExtras(src)
	if style != nil {
		extras["style"] = style
	}
	if state != nil {
		extras["state"] = state
	}
	if acc != nil {
		extras["accessibility"] = acc
	}
	return wire.Node{ID: src.ID, Kind: kind, Extras: extras}
}

func mustEncode(v wire.Value) string {
	s, _ := wire.EncodeValue(v)
	return s
}

// ── facet-isolation canonical probes (closure-safe bytes) ───────────────────

func kindCanonical(n wire.Node) string {
	return mustEncode(mkNode(n, childlessKind(n.Kind), nil, nil, nil))
}

func stateCanonical(shell wire.Obj, n wire.Node) string {
	return mustEncode(mkNode(n, shell, nil, n.Extras["state"], nil))
}

func accessibilityCanonical(shell wire.Obj, n wire.Node) string {
	return mustEncode(mkNode(n, shell, nil, nil, n.Extras["accessibility"]))
}

func styleField(n wire.Node, name string) *string {
	if style, ok := n.Extras["style"].(wire.Obj); ok {
		if v, ok := style.Fields[name].(wire.Str); ok {
			s := string(v)
			return &s
		}
	}
	if d, ok := styleDefaults[name]; ok {
		return &d
	}
	return nil
}

// ── facet pickers ───────────────────────────────────────────────────────────

func strEq(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func recordConflict(conflicts *[]Conflict, res resolution, nodeID, facet string, baseV, aV, bV *string) int {
	var primaryV, secondaryV *string
	if res.pinHeld {
		if res.aIsPrimary {
			primaryV, secondaryV = aV, bV
		} else {
			primaryV, secondaryV = bV, aV
		}
	} else {
		secondaryV = aV
	}
	conflictClass := classConcurrentEdit
	if facet == "children" {
		conflictClass = classReorderVsStructural
	}
	*conflicts = append(*conflicts, Conflict{
		NodeID: nodeID, Facet: facet, ConflictClass: conflictClass,
		Base: baseV, Primary: primaryV, Secondary: secondaryV,
		SecondaryTag: res.secondaryTag, PrimacyHeld: res.pinHeld, Choices: res.choices,
	})
	if res.pinHeld {
		if res.aIsPrimary {
			return 1
		}
		return 2
	}
	return 0
}

// pickField merges a scalar facet value; returns the chosen value.
func pickField(conflicts *[]Conflict, res resolution, nodeID, facet string, baseV, aV, bV *string) *string {
	aCh := !strEq(aV, baseV)
	bCh := !strEq(bV, baseV)
	if aCh && bCh && !strEq(aV, bV) {
		pick := recordConflict(conflicts, res, nodeID, facet, baseV, aV, bV)
		return []*string{baseV, aV, bV}[pick]
	}
	if aCh {
		return aV
	}
	if bCh {
		return bV
	}
	return baseV
}

// pickCanonical merges a canonical-bytes facet; returns 0=base, 1=a, 2=b.
func pickCanonical(conflicts *[]Conflict, res resolution, nodeID, facet, baseC, aC, bC string) int {
	aCh := aC != baseC
	bCh := bC != baseC
	if aCh && bCh && aC != bC {
		return recordConflict(conflicts, res, nodeID, facet, &baseC, &aC, &bC)
	}
	if aCh {
		return 1
	}
	if bCh {
		return 2
	}
	return 0
}

func mergeStyle(conflicts *[]Conflict, res resolution, nodeID string, base, a, b wire.Node) wire.Value {
	tone := pickField(conflicts, res, nodeID, "style.tone", styleField(base, "tone"), styleField(a, "tone"), styleField(b, "tone"))
	weight := pickField(conflicts, res, nodeID, "style.weight", styleField(base, "weight"), styleField(a, "weight"), styleField(b, "weight"))
	emphasis := pickField(conflicts, res, nodeID, "style.emphasis", styleField(base, "emphasis"), styleField(a, "emphasis"), styleField(b, "emphasis"))
	role := pickField(conflicts, res, nodeID, "style.role", styleField(base, "role"), styleField(a, "role"), styleField(b, "role"))
	voice := pickField(conflicts, res, nodeID, "style.voice", styleField(base, "voice"), styleField(a, "voice"), styleField(b, "voice"))

	// §3.6 omit-when-default on the merged facet too: every field is emitted
	// only when non-default, and an all-default style omits the whole facet —
	// so the merged tree carries the same canonical bytes the codec produces.
	fields := map[string]wire.Value{}
	if v := deref(emphasis); v != "" && v != "Normal" {
		fields["emphasis"] = wire.Str(v)
	}
	if v := deref(tone); v != "" && v != "Default" {
		fields["tone"] = wire.Str(v)
	}
	if v := deref(weight); v != "" && v != "Standard" {
		fields["weight"] = wire.Str(v)
	}
	if role != nil && *role != "None" {
		fields["role"] = wire.Str(*role)
	}
	if voice != nil && *voice != "Default" {
		fields["voice"] = wire.Str(*voice)
	}
	if len(fields) == 0 {
		return nil
	}
	return wire.Obj{Fields: fields}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// isPureAddition — true when head is base with zero removals and zero reorders.
func isPureAddition(baseIDs, headIDs []string) bool {
	headSet := toSet(headIDs)
	baseSet := toSet(baseIDs)
	var survive []string
	for _, i := range baseIDs {
		if headSet[i] {
			survive = append(survive, i)
		}
	}
	var headKept []string
	for _, i := range headIDs {
		if baseSet[i] {
			headKept = append(headKept, i)
		}
	}
	return sliceEq(survive, baseIDs) && sliceEq(headKept, baseIDs)
}

func toSet(ids []string) map[string]bool {
	s := make(map[string]bool, len(ids))
	for _, id := range ids {
		s[id] = true
	}
	return s
}

func sliceEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func ids(nodes []wire.Node) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = n.ID
	}
	return out
}

func nodeMap(nodes []wire.Node) map[string]wire.Node {
	m := make(map[string]wire.Node, len(nodes))
	for _, n := range nodes {
		m[n.ID] = n
	}
	return m
}

// merge3 recursively merges a base node against optional a / b variants.
func merge3(conflicts *[]Conflict, res resolution, base wire.Node, aOpt, bOpt *wire.Node) wire.Node {
	a := base
	if aOpt != nil {
		a = *aOpt
	}
	b := base
	if bOpt != nil {
		b = *bOpt
	}
	nodeID := base.ID
	shell := childlessKind(base.Kind)

	// kind facet
	kindPick := pickCanonical(conflicts, res, nodeID, "kind", kindCanonical(base), kindCanonical(a), kindCanonical(b))
	kindSource := base
	if kindPick == 1 {
		kindSource = a
	} else if kindPick == 2 {
		kindSource = b
	}

	// style sub-fields (independent)
	mergedStyle := mergeStyle(conflicts, res, nodeID, base, a, b)

	// state facet
	statePick := pickCanonical(conflicts, res, nodeID, "state",
		stateCanonical(shell, base), stateCanonical(shell, a), stateCanonical(shell, b))
	mergedState := pickExtra(base, a, b, statePick, "state")

	// accessibility facet
	accPick := pickCanonical(conflicts, res, nodeID, "accessibility",
		accessibilityCanonical(shell, base), accessibilityCanonical(shell, a), accessibilityCanonical(shell, b))
	mergedAcc := pickExtra(base, a, b, accPick, "accessibility")

	// children facet (structural)
	baseKids, aKids, bKids := childrenOf(base), childrenOf(a), childrenOf(b)
	baseIDs, aIDs, bIDs := ids(baseKids), ids(aKids), ids(bKids)
	aStruct := !sliceEq(aIDs, baseIDs)
	bStruct := !sliceEq(bIDs, baseIDs)
	baseM, aM, bM := nodeMap(baseKids), nodeMap(aKids), nodeMap(bKids)

	recurseChild := func(cid string) wire.Node {
		if bc, ok := baseM[cid]; ok {
			var ac, bb *wire.Node
			if v, ok := aM[cid]; ok {
				ac = &v
			}
			if v, ok := bM[cid]; ok {
				bb = &v
			}
			return merge3(conflicts, res, bc, ac, bb)
		}
		if ac, ok := aM[cid]; ok {
			return ac
		}
		return bM[cid]
	}

	var mergedChildren []wire.Node
	switch {
	case !aStruct && !bStruct:
		for _, i := range baseIDs {
			mergedChildren = append(mergedChildren, recurseChild(i))
		}
	case aStruct && !bStruct:
		for _, i := range aIDs {
			mergedChildren = append(mergedChildren, recurseChild(i))
		}
	case !aStruct && bStruct:
		for _, i := range bIDs {
			mergedChildren = append(mergedChildren, recurseChild(i))
		}
	default:
		baseSet := toSet(baseIDs)
		var aNew, bNew []string
		for _, i := range aIDs {
			if !baseSet[i] {
				aNew = append(aNew, i)
			}
		}
		for _, i := range bIDs {
			if !baseSet[i] {
				bNew = append(bNew, i)
			}
		}
		aNewSet := toSet(aNew)
		overlap := false
		for _, i := range bNew {
			if aNewSet[i] {
				overlap = true
			}
		}
		disjoint := isPureAddition(baseIDs, aIDs) && isPureAddition(baseIDs, bIDs) && !overlap
		if disjoint {
			for _, i := range baseIDs {
				mergedChildren = append(mergedChildren, recurseChild(i))
			}
			newIDs := unionSorted(aNew, bNew) // Ordinal (code-point) tie-break
			for _, i := range newIDs {
				mergedChildren = append(mergedChildren, recurseChild(i))
			}
		} else {
			baseJoin, aJoin, bJoin := strings.Join(baseIDs, ","), strings.Join(aIDs, ","), strings.Join(bIDs, ",")
			pick := recordConflict(conflicts, res, nodeID, "children", &baseJoin, &aJoin, &bJoin)
			chosen := baseIDs
			if pick == 1 {
				chosen = aIDs
			} else if pick == 2 {
				chosen = bIDs
			}
			for _, i := range chosen {
				mergedChildren = append(mergedChildren, recurseChild(i))
			}
		}
	}

	mergedKind := withKindChildren(childlessKind(kindSource.Kind), mergedChildren)
	return mkNode(base, mergedKind, mergedStyle, mergedState, mergedAcc)
}

func pickExtra(base, a, b wire.Node, pick int, key string) wire.Value {
	src := base
	if pick == 1 {
		src = a
	} else if pick == 2 {
		src = b
	}
	return src.Extras[key]
}

func unionSorted(a, b []string) []string {
	set := toSet(a)
	for _, id := range b {
		set[id] = true
	}
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Merge3Way is the author-agnostic facet 3-way merge of a and b over their
// common base (all three share the root id). Returns OK with the merged tree on
// full auto-merge, or OK false with the conflicting cells. Deterministic +
// host-reproducible (NodeId-byte tie-break, no wall-clock) — byte-identical to
// the sibling hosts.
func Merge3Way(base, a, b wire.Node) Result {
	var conflicts []Conflict
	merged := merge3(&conflicts, agnostic, base, &a, &b)
	if len(conflicts) == 0 {
		return Result{OK: true, Tree: merged}
	}
	return Result{OK: false, Conflicts: conflicts}
}

// Merge3WayWithAuthor is the human-primacy 3-way merge (the DAG-layer
// reconciler): a conflicted cell where one branch is Primary and the other
// Secondary is resolved in the primary's favour and recorded with
// PrimacyHeld=true (in Resolved, not blocking); a conflict with no precedence
// keeps base and blocks.
func Merge3WayWithAuthor(authorA, authorB Author, base, a, b wire.Node) Result {
	res := resolveAuthor(authorA, authorB)
	var conflicts []Conflict
	merged := merge3(&conflicts, res, base, &a, &b)
	var blocking []Conflict
	for _, c := range conflicts {
		if !c.PrimacyHeld {
			blocking = append(blocking, c)
		}
	}
	if len(blocking) > 0 {
		return Result{OK: false, Conflicts: blocking}
	}
	return Result{OK: true, Tree: merged, Resolved: conflicts}
}
