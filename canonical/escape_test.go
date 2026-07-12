package canonical

import "testing"

func TestEscapeString(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", "\"\""},
		{"plain", "\"plain\""},
		{"quote \" and backslash \\", "\"quote \\\" and backslash \\\\\""},
		// Control characters escape as lower-case four-digit \u00xx.
		{"tab\tnewline\ncr\rnul\x00", "\"tab\\u0009newline\\u000acr\\u000dnul\\u0000\""},
		// '/' and non-ASCII pass through literally (rule 6).
		{"a/b — héllo €", "\"a/b — héllo €\""},
	}
	for _, c := range cases {
		if got := EscapeString(c.in); got != c.want {
			t.Errorf("EscapeString(%q) = %s, want %s", c.in, got, c.want)
		}
	}
}
