package elicitation

import "github.com/fuaran-ui/fuaran-go/wire"

// Outcome is one of the closed four-case §18.3 outcome DU shapes, correlated by
// ElicitationID. Answer (Answered) and By (Superseded) are populated per case.
type Outcome struct {
	Kind          string // Answered | Declined | TimedOut | Superseded
	ElicitationID string
	Answer        map[string]any // Answered only (raw scalars)
	By            *string        // Superseded only, optional
}

// Per-outcome declared key sets (default-deny by shape: an undeclared key on an
// outcome shape is UNDECLARED_FIELD — a Declined cannot smuggle an answer).
var outcomeKeys = map[string]map[string]bool{
	"Answered":   {"$type": true, "answer": true, "elicitationId": true},
	"Declined":   {"$type": true, "elicitationId": true},
	"TimedOut":   {"$type": true, "elicitationId": true},
	"Superseded": {"$type": true, "by": true, "elicitationId": true},
}

// DecodeOutcome decodes an outcome document. Decoding does NOT check contract
// conformance (the outcome does not carry the contract). Returns the typed
// outcome or a *wire.DecodeError.
func DecodeOutcome(text string) (Outcome, error) {
	raw, err := wire.ParseCanonical(text)
	if err != nil {
		return Outcome{}, err
	}
	obj, ok := objOf(raw)
	if !ok {
		return Outcome{}, fail(wire.CodeWrongType, "$", "expected an object at $")
	}
	rawTag, ok := obj["$type"]
	if !ok {
		return Outcome{}, fail(wire.CodeMissingField, "$.$type", "missing $type discriminator")
	}
	tag, ok := rawTag.(string)
	if !ok {
		return Outcome{}, fail(wire.CodeWrongType, "$.$type", "$type must be a string")
	}
	declared, known := outcomeKeys[tag]
	if !known {
		return Outcome{}, fail(wire.CodeUnknownDUCase, "$.$type", "unrecognised outcome '"+tag+"'")
	}
	if e := strictKeys(obj, declared, "$"); e != nil {
		return Outcome{}, e
	}
	elicitationID, e := requireNonEmpty(obj, "elicitationId", "$")
	if e != nil {
		return Outcome{}, e
	}
	out := Outcome{Kind: tag, ElicitationID: elicitationID}
	switch tag {
	case "Answered":
		rawAnswer, ok := obj["answer"]
		if !ok {
			return Outcome{}, fail(wire.CodeMissingField, "$.answer", "Answered outcome missing 'answer'")
		}
		answer, ok := objOf(rawAnswer)
		if !ok {
			return Outcome{}, fail(wire.CodeWrongType, "$.answer", "answer must be an object")
		}
		out.Answer = answer
	case "Superseded":
		if rawBy, ok := obj["by"]; ok {
			by, ok := rawBy.(string)
			if !ok {
				return Outcome{}, fail(wire.CodeWrongType, "$.by", "by must be a string")
			}
			out.By = &by
		}
	}
	return out, nil
}

// EncodeOutcome re-encodes an outcome to canonical wire JSON (byte-exact).
func EncodeOutcome(o Outcome) (string, error) {
	fields := map[string]wire.Value{"elicitationId": wire.Str(o.ElicitationID)}
	switch o.Kind {
	case "Answered":
		fields["answer"] = answerObj(o.Answer)
	case "Superseded":
		if o.By != nil {
			fields["by"] = wire.Str(*o.By)
		}
	}
	return wire.EncodeValue(wire.Obj{Tag: o.Kind, Fields: fields})
}
