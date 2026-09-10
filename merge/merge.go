// Package merge is the deterministic, author-agnostic 3-way tree merge — the Go
// conformant host of the merge the F#/TS/Python hosts run, certified
// byte-for-byte against wire-format-fixtures/merge-conformance/.
//
// A node decomposes into independent facets, each merged on its own: kind (the
// node's own kind-fields, children neutralised), the SemanticStyle sub-fields
// (tone/weight/emphasis/role/voice, merged INDEPENDENTLY so A's tone + B's voice
// auto-blend), state, accessibility, and children (the ordered child-id list).
// When a facet changed on at most one side, that side's value is taken; when both
// changed it to the SAME value that shared value is taken (agreement, not
// conflict); when both changed it differently it is a conflict (returned, not
// silently picked).
//
// The refusal envelope is TWO-SIDED: A and B carry the first- and
// second-argument branches' values on every refusal, so swapping the branches
// transposes them and changes nothing else. Base / Primary / Secondary are the
// precedence view on top — Primary and Secondary are populated exactly when a
// primacy pin is held, because a value in either slot IS a precedence claim.
//
// The structural cases auto-merged across both sides are (a) disjoint pure
// inserts into the same parent, ordered by NodeId code-point (Ordinal) — the
// deterministic, wall-clock-free tie-break — and (b) two sides that reached the
// SAME child-id list, whose shared new children must then also agree on content:
// two branches inserting one id with different content is a refusal naming that
// id, never an arrival-order-dependent pick. Facet equality is canonical-JSON
// bytes, the same oracle the corpus commits to.
package merge

import (
	"sort"
	"strings"

	"github.com/fuaran-ui/fuaran-go/canonical"
	"github.com/fuaran-ui/fuaran-go/wire"
)

// styleDefaults — an absent style ⟺ all of these (WIRE_FORMAT §3.1).
var styleDefaults = map[string]string{"emphasis": "Normal", "tone": "Default", "weight": "Standard"}

// The node-envelope members this merge CONTROLS — each merged on its own axis
// and rebuilt deliberately by mkNode, rather than carried over wholesale.
//
// `tooltip` joined them in Phase 1653. Before that it was a NON-facet extra, so
// two branches setting different hints on one node auto-merged: the hint rode
// through `nonFacetExtras` from whichever node mkNode was handed, silently. It
// is a node-level TRAIT (WIRE_FORMAT.md §3.6) exactly as `accessibility` is,
// and it merges on the same axis, with the same facet-isolation probe.
var facetExtras = map[string]bool{"style": true, "state": true, "accessibility": true, "tooltip": true}

// ── Conflict + result vocabulary ────────────────────────────────────────────

const (
	classConcurrentEdit      = "ConcurrentEdit"
	classReorderVsStructural = "ReorderVsStructural"
	// classDeleteModify — one side EDITED a node the other REMOVED. Neither
	// answer is derivable: keeping the deletion discards an edit nobody
	// retracted, keeping the edit resurrects a node somebody deleted. Until
	// Phase 1653 this host took the deleting side's child list and dropped the
	// edit with the node, silently, on a merge it reported as clean.
	classDeleteModify = "DeleteModify"
	// classConcurrentMove — a node RELOCATED on one side and relocated
	// elsewhere, or edited in place, on the other. Same shape: the merge has
	// two incompatible answers about where the node lives, and the recursive
	// per-parent walk cannot see it, because each parent's own children list
	// merges cleanly in isolation.
	classConcurrentMove = "ConcurrentMove"
	// classUnencodableFacet — a facet whose canonical bytes could not be
	// produced, so the branches cannot be compared at all. Always blocking:
	// primacy resolves a DISAGREEMENT, and this is a failure to establish
	// whether there is one.
	classUnencodableFacet = "UnencodableFacet"

	// Every choice a refusal offers names a POPULATED slot of that refusal.
	// choiceKeepPrimary / choiceKeepSecondary name the precedence view, which is
	// populated exactly when PrimacyHeld is true; choiceKeepA / choiceKeepB name
	// the sides view, populated on every two-sided refusal. An unpinned refusal
	// offering KeepSecondary — which this host did until the reference tier fixed
	// it — names a slot that is nil there, so a resolver applying it has nothing
	// to keep or picks a side itself, which is the argument-order dependence the
	// two-sided envelope removed arriving instead through the resolver.
	choiceKeepPrimary   = "KeepPrimary"
	choiceKeepSecondary = "KeepSecondary"
	choiceKeepBase      = "KeepBase"
	choiceKeepA         = "KeepA"
	choiceKeepB         = "KeepB"
)

// Side is one SIDE of a two-sided refusal: the branch's value for the contended
// cell, plus that branch's own opaque provenance tag.
//
// The tag is per-side because SecondaryTag cannot be: it names the tag of the
// side that lost to a pin, so with no pin held there is no such side, and
// populating it from the A-side branch would make the envelope depend on the
// order the caller passed its branches.
type Side struct {
	Value string
	Tag   *string
}

// Conflict is a conflicting (NodeID, Facet) cell. NodeID + Facet are the minimal
// identity (the author-agnostic surface); the remaining fields carry the
// DAG-layer resolution detail.
//
// Two views of the same refusal, answering different questions:
//
//   - A / B are the SIDES view: the first- and second-argument branches' values
//     for the contended cell, populated on EVERY two-sided refusal whether or
//     not a pin is held. This is what a host needs to show a human what each
//     side wanted, and what a second replica merging the same pair in the
//     opposite order must agree with — swapping the branches TRANSPOSES A and B
//     and changes nothing else.
//   - Base / Primary / Secondary are the PRECEDENCE view: the LCA value, the
//     pinned winner, and the side that lost to it. Primary and Secondary are
//     populated exactly when PrimacyHeld is true — a value in either slot IS a
//     precedence claim, so with two Secondary sides (the Merge3Way shape) both
//     are nil and the values live in A / B alone.
type Conflict struct {
	NodeID        string
	Facet         string
	ConflictClass string
	Base          *string
	A             *Side
	B             *Side
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
	aTag         *string
	bTag         *string
}

// tagOf is the opaque provenance tag a side carries — a Primary side carries
// none (the tag is the Secondary case's payload).
func tagOf(a Author) *string {
	if a.Primary {
		return nil
	}
	return a.Tag
}

// resolveAuthor decides which side wins a conflicted facet under precedence.
//
// secondaryTag names the tag of the side that LOST TO A PIN, so it is nil
// whenever no pin is held. It used to be the A-side branch's tag in the
// two-secondary case, which made it a function of the order the caller passed
// its branches rather than of the merge; each branch's own tag now rides in its
// own side of the two-sided envelope.
func resolveAuthor(a, b Author) resolution {
	aTag, bTag := tagOf(a), tagOf(b)
	switch {
	case a.Primary && !b.Primary:
		return resolution{true, true, []string{choiceKeepPrimary, choiceKeepSecondary, choiceKeepBase}, b.Tag, aTag, bTag}
	case !a.Primary && b.Primary:
		return resolution{false, true, []string{choiceKeepPrimary, choiceKeepSecondary, choiceKeepBase}, a.Tag, aTag, bTag}
	case !a.Primary && !b.Primary:
		// No pin, so Primary / Secondary are both nil and the two values live in
		// A / B alone — the menu is side-addressed rather than precedence-named.
		return resolution{false, false, []string{choiceKeepBase, choiceKeepA, choiceKeepB}, nil, aTag, bTag}
	default:
		// Two primaries — no precedence, host decides. Deliberately NOT widened
		// to KeepA / KeepB even though both sides are populated here too: keeping
		// one side discards the OTHER side's pin, a materially different act from
		// keeping a side where no pin exists, and a menu listing it beside
		// KeepBase would understate it. KeepBase discards both pins symmetrically.
		return resolution{false, false, []string{choiceKeepBase}, nil, aTag, bTag}
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
func mkNode(src wire.Node, kind wire.Obj, style, state, acc, tip wire.Value) wire.Node {
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
	if tip != nil {
		extras["tooltip"] = tip
	}
	return wire.Node{ID: src.ID, Kind: kind, Extras: extras}
}

// encodeFacet canonical-encodes one facet projection, REPORTING failure.
//
// It used to swallow the error and return "", which made every unencodable
// facet compare equal to every other one — so a merge whose two branches could
// not be encoded at all concluded that they agreed, and returned one of them.
// The one case where the comparison is meaningless is exactly the case that
// produced the strongest possible verdict.
func encodeFacet(v wire.Value) (string, error) {
	return wire.EncodeValue(v)
}

// ── facet-isolation canonical probes (closure-safe bytes) ───────────────────

func kindCanonical(n wire.Node) (string, error) {
	return encodeFacet(mkNode(n, childlessKind(n.Kind), nil, nil, nil, nil))
}

func stateCanonical(shell wire.Obj, n wire.Node) (string, error) {
	return encodeFacet(mkNode(n, shell, nil, n.Extras["state"], nil, nil))
}

func accessibilityCanonical(shell wire.Obj, n wire.Node) (string, error) {
	return encodeFacet(mkNode(n, shell, nil, nil, n.Extras["accessibility"], nil))
}

func tooltipCanonical(shell wire.Obj, n wire.Node) (string, error) {
	return encodeFacet(mkNode(n, shell, nil, nil, nil, n.Extras["tooltip"]))
}

// facetTriple encodes one facet across base / a / b, returning the first error.
func facetTriple(baseC, aC, bC string, errs ...error) (string, string, string, error) {
	for _, err := range errs {
		if err != nil {
			return baseC, aC, bC, err
		}
	}
	return baseC, aC, bC, nil
}

// recordUnencodableFacet refuses a facet whose canonical bytes could not be
// produced. It is a CONFLICT and not an agreement: an encode failure means the
// merge cannot tell what the two branches hold, and "cannot tell" is the one
// thing that must never resolve to "the same".
func recordUnencodableFacet(conflicts *[]Conflict, res resolution, nodeID, facet string, err error) {
	detail := "facet could not be canonically encoded: " + err.Error()
	*conflicts = append(*conflicts, Conflict{
		NodeID: nodeID, Facet: facet, ConflictClass: classUnencodableFacet,
		A:            &Side{Value: detail, Tag: res.aTag},
		B:            &Side{Value: detail, Tag: res.bTag},
		SecondaryTag: res.secondaryTag, PrimacyHeld: false, Choices: res.choices,
	})
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

// styleFieldOr reads a style sub-field, MATERIALISING the wire's
// absent-⟺-default rule as the supplied default rather than returning nil.
//
// The five sub-fields above deliberately do not use it: nothing pins what an
// absent base spells in their envelopes, and inventing a spelling for a case no
// fixture reaches would be a guess wearing a fix's clothes. Widen this when a
// fixture says what those five should say.
func styleFieldOr(node wire.Node, name, def string) *string {
	if v := styleField(node, name); v != nil {
		return v
	}
	d := def
	return &d
}

// ── facet pickers ───────────────────────────────────────────────────────────

func strEq(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func recordConflict(conflicts *[]Conflict, res resolution, nodeID, facet string, baseV, aV, bV *string) int {
	conflictClass := classConcurrentEdit
	if facet == "children" {
		conflictClass = classReorderVsStructural
	}
	return recordClassed(conflicts, res, nodeID, facet, conflictClass, baseV, aV, bV)
}

// recordClassed is recordConflict with the class named rather than derived from
// the facet. The structural classes need it: `node` is the facet of both a
// DeleteModify and a ConcurrentMove, so the facet alone cannot say which.
func recordClassed(conflicts *[]Conflict, res resolution, nodeID, facet, conflictClass string, baseV, aV, bV *string) int {
	// The PRECEDENCE view is populated exactly when a pin is held. Before the
	// two-sided envelope, Secondary carried the A-side value in the no-pin case
	// — a precedence claim no pin supported, and one that changed when the
	// caller swapped its branches.
	var primaryV, secondaryV *string
	if res.pinHeld {
		if res.aIsPrimary {
			primaryV, secondaryV = aV, bV
		} else {
			primaryV, secondaryV = bV, aV
		}
	}
	*conflicts = append(*conflicts, Conflict{
		NodeID: nodeID, Facet: facet, ConflictClass: conflictClass,
		Base: baseV,
		// The SIDES view, populated on every two-sided refusal. Each side
		// carries its OWN branch's tag, so swapping the branches transposes
		// the pair and rewrites nothing.
		A:            &Side{Value: deref(aV), Tag: res.aTag},
		B:            &Side{Value: deref(bV), Tag: res.bTag},
		Primary:      primaryV,
		Secondary:    secondaryV,
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
	// `style.direction` is a merge facet like the five above, and it was missing
	// here: two branches declaring opposing directions on one node AUTO-MERGED,
	// silently taking whichever side the field-absence rule happened to favour,
	// where every other style sub-field refuses. A direction states which way a
	// run reads, so silently picking one is the sub-field where a wrong quiet
	// answer is most visible to a reader and least visible to an author.
	//
	// Its values are already the WIRE spellings here (`styleField` reads the
	// decoded style object), so the refusal envelope carries `ltr`/`rtl`/`auto`
	// with nothing to translate — which is what the corpus now pins
	// (`merge-conformance/merge-refusal-concurrent-direction`).
	// Absent ⟺ default on this sub-field, MATERIALISED rather than left nil: a
	// refusal envelope reports the LCA's value, and a node that never declared
	// a direction reads `auto`, not "nothing". The corpus fixture's base pins
	// exactly that (`"base":"auto"` over a node with no style at all).
	direction := pickField(conflicts, res, nodeID, "style.direction",
		styleFieldOr(base, "direction", "auto"),
		styleFieldOr(a, "direction", "auto"),
		styleFieldOr(b, "direction", "auto"))

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
	// Omit-when-default, and the default is the WIRE spelling `auto` — not a
	// case name, unlike every neighbour above. The decoder drops `"auto"` on
	// the way in (wire/decode.go), so re-emitting it here would make the merged
	// tree's canonical bytes differ from the codec's for the same tree.
	if direction != nil && *direction != "auto" {
		fields["direction"] = wire.Str(*direction)
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

// ── whole-tree placement (Phase 1653) ───────────────────────────────────────
//
// Everything else in this file merges one node against its two variants and
// recurses. A MOVE is the one shape that arrangement structurally cannot see:
// the node leaves one parent's children list and joins another's, and BOTH
// lists merge cleanly in isolation — one lost a child, one gained one, each an
// ordinary structural edit on its own. The disagreement only exists when the
// two parents are looked at together, so it is detected once, over the whole
// tree, before the recursion starts.
//
// It also has to run before the delete/modify check inside the recursion,
// because a node that left a parent looks exactly like a node that was
// deleted from it. The index is what tells them apart: gone from this parent
// and present under another is a MOVE; gone from the tree entirely is a
// DELETE.

// placement maps every node id in a tree to its parent's id (the root maps to
// the empty string).
type placement map[string]string

func indexPlacement(n wire.Node) placement {
	out := placement{}
	var walk func(node wire.Node, parent string)
	walk = func(node wire.Node, parent string) {
		out[node.ID] = parent
		for _, child := range childrenOf(node) {
			walk(child, node.ID)
		}
	}
	walk(n, "")
	return out
}

func nodeIndex(n wire.Node) map[string]wire.Node {
	out := map[string]wire.Node{}
	var walk func(node wire.Node)
	walk = func(node wire.Node) {
		out[node.ID] = node
		for _, child := range childrenOf(node) {
			walk(child)
		}
	}
	walk(n)
	return out
}

// moveCtx carries the three placements through the recursion so the
// delete/modify check can ask "did this node move, or did it go?".
type moveCtx struct {
	base, a, b placement
}

func newMoveCtx(base, a, b wire.Node) *moveCtx {
	return &moveCtx{base: indexPlacement(base), a: indexPlacement(a), b: indexPlacement(b)}
}

// movedNotDeleted reports whether a node absent from a parent it had in base is
// still SOMEWHERE in that side's tree.
func (m *moveCtx) movedNotDeleted(side placement, id string) bool {
	if m == nil {
		return false
	}
	_, ok := side[id]
	return ok
}

// detectConcurrentMoves emits the refusals for every node the two branches
// place differently. It runs ONCE over the whole tree, before merge3.
//
// Two cells, and both are needed. The `move` cell names the disagreement about
// WHERE — the base parent and each side's — which is the whole of the class.
// The `node` cell follows only when the two sides also disagree about the
// node's CONTENT, which is the case where accepting either placement would
// additionally discard an edit; a node moved by one side and untouched by the
// other yields the move cell alone.
func detectConcurrentMoves(conflicts *[]Conflict, res resolution, mc *moveCtx, base, a, b wire.Node) {
	aNodes, bNodes, baseNodes := nodeIndex(a), nodeIndex(b), nodeIndex(base)

	ids := make([]string, 0, len(mc.base))
	for id := range mc.base {
		ids = append(ids, id)
	}
	sort.Strings(ids) // deterministic emission; SortCanonical orders the envelope anyway

	for _, id := range ids {
		basePar := mc.base[id]
		aPar, inA := mc.a[id]
		bPar, inB := mc.b[id]
		if !inA || !inB {
			continue // removed on a side — the delete/modify class, not this one
		}
		if aPar == bPar {
			continue // the two branches agree about where it lives
		}

		aC, aErr := encodeFacet(aNodes[id])
		bC, bErr := encodeFacet(bNodes[id])
		baseC, baseErr := encodeFacet(baseNodes[id])
		if aErr != nil || bErr != nil || baseErr != nil {
			err := aErr
			if err == nil {
				err = bErr
			}
			if err == nil {
				err = baseErr
			}
			recordUnencodableFacet(conflicts, res, id, "node", err)
			continue
		}

		// A ONE-SIDED move is an ordinary structural edit and must auto-merge:
		// the side that did not move it did not say anything about where it
		// goes, so taking the move discards nothing. What makes the pair
		// irreconcilable is the OTHER side having something to say about the
		// same node — either it moved it somewhere else, or it edited it where
		// it stood, in which case honouring the move would carry the node away
		// and honouring the edit would leave it behind.
		//
		// This is exactly the distinction the corpus's totality PAIR exists to
		// pin: the refusal has one side moving and the other restyling THE
		// MOVED NODE; the twin has one side moving and the other restyling THE
		// PARENT IT LEFT, which is not a contest. A host refusing whenever the
		// placements differ passes the refusal and fails the twin.
		aMoved, bMoved := aPar != basePar, bPar != basePar
		contested := (aMoved && bMoved) || aC != bC
		if !contested {
			continue
		}

		recordClassed(conflicts, res, id, "move", classConcurrentMove, &basePar, &aPar, &bPar)
		if aC != bC {
			recordClassed(conflicts, res, id, "node", classConcurrentMove, &baseC, &aC, &bC)
		}
	}
}

// merge3 recursively merges a base node against optional a / b variants.
func merge3(conflicts *[]Conflict, res resolution, mc *moveCtx, base wire.Node, aOpt, bOpt *wire.Node) wire.Node {
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
	kindBaseC, kindBaseErr := kindCanonical(base)
	kindAC, kindAErr := kindCanonical(a)
	kindBC, kindBErr := kindCanonical(b)
	kindPick := 0
	if bC, aC, cC, err := facetTriple(kindBaseC, kindAC, kindBC, kindBaseErr, kindAErr, kindBErr); err != nil {
		recordUnencodableFacet(conflicts, res, nodeID, "kind", err)
	} else {
		kindPick = pickCanonical(conflicts, res, nodeID, "kind", bC, aC, cC)
	}
	kindSource := base
	if kindPick == 1 {
		kindSource = a
	} else if kindPick == 2 {
		kindSource = b
	}

	// style sub-fields (independent)
	mergedStyle := mergeStyle(conflicts, res, nodeID, base, a, b)

	// state facet
	stBaseC, stBaseErr := stateCanonical(shell, base)
	stAC, stAErr := stateCanonical(shell, a)
	stBC, stBErr := stateCanonical(shell, b)
	statePick := 0
	if bC, aC, cC, err := facetTriple(stBaseC, stAC, stBC, stBaseErr, stAErr, stBErr); err != nil {
		recordUnencodableFacet(conflicts, res, nodeID, "state", err)
	} else {
		statePick = pickCanonical(conflicts, res, nodeID, "state", bC, aC, cC)
	}
	mergedState := pickExtra(base, a, b, statePick, "state")

	// accessibility facet
	acBaseC, acBaseErr := accessibilityCanonical(shell, base)
	acAC, acAErr := accessibilityCanonical(shell, a)
	acBC, acBErr := accessibilityCanonical(shell, b)
	accPick := 0
	if bC, aC, cC, err := facetTriple(acBaseC, acAC, acBC, acBaseErr, acAErr, acBErr); err != nil {
		recordUnencodableFacet(conflicts, res, nodeID, "accessibility", err)
	} else {
		accPick = pickCanonical(conflicts, res, nodeID, "accessibility", bC, aC, cC)
	}
	mergedAcc := pickExtra(base, a, b, accPick, "accessibility")

	// tooltip facet — the node-level hint TRAIT, on its own axis (Phase 1653)
	tipBaseC, tipBaseErr := tooltipCanonical(shell, base)
	tipAC, tipAErr := tooltipCanonical(shell, a)
	tipBC, tipBErr := tooltipCanonical(shell, b)
	tipPick := 0
	if bC, aC, cC, err := facetTriple(tipBaseC, tipAC, tipBC, tipBaseErr, tipAErr, tipBErr); err != nil {
		recordUnencodableFacet(conflicts, res, nodeID, "tooltip", err)
	} else {
		tipPick = pickCanonical(conflicts, res, nodeID, "tooltip", bC, aC, cC)
	}
	mergedTip := pickExtra(base, a, b, tipPick, "tooltip")

	// children facet (structural)
	baseKids, aKids, bKids := childrenOf(base), childrenOf(a), childrenOf(b)
	baseIDs, aIDs, bIDs := ids(baseKids), ids(aKids), ids(bKids)
	aStruct := !sliceEq(aIDs, baseIDs)
	bStruct := !sliceEq(bIDs, baseIDs)
	baseM, aM, bM := nodeMap(baseKids), nodeMap(aKids), nodeMap(bKids)

	// DELETE/MODIFY, before the structural branch below picks a side.
	//
	// The branch that follows takes whichever side changed the child LIST and
	// recurses into its ids, so a child one side edited and the other removed
	// simply is not visited: the edit disappears with the node and the merge
	// reports clean. Neither answer is derivable from the pair, so the merge
	// must refuse — naming the node, its base content, the surviving side's
	// edit, and the deleting side as the EMPTY value that says "gone".
	for _, cid := range baseIDs {
		bc := baseM[cid]
		ac, inA := aM[cid]
		bb, inB := bM[cid]
		if inA == inB {
			continue // present on both sides, or removed by both — not this class
		}
		// Gone from THIS parent but still in that side's tree is a MOVE, and
		// the whole-tree pre-pass owns it. Without this the same node is
		// reported twice under two classes, and the one a reader would act on
		// is the wrong one.
		if !inA && mc.movedNotDeleted(mc.a, cid) {
			continue
		}
		if !inB && mc.movedNotDeleted(mc.b, cid) {
			continue
		}
		baseC, baseErr := encodeFacet(bc)
		survivor, survivorErr := ac, error(nil)
		if !inA {
			survivor = bb
		}
		survivorC, survivorErr := encodeFacet(survivor)
		if baseErr != nil || survivorErr != nil {
			err := baseErr
			if err == nil {
				err = survivorErr
			}
			recordUnencodableFacet(conflicts, res, cid, "node", err)
			continue
		}
		if survivorC == baseC {
			continue // removed on one side, UNTOUCHED on the other — an ordinary delete
		}
		gone := ""
		aV, bV := &survivorC, &gone
		if !inA {
			aV, bV = &gone, &survivorC
		}
		recordClassed(conflicts, res, cid, "node", classDeleteModify, &baseC, aV, bV)
	}

	recurseChild := func(cid string) wire.Node {
		if bc, ok := baseM[cid]; ok {
			var ac, bb *wire.Node
			if v, ok := aM[cid]; ok {
				ac = &v
			}
			if v, ok := bM[cid]; ok {
				bb = &v
			}
			return merge3(conflicts, res, mc, bc, ac, bb)
		}
		ac, inA := aM[cid]
		bc, inB := bM[cid]
		if inA && inB {
			// BOTH branches introduced this id. There is no base to merge
			// against, so agreement is the only clean outcome: identical
			// content is the shared value, and DIFFERENT content is a refusal
			// naming the id.
			//
			// Taking the A side unconditionally is a silent,
			// arrival-order-dependent pick, and it is the case the
			// disjointness test below used to make unreachable. The
			// shared-children guard reaches it, so the guard and this check
			// land together or the merge trades a spurious refusal for a
			// divergence.
			acC, acErr := encodeFacet(ac)
			bcC, bcErr := encodeFacet(bc)
			if acErr != nil || bcErr != nil {
				// Two branches that cannot be encoded are not two branches
				// that agree. Refuse and keep the A side unmerged; the merge
				// has already failed, so no caller reads this value.
				err := acErr
				if err == nil {
					err = bcErr
				}
				recordUnencodableFacet(conflicts, res, cid, "insert", err)
				return ac
			}
			if acC == bcC {
				return ac
			}
			// The id exists on neither side of the LCA, so it has no base
			// value — the empty string, not an encoding of a node that was
			// never there.
			empty := ""
			recordConflict(conflicts, res, cid, "insert", &empty, &acC, &bcC)
			// The merge has already refused, so this value reaches no caller of
			// Merge3Way — but a lenient caller building a virtual ancestor from
			// it must not get a tree that depends on which branch arrived
			// first. Same doctrine as the insert tie-break: order by canonical
			// bytes.
			if acC <= bcC {
				return ac
			}
			return bc
		}
		if inA {
			return ac
		}
		return bc
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
	case sliceEq(aIDs, bIDs):
		// Both sides changed the children to the SAME id list — agreement, not
		// a conflict, and the guard every other facet already has (pickCanonical's
		// aC != bC). Its absence here is what made a merge of a branch against
		// itself refuse for any branch that touched children at all. The shared
		// ids' CONTENTS are checked by recurseChild, which refuses a
		// same-id-different-content insert rather than defaulting to a side.
		for _, i := range aIDs {
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
	return mkNode(base, mergedKind, mergedStyle, mergedState, mergedAcc, mergedTip)
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

// ── the refusal envelope (the cross-host artefact of a REFUSED merge) ───────

// SortCanonical orders a refusal set deterministically. (NodeID, Facet) is
// unique within one merge — a facet of a node is merged once — so it totally
// orders an envelope regardless of the fold's internal emission order.
func SortCanonical(conflicts []Conflict) []Conflict {
	out := make([]Conflict, len(conflicts))
	copy(out, conflicts)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].NodeID != out[j].NodeID {
			return out[i].NodeID < out[j].NodeID
		}
		return out[i].Facet < out[j].Facet
	})
	return out
}

func encodeSide(s *Side) string {
	if s == nil {
		return "null"
	}
	tag := "null"
	if s.Tag != nil {
		tag = canonical.EscapeString(*s.Tag)
	}
	return `{"tag":` + tag + `,"value":` + canonical.EscapeString(s.Value) + `}`
}

// EncodeEnvelope is the canonical JSON of a REFUSAL envelope: the conflict set
// as a sorted array of {a,b,base,class,facet,nodeId,primacyHeld} objects (object
// keys alphabetical, array entries in (NodeID, Facet) order). Byte-stable across
// hosts, so sha256 over it is the cross-host refusal hash — the determinism
// artefact for a REFUSED structural merge, the analogue of the outcome hash for
// an auto-merge and of the verdict for a gated one.
//
// The precedence view is deliberately projected as primacyHeld alone rather than
// as the Primary / Secondary strings: those are derivable from the sides plus
// the pin, and a corpus that committed both would pin the same value twice and
// go red on a host that agreed about the merge.
func EncodeEnvelope(conflicts []Conflict) string {
	out := "["
	for i, c := range SortCanonical(conflicts) {
		if i > 0 {
			out += ","
		}
		primacy := "false"
		if c.PrimacyHeld {
			primacy = "true"
		}
		out += `{"a":` + encodeSide(c.A) +
			`,"b":` + encodeSide(c.B) +
			`,"base":` + canonical.EscapeString(deref(c.Base)) +
			`,"class":` + canonical.EscapeString(c.ConflictClass) +
			`,"facet":` + canonical.EscapeString(c.Facet) +
			`,"nodeId":` + canonical.EscapeString(c.NodeID) +
			`,"primacyHeld":` + primacy + "}"
	}
	return out + "]"
}

// Merge3Way is the author-agnostic facet 3-way merge of a and b over their
// common base (all three share the root id). Returns OK with the merged tree on
// full auto-merge, or OK false with the conflicting cells. Deterministic +
// host-reproducible (NodeId-byte tie-break, no wall-clock) — byte-identical to
// the sibling hosts.
func Merge3Way(base, a, b wire.Node) Result {
	var conflicts []Conflict
	mc := newMoveCtx(base, a, b)
	detectConcurrentMoves(&conflicts, agnostic, mc, base, a, b)
	merged := merge3(&conflicts, agnostic, mc, base, &a, &b)
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
	mc := newMoveCtx(base, a, b)
	detectConcurrentMoves(&conflicts, res, mc, base, a, b)
	merged := merge3(&conflicts, res, mc, base, &a, &b)
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
