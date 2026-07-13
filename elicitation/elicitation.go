package elicitation

import "github.com/fuaran-ui/fuaran-go/wire"

// Elicitation is a decoded §18 envelope: the question tree + the answer
// contract + optional default/timeout. Round-trip is byte-exact.
type Elicitation struct {
	ID        string
	Tree      wire.Node
	Contract  Contract
	Default   map[string]any // the raw answer object (name→scalar), or nil
	TimeoutMs *int64
}

var envelopeKeys = map[string]bool{
	"$elicitation": true, "contract": true, "default": true, "id": true, "timeoutMs": true, "tree": true,
}

// DecodeElicitation runs the §18.4 decode + validation pipeline, failing fast
// with one structured error. Returns the typed envelope on acceptance.
func DecodeElicitation(text string) (Elicitation, error) {
	raw, err := wire.ParseCanonical(text)
	if err != nil {
		return Elicitation{}, err
	}
	obj, ok := objOf(raw)
	if !ok {
		return Elicitation{}, fail(wire.CodeWrongType, "$", "expected an object at $")
	}
	// 2 — undeclared envelope keys.
	if e := strictKeys(obj, envelopeKeys, "$"); e != nil {
		return Elicitation{}, e
	}
	// 3 — version tag.
	rawVer, ok := obj["$elicitation"]
	if !ok {
		return Elicitation{}, fail(wire.CodeMissingField, "$.$elicitation", "missing '$elicitation' format tag")
	}
	ver, ok := rawVer.(string)
	if !ok {
		return Elicitation{}, fail(wire.CodeWrongType, "$.$elicitation", "$elicitation must be a string")
	}
	if ver != FormatVersion {
		return Elicitation{}, fail(CodeUnsupportedVersion, "$.$elicitation", "unsupported elicitation version '"+ver+"'")
	}
	// 4 — id.
	id, e := requireNonEmpty(obj, "id", "$")
	if e != nil {
		return Elicitation{}, e
	}
	// 5 — tree.
	rawTree, ok := obj["tree"]
	if !ok {
		return Elicitation{}, fail(wire.CodeMissingField, "$.tree", "missing 'tree'")
	}
	tree, terr := wire.DecodeNodeValue(rawTree)
	if terr != nil {
		return Elicitation{}, rerootUnder(terr, "$.tree")
	}
	// 6 — contract (structure/shape/duplicate, then tree membership).
	rawContract, ok := obj["contract"]
	if !ok {
		return Elicitation{}, fail(wire.CodeMissingField, "$.contract", "missing 'contract'")
	}
	contract, ce := decodeContract(rawContract, "$.contract")
	if ce != nil {
		return Elicitation{}, ce
	}
	ids := make(map[string]bool)
	collectNodeIDs(tree, ids)
	for i, f := range contract.Fields {
		if !ids[f.NodeID] {
			return Elicitation{}, fail(CodeContractUnknownNode,
				"$.contract.fields["+itoa(i)+"].nodeId", "field nodeId '"+f.NodeID+"' names no node in the tree")
		}
	}
	// 7 — timeoutMs.
	var timeoutMs *int64
	if rawTimeout, ok := obj["timeoutMs"]; ok {
		f, ok := numOf(rawTimeout)
		if !ok || f < 1 || f != float64(int64(f)) {
			return Elicitation{}, fail(wire.CodeWrongType, "$.timeoutMs", "timeoutMs must be an integer >= 1")
		}
		v := int64(f)
		timeoutMs = &v
	}
	// 8 — default (conformance → DEFAULT_NONCONFORMANT).
	var def map[string]any
	if rawDefault, ok := obj["default"]; ok {
		d, ok := objOf(rawDefault)
		if !ok {
			return Elicitation{}, fail(CodeDefaultNonconformant, "$.default", "default must be an answer object")
		}
		if ve := validateAnswer(d, contract, "$.default"); ve != nil {
			return Elicitation{}, fail(CodeDefaultNonconformant, ve.Path, ve.Message)
		}
		def = d
	}

	return Elicitation{ID: id, Tree: tree, Contract: contract, Default: def, TimeoutMs: timeoutMs}, nil
}

func rerootUnder(err error, prefix string) error {
	de, ok := err.(*wire.DecodeError)
	if !ok {
		return err
	}
	return &wire.DecodeError{Code: de.Code, Path: prefix + de.Path[1:], Message: de.Message, ExpectedShape: de.ExpectedShape}
}

// EncodeElicitation re-encodes an envelope to canonical wire JSON (byte-exact
// round-trip). Keys sort Ordinal: $elicitation < contract < default < id <
// timeoutMs < tree.
func EncodeElicitation(e Elicitation) (string, error) {
	fieldObjs := make(wire.Arr, len(e.Contract.Fields))
	for i, f := range e.Contract.Fields {
		fieldObjs[i] = wire.Obj{Fields: map[string]wire.Value{
			"name":     wire.Str(f.Name),
			"nodeId":   wire.Str(f.NodeID),
			"required": wire.Bool(f.Required),
			"space":    encodeSpace(f.Space),
			"stateKey": wire.Str(f.StateKey),
		}}
	}
	fields := map[string]wire.Value{
		"$elicitation": wire.Str(FormatVersion),
		"contract":     wire.Obj{Fields: map[string]wire.Value{"fields": fieldObjs}},
		"id":           wire.Str(e.ID),
		"tree":         e.Tree,
	}
	if e.Default != nil {
		fields["default"] = answerObj(e.Default)
	}
	if e.TimeoutMs != nil {
		fields["timeoutMs"] = wire.Int(*e.TimeoutMs)
	}
	return wire.EncodeValue(wire.Obj{Fields: fields})
}

// answerObj converts a raw answer object (name→scalar) to a canonical wire
// Value, preserving each scalar's int/float/string form.
func answerObj(answer map[string]any) wire.Value {
	fields := make(map[string]wire.Value, len(answer))
	for k, v := range answer {
		fields[k] = wire.ValueFromParsed(v)
	}
	return wire.Obj{Fields: fields}
}
