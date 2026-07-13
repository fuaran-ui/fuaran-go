// Package validator is the pre-emit validation surface (default-deny by
// shape). The decoder already rejects malformed WIRE input; this surface
// validates a CONSTRUCTED tree before it is emitted or driven, catching the
// structural defects an author is most likely to introduce — empty node ids,
// duplicate ids, unrecognised node kinds, missing required slots, and
// out-of-domain bounded primitives — and returning structured findings rather
// than panicking. It mirrors the sibling hosts' decoded-tree validator
// surfaces: the shared codes and $-rooted paths agree case-for-case.
package validator

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/fuaran-ui/fuaran-go/wire"
)

// Severity classifies a finding: an Error is a shape defect (default-deny —
// do not emit the tree); a Warning is advisory.
type Severity string

const (
	SeverityError   Severity = "Error"
	SeverityWarning Severity = "Warning"
)

// Finding is a structural validation finding at a $-rooted path.
type Finding struct {
	Code     string
	Path     string
	Message  string
	Severity Severity
}

var (
	knownKinds     map[string]bool
	requiredFields map[string][]string
)

func init() {
	knownKinds = make(map[string]bool)
	for _, k := range wire.KnownNodeKinds() {
		knownKinds[k] = true
	}
	requiredFields = wire.RequiredKindFields()
}

// ValidateNode walks a node tree, returning any structural findings (an empty
// slice means the tree is clean).
func ValidateNode(node wire.Node) []Finding {
	var findings []Finding
	seen := make(map[string]bool)
	walk(node, "$", &findings, seen)
	return findings
}

func walk(node wire.Node, path string, findings *[]Finding, seen map[string]bool) {
	switch {
	case node.ID == "":
		*findings = append(*findings, Finding{
			Code: "EMPTY_NODE_ID", Path: path + ".id",
			Message: "node id is empty", Severity: SeverityError,
		})
	case seen[node.ID]:
		*findings = append(*findings, Finding{
			Code: "DUPLICATE_NODE_ID", Path: path + ".id",
			Message: fmt.Sprintf("duplicate node id '%s'", node.ID), Severity: SeverityError,
		})
	default:
		seen[node.ID] = true
	}

	kindPath := path + ".kind"
	if !knownKinds[node.Kind.Tag] {
		*findings = append(*findings, Finding{
			Code: "UNKNOWN_NODE_KIND", Path: kindPath + ".$type",
			Message: fmt.Sprintf("unrecognised node kind '%s'", node.Kind.Tag), Severity: SeverityError,
		})
	} else if required, ok := requiredFields[node.Kind.Tag]; ok {
		// Required-slot check: a constructed tree missing a wire-required
		// field would fail decode on every conformant host — surface it
		// before emit instead.
		for _, field := range required {
			if _, present := node.Kind.Fields[field]; !present {
				*findings = append(*findings, Finding{
					Code: "MISSING_REQUIRED_FIELD", Path: kindPath + "." + field,
					Message:  fmt.Sprintf("kind '%s' requires field '%s'", node.Kind.Tag, field),
					Severity: SeverityError,
				})
			}
		}
	}

	switch node.Kind.Tag {
	case "Switch":
		checkSwitch(node.Kind, kindPath, findings)
	case "Progress":
		checkProgressFraction(node.Kind, kindPath, findings)
	}

	for _, entry := range childNodes(node.Kind, kindPath) {
		walk(entry.node, entry.path, findings, seen)
	}
	if state, ok := node.Extras["state"].(wire.Obj); ok {
		for _, entry := range childNodes(state, path+".state") {
			walk(entry.node, entry.path, findings, seen)
		}
	}
}

// checkSwitch runs the Switch-specific structural checks: duplicate match
// values (dead cases, FUARAN082) and an empty state key (FUARAN083).
func checkSwitch(kind wire.Obj, path string, findings *[]Finding) {
	if key, ok := kind.Fields["stateKey"].(wire.Str); ok && key == "" {
		*findings = append(*findings, Finding{
			Code: "UNGROUNDED_SWITCH_STATE_KEY", Path: path + ".stateKey",
			Message:  "switch stateKey is empty — it can never resolve a case and is stuck on its default (FUARAN083)",
			Severity: SeverityError,
		})
	}
	cases, ok := kind.Fields["cases"].(wire.Arr)
	if !ok {
		return
	}
	seen := make(map[string]bool)
	reported := make(map[string]bool)
	for _, item := range cases {
		caseObj, ok := item.(wire.Obj)
		if !ok {
			continue
		}
		match, ok := caseObj.Fields["match"].(wire.Str)
		if !ok {
			continue
		}
		if seen[string(match)] && !reported[string(match)] {
			*findings = append(*findings, Finding{
				Code: "DUPLICATE_SWITCH_MATCH", Path: path + ".cases",
				Message:  fmt.Sprintf("duplicate switch match '%s' (FUARAN082)", string(match)),
				Severity: SeverityError,
			})
			reported[string(match)] = true
		}
		seen[string(match)] = true
	}
}

// checkProgressFraction is the bounded-primitive check (FUARAN050, advisory):
// a Progress fraction has the known closed domain [0, 1]; a statically-known
// literal outside it is almost always a unit error (a 0..100 percentage or a
// raw count) the renderer would silently clamp. Only a Static number binding
// carries a checkable value — every other binding case is left alone.
func checkProgressFraction(kind wire.Obj, path string, findings *[]Finding) {
	binding, ok := kind.Fields["fraction"].(wire.Obj)
	if !ok || binding.Tag != "Static" {
		return
	}
	var v float64
	switch t := binding.Fields["value"].(type) {
	case wire.Int:
		v = float64(t)
	case wire.Float:
		v = float64(t)
	default:
		return
	}
	if v < 0 || v > 1 {
		*findings = append(*findings, Finding{
			Code: "FUARAN050", Path: path + ".fraction",
			Message: fmt.Sprintf(
				"Progress fraction literal %g is outside the known-bounded domain [0, 1] — "+
					"use an honest 0..1 value, or indeterminate=true with a caveat", v),
			Severity: SeverityWarning,
		})
	}
}

type childEntry struct {
	node wire.Node
	path string
}

// childNodes finds directly-nested Node values (layout children, an
// ErrorBoundary's child/fallback, Switch cases, state surfaces, …) with their
// $-rooted paths, walking arrays by index and objects by sorted key so the
// reported order is deterministic.
func childNodes(value wire.Value, path string) []childEntry {
	switch t := value.(type) {
	case wire.Node:
		return []childEntry{{t, path}}
	case wire.Arr:
		var out []childEntry
		for i, item := range t {
			out = append(out, childNodes(item, path+"."+strconv.Itoa(i))...)
		}
		return out
	case wire.Obj:
		keys := make([]string, 0, len(t.Fields))
		for k := range t.Fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var out []childEntry
		for _, k := range keys {
			out = append(out, childNodes(t.Fields[k], path+"."+k)...)
		}
		return out
	}
	return nil
}
