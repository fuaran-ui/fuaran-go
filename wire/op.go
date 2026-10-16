package wire

import (
	"strconv"

	"github.com/fuaran-ui/fuaran-go/wire/idl"
)

// The TreeOp codec (WIRE_FORMAT.md §3.4). A TreeOp is, on the wire, a
// top-level "$type"-discriminated object, so a decoded op is a tagged Obj
// whose Tag is the op kind. The decoder validates the discriminator and each
// op's required fields, reusing the node / kind / binding / style / state
// decoders.

var opCases = newCaseSet(idl.OpTags()...)

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

func decodeStyleField(w *walkState, raw any, path string) Value {
	return decodeStyle(w, raw, path)
}

func decodeStyleDeltaField(w *walkState, raw any, path string) Value {
	return decodeStyleDelta(w, raw, path)
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

// opFields is each op's field table, read from the generated idl.json row: the
// fields, their order (which defect surfaces first is therefore fixed) and
// which are required all come from the IDL. Each field is read by the decoder
// its declared type implies, except the three below whose reading carries
// behaviour this host owns. UpdateProp's path is a plain string at codec level
// — the nested-addressing grammar is validated at APPLY time, never at decode.
//
// Populated in init(): Batch recurses through decodeOpValue, which a
// package-level composite literal would report as an initialization cycle.
var opFields map[string][]opField

type opField struct {
	name     string
	required bool
	dec      fieldDecoder
}

// opFieldOverrides are the hand readings, keyed "<op>.<field>".
var opFieldOverrides = map[string]fieldDecoder{
	// The reserved in-process sentinels are refused by name (opJSONValue).
	"UpdateProp.value": opJSONValue,
	// An id list names its offending element by index.
	"ReorderChildren.newOrder": decodeIDList,
}

func init() {
	opFieldOverrides["Batch.ops"] = decodeOpList
	opFields = make(map[string][]opField, len(idl.Ops))
	for _, op := range idl.Ops {
		fields := make([]opField, 0, len(op.Fields))
		for _, f := range op.Fields {
			if f.Optionality == idl.HostOnly {
				continue
			}
			dec, ok := opFieldOverrides[op.Tag+"."+f.Name]
			if !ok {
				dec = typeDecoder(f.Type)
			}
			fields = append(fields, opField{f.Name, f.Optionality == idl.Required, dec})
		}
		opFields[op.Tag] = fields
	}
}

// opRetiredFields — the RETIRED positional slot per op (Phase 687, closing the
// window Phase 681 opened).
//
// Phase 681 removed the field and every host then ACCEPTED AND IGNORED it so
// each could adopt independently. Silence was the whole mechanism: the loop in
// decodeOpValue walks the op's schema and never looks at anything else, so *not
// reading it* was the tolerance. That is why closing the window cannot be done
// by deletion — there was never a read to delete, and the field would go on
// decoding silently forever. The close is an explicit refusal BY NAME, on the
// checkNearMisses pattern and for its reason: a key that no-ops is worse than
// one that fails, because the op decodes, applies, and puts the node somewhere
// other than where the ordinal asked.
var opRetiredFields = map[string]string{
	"InsertChild": "position",
	"MoveNode":    "newPosition",
}

// checkRetiredPositional runs BEFORE the schema loop, mirroring the FormField
// near-miss ordering, so an op carrying both a retired ordinal and some other
// defect names the ordinal rather than reporting a defect the author would fix
// without ever learning the field is gone. The ordering is identical in all
// five hosts, so which defect surfaces first is deterministic.
func checkRetiredPositional(obj map[string]any, tag, path string) {
	name, governed := opRetiredFields[tag]
	if !governed {
		return
	}
	if _, present := obj[name]; present {
		failExpecting(
			CodeWrongType,
			path+"."+name,
			"'"+name+"' was removed from the wire format — "+tag+
				" appends, and order is stated by naming ids with ReorderChildren",
			"a Batch of the structural op followed by ReorderChildren",
		)
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
	checkRetiredPositional(obj, tag, path)
	fields := make(map[string]Value)
	for _, f := range opFields[tag] {
		if raw, ok := obj[f.name]; ok {
			fields[f.name] = f.dec(w, raw, path+"."+f.name)
		} else if f.required {
			fail(CodeMissingField, path+"."+f.name, "missing required field '"+f.name+"'")
		}
	}
	return Obj{Tag: tag, Fields: fields}
}

// DecodeOp decodes a canonical-wire TreeOp document into a tagged Obj whose
// Tag is the op kind (EditNode, UpdateProp, …, Batch). Malformed input returns
// a *DecodeError with one of the six canonical codes and a "$"-rooted path.
func DecodeOp(canonicalJSON string) (op Obj, err error) {
	defer recoverDecode(&err)
	checkDocumentBytes(canonicalJSON) // §21.7, before the parse allocates
	raw := parseJSON(canonicalJSON)
	checkShape(raw)
	return decodeOpValue(newWalkState(), raw, "$"), nil
}

// EncodeOp re-encodes a decoded TreeOp to canonical wire JSON, byte-identical
// to the shared corpus.
func EncodeOp(op Obj) (string, error) {
	return encodeValue(op)
}
