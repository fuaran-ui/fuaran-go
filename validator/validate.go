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
			Code: "FUARAN-EMPTY-ID", Path: path + ".id",
			Message: "node id is empty", Severity: SeverityError,
		})
	case seen[node.ID]:
		*findings = append(*findings, Finding{
			Code: "FUARAN-DUP-ID", Path: path + ".id",
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

	checkInertControl(node, node.Kind, kindPath, findings)

	for _, entry := range childNodes(node.Kind, kindPath) {
		walk(entry.node, entry.path, findings, seen)
	}
	if state, ok := node.Extras["state"].(wire.Obj); ok {
		for _, entry := range childNodes(state, path+".state") {
			walk(entry.node, entry.path, findings, seen)
		}
	}
}

// checkSwitch runs the Switch-specific structural checks: a missing selector
// (one of stateKey / on is required — Phase 768 widened the selector to any
// Binding), duplicate match values (dead cases, FUARAN082) and an empty state
// key (FUARAN083).
func checkSwitch(kind wire.Obj, path string, findings *[]Finding) {
	if _, hasKey := kind.Fields["stateKey"]; !hasKey {
		if _, hasOn := kind.Fields["on"]; !hasOn {
			*findings = append(*findings, Finding{
				Code: "MISSING_REQUIRED_FIELD", Path: path + ".stateKey",
				Message:  "kind 'Switch' requires a selector — field 'stateKey' or 'on'",
				Severity: SeverityError,
			})
		}
	}
	if key, ok := kind.Fields["stateKey"].(wire.Str); ok && key == "" {
		*findings = append(*findings, Finding{
			Code: "FUARAN083", Path: path + ".stateKey",
			Message: "switch has an empty stateKey — it can never resolve a case and is stuck on its " +
				"default; name the state key the switch selects on",
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
				Code: "FUARAN082", Path: path + ".cases",
				Message: fmt.Sprintf(
					"switch has two or more cases matching '%s' — first-match-wins makes the later "+
						"case dead; give each case a distinct match value", string(match)),
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

// ── FUARAN069 — the inert-control rule (Phase 426 write-back doctrine) ───────

// writableBindingTags are the binding kinds the write-back default can write TO.
// Everything else — Static, Query, Computed, Transform, Selection — is a read.
var writableBindingTags = map[string]bool{"State": true, "Local": true}

// isWriteBackTarget mirrors the reference host's predicate. `Filter` is writable
// only WITHOUT a default: a defaulted filter is a read of a computed value, not a
// slot the renderer can commit a change to.
func isWriteBackTarget(v wire.Value) bool {
	obj, ok := v.(wire.Obj)
	if !ok {
		return false
	}
	if writableBindingTags[obj.Tag] {
		return true
	}
	if obj.Tag != "Filter" {
		return false
	}
	_, hasDefault := obj.Fields["default"]
	return !hasDefault
}

// inert reports the FUARAN069 condition: no handler AND no writable slot, so
// nothing can carry the interaction.
//
// An omitted handler is the DECLARATIVE shape, not a defect — the write-back
// default is meant to carry it. The defect is omitting the handler *and* pointing
// the value at something unwritable, which leaves a control that looks interactive
// and does nothing.
func inert(kind wire.Obj, handler, slot string) bool {
	if _, hasHandler := kind.Fields[handler]; hasHandler {
		return false
	}
	return !isWriteBackTarget(kind.Fields[slot])
}

// checkInertControl raises FUARAN069 (Warning) for a control that cannot act,
// with a short descriptor naming which one — matching the reference host's sites:
// Tabs, Disclosure, Modal, Select and Form fields.
func checkInertControl(node wire.Node, kind wire.Obj, path string, findings *[]Finding) {
	report := func(control string) {
		*findings = append(*findings, Finding{
			Code: "FUARAN069", Path: path,
			Message: fmt.Sprintf(
				"%s on '%s' has no event handler and no writable value binding — bind its value to "+
					"$state.<key> or $filters.<name>, or supply the handler", control, node.ID),
			Severity: SeverityWarning,
		})
	}

	switch kind.Tag {
	case "Tabs":
		// The tag overlay is a second way to be live: `activeTag` over a populated
		// `tabTags` carries the selection when `activeIndex` does not.
		_, hasSelectTag := kind.Fields["onSelectTag"]
		_, hasTabTags := kind.Fields["tabTags"]
		tagLive := hasSelectTag || (hasTabTags && isWriteBackTarget(kind.Fields["activeTag"]))
		if inert(kind, "onSelect", "activeIndex") && !tagLive {
			report("Tabs")
		}
	case "Disclosure":
		if inert(kind, "onToggle", "open") {
			report("Disclosure")
		}
	case "Modal":
		// Only a DISMISSABLE modal is defective: one that cannot be dismissed by
		// design is not inert, it is modal.
		if dismissable, ok := kind.Fields["dismissable"].(wire.Bool); ok && bool(dismissable) &&
			inert(kind, "onDismiss", "open") {
			report("Modal")
		}
	case "Select":
		if multiple, ok := kind.Fields["multiple"].(wire.Bool); ok && bool(multiple) {
			if inert(kind, "onChangeMulti", "values") {
				report("Select(multiple)")
			}
		} else if inert(kind, "onChange", "value") {
			report("Select")
		}
	case "Form":
		fields, ok := kind.Fields["fields"].(wire.Arr)
		if !ok {
			return
		}
		for _, item := range fields {
			fieldObj, ok := item.(wire.Obj)
			if !ok {
				continue
			}
			fieldKind, ok := fieldObj.Fields["kind"].(wire.Obj)
			if !ok {
				continue
			}
			if inert(fieldKind, "onChange", "value") {
				id := "?"
				if s, ok := fieldObj.Fields["id"].(wire.Str); ok {
					id = string(s)
				}
				report(fmt.Sprintf("FormField(%s)", id))
			}
		}
	}
}
