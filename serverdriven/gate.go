package serverdriven

import (
	"sort"

	"github.com/fuaran-ui/fuaran-go/wire"
)

// The authority gate — parity with the sibling Rust host's, which vets every op
// a handler produces before it can touch the tree.
//
// The structural checks (unknown node, illegitimate event) are the trust
// boundary on what the CLIENT sends. This is the boundary on what the HANDLER
// returns, and the two are not the same question: a handler is host code, but
// it routinely computes ops from client-supplied values, so "the host wrote
// this function" is not the same as "the host intended this particular mount".
// A Mount declares the capabilities its guest needs; the gate decides, per
// mount, whether the host granted them — denying by default anything not
// explicitly granted.
//
// The gate is OPT-IN on this host, which differs from the sibling's
// grant-nothing default, and the difference is deliberate rather than a
// weakening. Go's driver has shipped without a gate, so a zero-valued Session
// that suddenly denied every mount would break working hosts silently at
// runtime rather than at compile time. WithCapabilityGate is the act that turns
// it on; once on, it denies by default exactly as the sibling does.

// CapabilityGate is a host authority context: the set of capability ids the
// host grants. The zero value grants nothing.
type CapabilityGate struct {
	grants map[string]struct{}
}

// NewCapabilityGate builds a gate granting exactly the given capability ids.
func NewCapabilityGate(capabilities ...string) CapabilityGate {
	grants := make(map[string]struct{}, len(capabilities))
	for _, c := range capabilities {
		grants[c] = struct{}{}
	}
	return CapabilityGate{grants: grants}
}

// Grant adds one capability, returning the widened gate (builder-style).
func (g CapabilityGate) Grant(capability string) CapabilityGate {
	grants := make(map[string]struct{}, len(g.grants)+1)
	for c := range g.grants {
		grants[c] = struct{}{}
	}
	grants[capability] = struct{}{}
	return CapabilityGate{grants: grants}
}

// Allows reports whether this exact capability id is granted.
func (g CapabilityGate) Allows(capability string) bool {
	_, ok := g.grants[capability]
	return ok
}

// DecideMount returns the ungranted capabilities a Mount declares — empty when
// every one is granted. Deduped and sorted, so a refusal message is stable
// whatever order the document listed them in.
func (g CapabilityGate) DecideMount(mount wire.Obj) []string {
	return g.missingFrom(mount.Fields["capabilities"])
}

// AuditMounts returns, in document order, each Mount node's id paired with the
// capabilities the gate withholds from it — the "which guests are live, which
// are blocked" view.
func (g CapabilityGate) AuditMounts(tree wire.Node) []MountDecision {
	var out []MountDecision
	walkNodes(tree, func(n wire.Node) {
		if n.Kind.Tag == "Mount" {
			out = append(out, MountDecision{NodeID: n.ID, Missing: g.DecideMount(n.Kind)})
		}
	})
	return out
}

// MountDecision is one Mount's verdict: the node id and the capabilities the
// gate withholds (empty = allowed).
type MountDecision struct {
	NodeID  string
	Missing []string
}

// Allowed reports whether the mount is permitted.
func (d MountDecision) Allowed() bool { return len(d.Missing) == 0 }

// missingFrom reads a capabilities value — an array of strings on the wire —
// and returns the ungranted ids.
//
// A value that is not an array of strings contributes NOTHING rather than
// being treated as "no capabilities declared". The decoder has already refused
// a malformed Mount by the time an op reaches here, so this is a total-function
// tail rather than a check; making it silently permissive would be the one
// reading that turns a decode failure into a grant.
func (g CapabilityGate) missingFrom(value wire.Value) []string {
	arr, ok := value.(wire.Arr)
	if !ok {
		return nil
	}
	seen := make(map[string]struct{}, len(arr))
	var missing []string
	for _, item := range arr {
		id, ok := item.(wire.Str)
		if !ok {
			continue
		}
		if g.Allows(string(id)) {
			continue
		}
		if _, dup := seen[string(id)]; dup {
			continue
		}
		seen[string(id)] = struct{}{}
		missing = append(missing, string(id))
	}
	sort.Strings(missing)
	return missing
}

// opMissingCapabilities collects every capability an op's introduced nodes
// declare and the gate withholds — across InsertChild, ReplaceRoot, and
// recursively through Batch. Ops that introduce no gated surface contribute
// nothing.
func (g CapabilityGate) opMissingCapabilities(op wire.Obj) []string {
	seen := map[string]struct{}{}
	var collect func(value wire.Value)
	collect = func(value wire.Value) {
		if node, ok := asNode(value); ok {
			walkNodes(node, func(n wire.Node) {
				if n.Kind.Tag != "Mount" {
					return
				}
				for _, id := range g.DecideMount(n.Kind) {
					seen[id] = struct{}{}
				}
			})
			return
		}
	}

	var walkOp func(o wire.Obj)
	walkOp = func(o wire.Obj) {
		switch o.Tag {
		case "Batch":
			if inner, ok := o.Fields["ops"].(wire.Arr); ok {
				for _, item := range inner {
					if innerOp, ok := item.(wire.Obj); ok {
						walkOp(innerOp)
					}
				}
			}
		default:
			// The two op positions that INTRODUCE a node, per the op schema
			// (wire/op.go): InsertChild.child and ReplaceRoot.node. The other
			// eight ops address nodes already in the tree, which the gate
			// vetted when they arrived — re-vetting a move or a removal would
			// deny on a grant that was never at issue.
			for _, key := range []string{"child", "node"} {
				if v, ok := o.Fields[key]; ok {
					collect(v)
				}
			}
		}
	}
	walkOp(op)

	missing := make([]string, 0, len(seen))
	for id := range seen {
		missing = append(missing, id)
	}
	sort.Strings(missing)
	return missing
}

// walkNodes visits a node and every sub-node, in document order.
func walkNodes(node wire.Node, visit func(wire.Node)) {
	visit(node)
	for _, child := range subNodes(node) {
		walkNodes(child, visit)
	}
}
