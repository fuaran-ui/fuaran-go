package wire

// Node is the decoded UI-node envelope (WIRE_FORMAT.md §3.1): the required
// non-empty ID and "$type"-discriminated Kind, plus the validated optional
// sections ("state" / "style" / "accessibility") in Extras. The wire-omitted
// fields of §9 (motion, extra attributes) have no representation here — a
// conformant host never reads or writes them on the wire.
type Node struct {
	ID     string
	Kind   Obj
	Extras map[string]Value
}

func (Node) isValue() {}

// DecodeNode decodes a canonical-wire Node document. Malformed input returns a
// *DecodeError carrying one of the six canonical codes and a "$"-rooted path —
// a structured, recoverable result, never a panic (WIRE_FORMAT.md §6).
func DecodeNode(canonicalJSON string) (node Node, err error) {
	defer recoverDecode(&err)
	raw := parseJSON(canonicalJSON)
	return decodeNodeValue(raw, "$"), nil
}

// EncodeNode re-encodes a decoded Node to canonical wire JSON, byte-identical
// to the shared corpus (encode(decode(x)) == x for every canonical document x).
func EncodeNode(n Node) (string, error) {
	return encodeValue(n)
}
