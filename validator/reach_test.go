package validator

import "testing"

// The validator walks every position a node holds another in (the keyed reach
// id uniqueness quantifies over). It used to walk the kind and the state block
// and never the envelope fallback, so a duplicate id held there — which makes
// every later id-addressed op ambiguous — went unreported.
func TestDuplicateIDInTheEnvelopeFallbackIsFlagged(t *testing.T) {
	root := mustDecode(t, `{"id":"root","kind":{"$type":"Box","children":[`+
		`{"id":"dup","kind":{"$type":"Markdown","text":"x"}},`+
		`{"fallback":{"id":"dup","kind":{"$type":"Markdown","text":"f"}},"id":"y","kind":{"$type":"Markdown","text":"y"}}`+
		`],"layout":{"$type":"Flex","direction":"Vertical","wrap":false},"role":"Group"}}`)
	for _, f := range ValidateNode(root) {
		if f.Code == "FUARAN-DUP-ID" && f.Path == "$.kind.children.1.fallback.id" {
			return
		}
	}
	t.Errorf("expected FUARAN-DUP-ID at $.kind.children.1.fallback.id; findings %+v", ValidateNode(root))
}
