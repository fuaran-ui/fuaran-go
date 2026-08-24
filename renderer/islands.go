package renderer

import (
	"strings"

	"github.com/fuaran-ui/fuaran-go/wire"
)

// Islands partial hydration — static-by-default pages with selectively
// hydrated subtrees. A host marks individual subtrees as islands; the server
// emits the page statically, wrapping each island's rendered element in a
// <div class="fuaran-island" data-fuaran-island="…"> boundary (the hydration
// container), and appends one scoped <script type="application/json"> per
// island carrying only that subtree's canonical wire tree. The shipped
// generic client (hydrateIslands) mounts per island only; everything outside
// an island stays inert static HTML, and a page with zero islands emits zero
// hydrate script — byte-identical to RenderHTML.
//
// Mismatch-freedom: the boundary wrapper's children are exactly the island
// node's normal static render, which is byte-identical to what the client
// re-renders into the wrapper — so hydration attaches without a
// reconciliation warning.
//
// The island marker never rides the wire (it is a render-time concern), so a
// Go host declares its islands per render: Islands maps a node id in the tree
// to the island id the boundary + payload carry.

// IslandScriptID is the id of an island's embedded hydrate <script> (distinct
// from a whole-tree payload id, so the two can coexist).
func IslandScriptID(islandID string) string {
	return "fuaran-hydrate-island-" + islandID
}

// escapeForScript escapes wire JSON for safe embedding inside a <script>
// element: < > & become their \uXXXX escapes so a "</script>" substring
// inside string data cannot break out. JSON parsers decode the escapes back,
// so the decoded tree is byte-identical to what the server encoded.
func escapeForScript(json string) string {
	json = strings.ReplaceAll(json, "<", "\\u003c")
	json = strings.ReplaceAll(json, ">", "\\u003e")
	json = strings.ReplaceAll(json, "&", "\\u0026")
	return json
}

// islandChildrenOf enumerates the container children the island collection
// walks (layout children + an ErrorBoundary's child + a FragmentDecl's body —
// the reference walk basis).
func islandChildrenOf(node wire.Node) []wire.Node {
	switch node.Kind.Tag {
	case "Box", "SplitPanel", "Tabs", "Stepper", "SummaryList", "Disclosure":
		return childNodesOf(node.Kind.Fields)
	case "ErrorBoundary":
		if child, ok := asNode(node.Kind.Fields["child"]); ok {
			return []wire.Node{child}
		}
	case "FragmentDecl":
		if body, ok := asNode(node.Kind.Fields["body"]); ok {
			return []wire.Node{body}
		}
	}
	return nil
}

type islandEntry struct {
	id   string
	node wire.Node
}

// collectIslands DFS-collects every marked island in document order.
func collectIslands(node wire.Node, islands map[string]string) []islandEntry {
	var out []islandEntry
	if islandID, ok := islands[node.ID]; ok {
		out = append(out, islandEntry{islandID, node})
	}
	for _, child := range islandChildrenOf(node) {
		out = append(out, collectIslands(child, islands)...)
	}
	return out
}

// islandScript renders the per-island embedded hydrate payload: the island
// subtree's canonical wire JSON in a <script type="application/json"> keyed by
// the island id.
func islandScript(islandID string, node wire.Node) (string, error) {
	json, err := wire.EncodeNode(node)
	if err != nil {
		return "", err
	}
	return `<script type="application/json" id="` + escapeAttr(IslandScriptID(islandID)) +
		`" data-fuaran-island-payload="` + escapeAttr(islandID) + `">` +
		escapeForScript(json) + "</script>", nil
}

// RenderWithIslands renders the page statically with per-island boundary
// markers plus one embedded hydrate <script> per island, appended after the
// static HTML. Islands maps a node id in the tree to its island id; a nil or
// empty map returns exactly RenderHTML(node, sources) — no wrappers, no
// scripts.
//
// The destination policy is the ambient DenyNonLocalEgress, exactly as on the
// static path (WIRE_FORMAT.md §14.1) — the two surfaces must not differ, or a
// host would widen its egress merely by marking an island. A wider posture is
// reached BY NAME through RenderWithIslandsAndEgress.
//
// The embedded hydrate payload is the island subtree's CANONICAL WIRE JSON and
// is deliberately not filtered: it is the tree, not a rendering of it, and the
// client that consumes it is a conformant host applying its own policy at its
// own emission sites. Rewriting a destination inside the payload would hand the
// client a tree that no longer round-trips.
func RenderWithIslands(node wire.Node, sources BindingSources, islands map[string]string) (string, error) {
	return RenderWithIslandsAndEgress(node, sources, islands, DenyNonLocalEgress())
}

// RenderWithIslandsAndEgress is RenderWithIslands with an EXPLICIT destination
// policy — the named opt-out from the ambient default-deny. See
// RenderHTMLWithEgress for the postures a host reaches for and why the opt-out
// is a named entry point rather than a parameter default.
func RenderWithIslandsAndEgress(
	node wire.Node,
	sources BindingSources,
	islands map[string]string,
	policy EgressPolicy,
) (string, error) {
	fragments := make(map[string]wire.Node)
	collectFragments(node, fragments)
	r := &renderer{sources: sources, fragments: fragments, islands: islands, egress: policy}
	staticHTML := r.renderNode(node)

	var scripts strings.Builder
	for _, entry := range collectIslands(node, islands) {
		script, err := islandScript(entry.id, entry.node)
		if err != nil {
			return "", err
		}
		scripts.WriteString(script)
	}
	return staticHTML + scripts.String(), nil
}
