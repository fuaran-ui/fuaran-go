package elicitation

import (
	"sort"

	"github.com/fuaran-ui/fuaran-go/wire"
)

// Field is one answer-contract field (§18.1). All five keys are required and
// strict.
type Field struct {
	Name     string
	NodeID   string
	StateKey string
	Required bool
	Space    Space
}

// Contract is the answer contract: a non-empty ordered field set.
type Contract struct {
	Fields []Field
}

var fieldKeys = map[string]bool{"name": true, "nodeId": true, "required": true, "space": true, "stateKey": true}

func decodeField(raw any, path string) (Field, *wire.DecodeError) {
	obj, ok := objOf(raw)
	if !ok {
		return Field{}, fail(wire.CodeWrongType, path, "expected a field object")
	}
	if e := strictKeys(obj, fieldKeys, path); e != nil {
		return Field{}, e
	}
	name, e := requireNonEmpty(obj, "name", path)
	if e != nil {
		return Field{}, e
	}
	nodeID, e := requireNonEmpty(obj, "nodeId", path)
	if e != nil {
		return Field{}, e
	}
	stateKey, e := requireNonEmpty(obj, "stateKey", path)
	if e != nil {
		return Field{}, e
	}
	req, ok := obj["required"].(bool)
	if !ok {
		return Field{}, fail(wire.CodeWrongType, path+".required", "required must be a boolean")
	}
	rawSpace, ok := obj["space"]
	if !ok {
		return Field{}, fail(wire.CodeMissingField, path+".space", "missing 'space'")
	}
	space, se := decodeSpace(rawSpace, path+".space")
	if se != nil {
		return Field{}, se
	}
	return Field{Name: name, NodeID: nodeID, StateKey: stateKey, Required: req, Space: space}, nil
}

func requireNonEmpty(obj map[string]any, key, path string) (string, *wire.DecodeError) {
	raw, ok := obj[key]
	if !ok {
		return "", fail(wire.CodeMissingField, path+"."+key, "missing '"+key+"'")
	}
	s, ok := raw.(string)
	if !ok || s == "" {
		return "", fail(wire.CodeWrongType, path+"."+key, "'"+key+"' must be a non-empty string")
	}
	return s, nil
}

var contractKeys = map[string]bool{"fields": true}

// decodeContract decodes the contract structure + per-field shape + duplicate
// names. The tree-membership (CONTRACT_UNKNOWN_NODE) check is layered by the
// envelope pipeline (which has the tree); the answer-doc codec skips it.
func decodeContract(raw any, path string) (Contract, *wire.DecodeError) {
	obj, ok := objOf(raw)
	if !ok {
		return Contract{}, fail(wire.CodeWrongType, path, "expected a contract object")
	}
	if e := strictKeys(obj, contractKeys, path); e != nil {
		return Contract{}, e
	}
	rawFields, ok := arrOf(obj["fields"])
	if !ok {
		return Contract{}, fail(wire.CodeWrongType, path+".fields", "contract.fields must be an array")
	}
	if len(rawFields) == 0 {
		return Contract{}, fail(CodeContractEmpty, path+".fields", "the contract declares no fields")
	}
	seen := make(map[string]bool)
	fields := make([]Field, len(rawFields))
	for i, raw := range rawFields {
		fieldPath := path + ".fields[" + itoa(i) + "]"
		f, e := decodeField(raw, fieldPath)
		if e != nil {
			return Contract{}, e
		}
		if seen[f.Name] {
			return Contract{}, fail(CodeContractDuplicate, fieldPath+".name", "duplicate field name '"+f.Name+"'")
		}
		seen[f.Name] = true
		fields[i] = f
	}
	return Contract{Fields: fields}, nil
}

// validateAnswer runs the §18.4 answer validation: undeclared answer keys (in
// Ordinal order), then each contract field in declaration order (missing-
// required, then type-vs-space, then in-space). Returns the first ANSWER_*
// error, or nil. pathPrefix is the answer object's location (e.g. "$.answer").
func validateAnswer(answer map[string]any, contract Contract, pathPrefix string) *wire.DecodeError {
	declared := make(map[string]bool, len(contract.Fields))
	for _, f := range contract.Fields {
		declared[f.Name] = true
	}
	keys := make([]string, 0, len(answer))
	for k := range answer {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !declared[k] {
			return fail(CodeAnswerUndeclaredField, pathPrefix+"."+k, "undeclared answer key '"+k+"'")
		}
	}
	for _, f := range contract.Fields {
		value, present := answer[f.Name]
		if !present {
			if f.Required {
				return fail(CodeAnswerMissingField, pathPrefix+"."+f.Name, "required answer field '"+f.Name+"' is absent")
			}
			continue
		}
		if e := conformsToSpace(value, f.Space, pathPrefix+"."+f.Name); e != nil {
			return e
		}
	}
	return nil
}

var answerDocKeys = map[string]bool{"answer": true, "contract": true}

// DecodeAnswerDoc runs the elicitation-answer conformance document
// {"answer":…,"contract":…}: decode the contract, then validate the answer.
// The document carries no tree, so CONTRACT_UNKNOWN_NODE does not apply.
// Returns nil on acceptance, a *wire.DecodeError on refusal.
func DecodeAnswerDoc(text string) error {
	raw, err := wire.ParseCanonical(text)
	if err != nil {
		return err
	}
	obj, ok := objOf(raw)
	if !ok {
		return fail(wire.CodeWrongType, "$", "expected an object at $")
	}
	if e := strictKeys(obj, answerDocKeys, "$"); e != nil {
		return e
	}
	rawContract, ok := obj["contract"]
	if !ok {
		return fail(wire.CodeMissingField, "$.contract", "missing 'contract'")
	}
	contract, ce := decodeContract(rawContract, "$.contract")
	if ce != nil {
		return ce
	}
	rawAnswer, ok := obj["answer"]
	if !ok {
		return fail(wire.CodeMissingField, "$.answer", "missing 'answer'")
	}
	answer, ok := objOf(rawAnswer)
	if !ok {
		return fail(wire.CodeWrongType, "$.answer", "answer must be an object")
	}
	if e := validateAnswer(answer, contract, "$.answer"); e != nil {
		return e
	}
	return nil
}

// collectNodeIDs returns every node id in a decoded tree (for the contract's
// CONTRACT_UNKNOWN_NODE membership check).
func collectNodeIDs(node wire.Node, into map[string]bool) {
	into[node.ID] = true
	for _, child := range treeChildren(node) {
		collectNodeIDs(child, into)
	}
}

func treeChildren(node wire.Node) []wire.Node {
	var out []wire.Node
	fields := node.Kind.Fields
	if arr, ok := fields["children"].(wire.Arr); ok {
		for _, item := range arr {
			if c, ok := item.(wire.Node); ok {
				out = append(out, c)
			}
		}
	}
	for _, key := range []string{"child", "fallback", "default", "body"} {
		if c, ok := fields[key].(wire.Node); ok {
			out = append(out, c)
		}
	}
	if cases, ok := fields["cases"].(wire.Arr); ok {
		for _, item := range cases {
			if caseObj, ok := item.(wire.Obj); ok {
				if c, ok := caseObj.Fields["child"].(wire.Node); ok {
					out = append(out, c)
				}
			}
		}
	}
	if state, ok := node.Extras["state"].(wire.Obj); ok {
		for _, key := range []string{"onLoading", "onEmpty"} {
			if c, ok := state.Fields[key].(wire.Node); ok {
				out = append(out, c)
			}
		}
	}
	return out
}
