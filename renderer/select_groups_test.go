package renderer

import (
	"strings"
	"testing"
)

// Phase 2571 — a Select's option groups (WIRE_FORMAT.md §3.6.30): one <optgroup>
// per group in list order, a group's options in `source` order, the ungrouped
// remainder after the last group.
func TestSelectOptionGroupsRenderAsOptgroupsWithTheRemainderLast(t *testing.T) {
	node := mustDecode(t, `{"id":"s","kind":{"$type":"Select","groups":[{"label":"Second","values":["d","b"]},{"label":"First","values":["a"]}],"label":"Pick","source":{"$type":"Static","value":[{"label":"A","value":"a"},{"label":"B","value":"b"},{"label":"C","value":"c"},{"label":"D","value":"d"}]},"value":{"$type":"Static","value":"b"}}}`)
	html := renderHTML(t, node, BindingSources{})
	want := `<optgroup label="Second"><option value="b">B</option><option value="d">D</option></optgroup><optgroup label="First"><option value="a">A</option></optgroup><option value="c">C</option></select>`
	if !strings.Contains(html, want) {
		t.Errorf("want %q in:\n%s", want, html)
	}
}

func TestAnUngroupedSelectRendersNoOptgroup(t *testing.T) {
	node := mustDecode(t, `{"id":"s","kind":{"$type":"Select","label":"Pick","source":{"$type":"Static","value":[{"label":"A","value":"a"}]},"value":{"$type":"Static","value":"a"}}}`)
	mustNotContain(t, renderHTML(t, node, BindingSources{}), "optgroup")
}
