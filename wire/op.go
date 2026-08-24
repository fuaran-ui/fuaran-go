package wire

import "strconv"

// The TreeOp codec (WIRE_FORMAT.md §3.4). A TreeOp is, on the wire, a
// top-level "$type"-discriminated object, so a decoded op is a tagged Obj
// whose Tag is the op kind. The decoder validates the discriminator and each
// op's required fields, reusing the node / kind / binding / style / state
// decoders.

var opCases = newCaseSet(
	"EditNode", "UpdateProp", "ReplaceBinding", "UpdateStyle", "UpdateState",
	"InsertChild", "RemoveNode", "MoveNode", "ReorderChildren", "ReplaceRoot", "Batch",
)

// KnownOpKinds returns the sorted set of recognised TreeOp discriminators
// (the conformance exhaustiveness guard keys off it).
func KnownOpKinds() []string {
	return append([]string(nil), opCases.names...)
}

// opJSONValue decodes an UpdateProp value — a structured JSON payload position
// (rule 12: no null), null-strict at the null's exact path. A top-level
// reserved sentinel is the canonical encoder's marker for an in-process-only
// payload that was never wire-representable: replaying it would apply the
// sentinel TEXT as live data, so it rejects by name.
func opJSONValue(w *walkState, raw any, path string) Value {
	if s, ok := raw.(string); ok && (s == opaqueSentinel || s == closureSentinel) {
		failExpecting(
			CodeWrongType,
			path,
			"'"+s+"' is the reserved in-process-only sentinel, not a wire value"+
				" — the op was logged from a payload that cannot replay",
			"a wire-representable JSON value (the sentinels are reserved vocabulary)",
		)
	}
	return fromJSONStrict(w, raw, path)
}

func decodeKindField(w *walkState, raw any, path string) Value {
	return decodeKind(w, raw, path)
}

func decodeNodeField(w *walkState, raw any, path string) Value {
	return decodeNodeValue(w, raw, path)
}

func decodeStyleField(w *walkState, raw any, path string) Value {
	return decodeStyle(w, raw, path)
}

func decodeStateField(w *walkState, raw any, path string) Value {
	return decodeState(w, raw, path)
}

func decodeIDList(w *walkState, raw any, path string) Value {
	arr := expectArray(raw, path)
	out := make(Arr, len(arr))
	for i, item := range arr {
		out[i] = Str(expectString(item, path+"."+strconv.Itoa(i)))
	}
	return out
}

func decodeOpList(w *walkState, raw any, path string) Value {
	arr := expectArray(raw, path)
	out := make(Arr, len(arr))
	for i, item := range arr {
		out[i] = decodeOpValue(w, item, path+"."+strconv.Itoa(i))
	}
	return out
}

// opSchemas drives per-op field validation, walked in declaration order.
// UpdateProp's path is a plain string at codec level — the nested-addressing
// grammar is validated at APPLY time, never at decode time.
//
// Populated in init(): Batch recurses through decodeOpValue, which a
// package-level composite literal would report as an initialization cycle.
var opSchemas map[string][]fieldSpec

func init() {
	opSchemas = map[string][]fieldSpec{
		"EditNode": {
			{"newKind", true, decodeKindField},
			{"target", true, decodeString},
		},
		"UpdateProp": {
			{"path", true, decodeString},
			{"target", true, decodeString},
			{"value", true, opJSONValue},
		},
		"ReplaceBinding": {
			{"binding", true, decodeBinding},
			{"slot", true, decodeString},
			{"target", true, decodeString},
		},
		"UpdateStyle": {
			{"style", true, decodeStyleField},
			{"target", true, decodeString},
		},
		"UpdateState": {
			{"state", true, decodeStateField},
			{"target", true, decodeString},
		},
		"InsertChild": {
			{"child", true, decodeNodeField},
			{"parentId", true, decodeString},
		},
		"RemoveNode": {
			{"target", true, decodeString},
		},
		"MoveNode": {
			{"newParentId", true, decodeString},
			{"target", true, decodeString},
		},
		"ReorderChildren": {
			{"newOrder", true, decodeIDList},
			{"parentId", true, decodeString},
		},
		"ReplaceRoot": {
			{"node", true, decodeNodeField},
		},
		"Batch": {
			{"ops", true, decodeOpList},
		},
	}
}

func decodeOpValue(w *walkState, raw any, path string) Obj {
	// The op axis, counted separately from the node axis and held to the same
	// ceiling — §21.5's note for implementers, since Batch makes this function
	// self-recursive and the syntactic bound only LOOKS like cover for it.
	w.enterOp(path)
	defer w.exitOp()

	obj := expectObject(raw, path)
	tag := dispatch(obj, path, opCases, CodeUnknownDUCase)
	fields := make(map[string]Value)
	for _, fs := range opSchemas[tag] {
		if raw, ok := obj[fs.name]; ok {
			fields[fs.name] = fs.dec(w, raw, path+"."+fs.name)
		} else if fs.required {
			fail(CodeMissingField, path+"."+fs.name, "missing required field '"+fs.name+"'")
		}
	}
	return Obj{Tag: tag, Fields: fields}
}

// DecodeOp decodes a canonical-wire TreeOp document into a tagged Obj whose
// Tag is the op kind (EditNode, UpdateProp, …, Batch). Malformed input returns
// a *DecodeError with one of the six canonical codes and a "$"-rooted path.
func DecodeOp(canonicalJSON string) (op Obj, err error) {
	defer recoverDecode(&err)
	raw := parseJSON(canonicalJSON)
	checkShape(raw)
	return decodeOpValue(newWalkState(), raw, "$"), nil
}

// EncodeOp re-encodes a decoded TreeOp to canonical wire JSON, byte-identical
// to the shared corpus.
func EncodeOp(op Obj) (string, error) {
	return encodeValue(op)
}
