package renderer

import (
	"strings"
	"testing"
)

// The fragment registry walks the fragment scope (the reference's: every
// ordered list, every arm, the two state alternatives). It used to read an
// ErrorBoundary's child but not its fallback, never descend into the state
// alternatives or a fragment's own body, so a reference to a fragment declared
// in any of those rendered unresolved.
func TestFragmentDeclaredInEveryScopePositionResolves(t *testing.T) {
	const decl = `{"id":"decl","kind":{"$type":"FragmentDecl","body":{"id":"body-md","kind":{"$type":"Markdown","text":"template body"}},"name":"tpl"}}`
	const ref = `{"id":"use","kind":{"$type":"FragmentRef","name":"tpl"}}`
	const flex = `"layout":{"$type":"Flex","direction":"Vertical","wrap":false},"role":"Group"`
	cases := map[string]string{
		"an ErrorBoundary's fallback": `{"id":"eb","kind":{"$type":"ErrorBoundary","child":{"id":"ok","kind":{"$type":"Markdown","text":"fine"}},"fallback":` + decl + `}}`,
		"a state alternative":         `{"id":"host","kind":{"$type":"Markdown","text":"host"},"state":{"onLoading":` + decl + `}}`,
		"a fragment's own body":       `{"id":"outer","kind":{"$type":"FragmentDecl","body":` + decl + `,"name":"outer"}}`,
	}
	for where, holder := range cases {
		tree := `{"id":"root","kind":{"$type":"Box","children":[` + holder + `,` + ref + `],` + flex + `}}`
		html := renderHTML(t, mustDecode(t, tree), BindingSources{})
		if !strings.Contains(html, "template body") {
			t.Errorf("a fragment declared in %s did not resolve:\n%s", where, html)
		}
	}
}
