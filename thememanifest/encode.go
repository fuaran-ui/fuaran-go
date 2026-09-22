package thememanifest

import (
	"strings"

	"github.com/fuaran-ui/fuaran-go/wire"
)

// ThemeManifest → JSON, the inverse of the OfJSON / Decode walk in decode.go.
//
// The value tree is a wire.Value and the bytes come from wire.EncodeValue — the
// embedding API's canonical encoder (Ordinal key sort, the §2 rule-5 number
// layout, the rule-6 escapes). Nothing here re-implements canonical rendering,
// which is the whole reason that seam is exported.
//
// Phase 1725 made fuaran-rs the first host to emit a manifest and pinned the
// canonical byte literals in its `tests/manifest.rs` as the portable oracle.
// This encoder is held to those bytes (see encode_test.go); a change to one of
// them is a wire-format change for every host, not a fixture refresh.

// putStr pushes a string member only when it is non-empty — the omit-at-default
// rule for every member the decoder reads through strOr(_, ""), which cannot
// tell an absent key from an empty one.
func putStr(fields map[string]wire.Value, key, value string) {
	if value != "" {
		fields[key] = wire.Str(value)
	}
}

// putNum pushes a numeric member only when it differs from the value numOr
// supplies in its absence.
func putNum(fields map[string]wire.Value, key string, value, def float64) {
	if value != def {
		fields[key] = wire.Float(value)
	}
}

// tokenLeaf builds one DTCG token node. `$value` is the leaf discriminator
// walkTokens stops on, so it is emitted unconditionally — an empty value
// included.
func tokenLeaf(t ManifestToken) wire.Value {
	fields := map[string]wire.Value{}
	putStr(fields, "$type", t.Type)
	fields["$value"] = wire.Str(t.Value)
	if t.Description != nil {
		fields["$description"] = wire.Str(*t.Description)
	}
	if t.Role != nil {
		fields["$extensions"] = wire.Obj{Fields: map[string]wire.Value{
			"fuaran": wire.Obj{Fields: map[string]wire.Value{"role": wire.Str(*t.Role)}},
		}}
	}
	return wire.Obj{Fields: fields}
}

// isLeaf reports whether the decoder's token walk stops at this node.
func isLeaf(v wire.Value) bool {
	o, ok := v.(wire.Obj)
	if !ok {
		return false
	}
	_, has := o.Fields["$value"]
	return has
}

// insertAt places one token at its dotted path, replacing whatever occupies it.
//
// A DTCG path addresses a group OR a token, never both, so two tokens whose
// names collide (equal, or one a strict prefix of the other) are not jointly
// representable. The later write wins — the same precedence dedupeTokens and
// Merge already apply throughout this package — which is why descending past a
// leaf clears it: leaving its `$value` in place would hide every descendant
// from the decoder and make the EARLIER token win instead.
//
// A wire.Obj is a struct holding a map, so the child written into the tree and
// the child descended into share one Fields map; mutating it after the write is
// what builds the nested object.
func insertAt(tree map[string]wire.Value, segments []string, leaf wire.Value) {
	if len(segments) == 0 {
		return
	}
	head, rest := segments[0], segments[1:]
	if len(rest) == 0 {
		tree[head] = leaf
		return
	}
	child, ok := tree[head].(wire.Obj)
	if !ok || isLeaf(child) {
		child = wire.Obj{Fields: map[string]wire.Value{}}
		tree[head] = child
	}
	insertAt(child.Fields, rest, leaf)
}

func tokensTree(tokens []ManifestToken) wire.Value {
	root := map[string]wire.Value{}
	for _, t := range tokens {
		insertAt(root, strings.Split(t.Name, "."), tokenLeaf(t))
	}
	return wire.Obj{Fields: root}
}

// isAnonymousRole reports whether a binding's role is the one value an absent
// `role` member decodes to — NamedRole{""} — and so the one value omitted.
//
// A nil Role is treated as that same default: no decoder or projector in this
// package produces one (parseRoleBinding always sets a role), so it is reachable
// only by hand-building a RoleBinding, and omitting the member is what makes
// such a binding decode back as the anonymous binding it already means.
func isAnonymousRole(r ManifestRole) bool {
	if r == nil {
		return true
	}
	n, ok := r.(NamedRole)
	return ok && n.Name == ""
}

func roleJSON(r ManifestRole) wire.Value {
	switch x := r.(type) {
	case ToneRole:
		return wire.Obj{Fields: map[string]wire.Value{"tone": wire.Str(x.Tone)}}
	case NamedRole:
		return wire.Obj{Fields: map[string]wire.Value{"named": wire.Str(x.Name)}}
	}
	// ManifestRole is closed by its unexported marker method, so this arm is
	// unreachable; the anonymous named role is the value an absent member means.
	return wire.Obj{Fields: map[string]wire.Value{"named": wire.Str("")}}
}

func roleBindingJSON(b RoleBinding) wire.Value {
	// `token` is required — parseRoleBinding drops a binding without it.
	fields := map[string]wire.Value{"token": wire.Str(b.TokenName)}
	if !isAnonymousRole(b.Role) {
		fields["role"] = roleJSON(b.Role)
	}
	return wire.Obj{Fields: fields}
}

func invariantJSON(inv Invariant) wire.Value {
	// `kind` is the discriminator — an unrecognised or absent one drops the
	// invariant at decode, so it is never omitted.
	fields := map[string]wire.Value{"kind": wire.Str(InvariantKindName(inv))}
	switch k := inv.Kind.(type) {
	case ContrastFloor:
		putStr(fields, "role", k.Role)
		putNum(fields, "minRatio", k.MinRatio, 0.0)
	case UsageBudget:
		putStr(fields, "token", k.Token)
		putNum(fields, "targetPct", k.TargetPct, 0.0)
		putNum(fields, "tolerancePct", k.TolerancePct, 0.0)
	case MotionVoice:
		putNum(fields, "maxDurationMs", float64(k.Budget.MaxDurationMs), 0.0)
		if k.Budget.Easing != nil {
			fields["easing"] = wire.Str(*k.Budget.Easing)
		}
	}
	putNum(fields, "weight", inv.Weight, DefaultWeight)
	return wire.Obj{Fields: fields}
}

func metaJSON(meta ManifestMeta) (wire.Value, bool) {
	if meta == AnonymousMeta {
		return nil, false
	}
	fields := map[string]wire.Value{}
	putStr(fields, "name", meta.Name)
	putStr(fields, "version", meta.Version)
	if meta.Description != nil {
		fields["description"] = wire.Str(*meta.Description)
	}
	return wire.Obj{Fields: fields}, true
}

// toJSON builds the wire value for a manifest — the inverse of OfJSON.
//
// Always the Fuaran wrapper shape, never a bare DTCG tree: a top-level `tokens`
// key is what selects the wrapper branch in OfJSON, so omitting it at empty
// would decode the document as vanilla DTCG and silently discard meta, roles
// and invariants.
func toJSON(m ThemeManifest) wire.Value {
	fields := map[string]wire.Value{}
	if meta, ok := metaJSON(m.Meta); ok {
		fields["meta"] = meta
	}
	fields["tokens"] = tokensTree(m.Tokens)
	if len(m.Roles) > 0 {
		items := make(wire.Arr, 0, len(m.Roles))
		for _, b := range m.Roles {
			items = append(items, roleBindingJSON(b))
		}
		fields["roles"] = items
	}
	if len(m.Invariants) > 0 {
		items := make(wire.Arr, 0, len(m.Invariants))
		for _, inv := range m.Invariants {
			items = append(items, invariantJSON(inv))
		}
		fields["invariants"] = items
	}
	return wire.Obj{Fields: fields}
}

// Encode renders a manifest as canonical JSON — the round trip the projectors
// and Merge had no way to emit, so a headless Go service that merged a brand
// override over a base can hand the result back over the wire.
//
// Canonical in the host's one sense (wire.EncodeValue): Ordinal-sorted keys, no
// whitespace, the §2 rule-5 number layout, the rule-6 escapes. Every member the
// decoder tolerates the absence of is omitted at its default, so a projected
// manifest does not carry a page of empty strings.
//
// # The round trip, stated precisely
//
// Encode(Decode(bytes)) == bytes for canonical bytes already in this shape, and
// Decode(Encode(m)) == m for any manifest whose tokens are in the wire's own
// order — every manifest Decode produces is one. A projector or Merge result
// carries tokens in first-appearance order instead, and the wire's order is
// sorted, so the round trip there preserves the token SET and normalises the
// order; the total statement that covers every manifest is that Encode is a
// fixpoint through it — Encode(Decode(Encode(m))) == Encode(m).
//
// # Four model states the wire cannot carry
//
// Recorded rather than hidden because each is reachable only by hand-building a
// ThemeManifest — no decoder or projector in this package produces one. Two
// token names that collide (equal, or one a strict prefix of the other) resolve
// last-write-wins, per insertAt. A name whose first segment starts with `$` is
// emitted but is unreachable to the decoder, which skips `$`-prefixed keys as
// DTCG metadata. A ToneRole holding a string outside the canonical palette
// decodes back as a NamedRole, since parseRole validates the tone. A nil Role
// or a nil Invariant.Kind — Go's zero values, which the Rust host's closed enums
// have no analogue of — decode back as the anonymous named role and as no
// invariant at all. Widening any of these is a wire-format question for every
// host at once, not a change this host makes alone.
func Encode(m ThemeManifest) string {
	s, err := wire.EncodeValue(toJSON(m))
	if err != nil {
		// Unreachable by construction: wire.EncodeValue refuses only a nil
		// Value or a Value implementation it does not know, and toJSON builds
		// nothing but Str, Float, Arr and Obj. Panicking says so loudly; the
		// alternative — returning the empty string — would put a document that
		// is not a manifest onto the wire and call it success.
		panic("thememanifest: canonical encode refused a value this package cannot build: " + err.Error())
	}
	return s
}
