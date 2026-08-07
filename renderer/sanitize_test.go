package renderer

import "testing"

// Render-time sanitisation floor — the URL-scheme and markdown raw-HTML seams.
// These mirror the sibling hosts' payload corpus so the hosts cannot drift on
// safety: a tree vetted on one host must be no more dangerous on another.

func TestSanitizeURLSchemes(t *testing.T) {
	accepted := []string{
		"https://example.com/path?q=1",
		"http://a.b",
		"mailto:a@b.c",
		"tel:+15551234",
		"ftp://f.example/x",
	}
	for _, url := range accepted {
		if got, ok := SanitizeURL(url); !ok || got != url {
			t.Errorf("SanitizeURL(%q) = (%q, %v); want (%q, true)", url, got, ok, url)
		}
	}

	rejected := []string{
		"javascript:alert(1)",
		"JAVASCRIPT:alert(1)",
		"java\tscript:alert(1)",
		"  javascript:alert(1)",
		"vbscript:msgbox",
		"file:///etc/passwd",
		"data:text/html,<script>alert(1)</script>",
		"chrome-extension://abc/page",
	}
	for _, url := range rejected {
		if _, ok := SanitizeURL(url); ok {
			t.Errorf("SanitizeURL(%q) accepted; want rejected", url)
		}
		if got := SanitizeURLOrBlank(url); got != "about:blank" {
			t.Errorf("SanitizeURLOrBlank(%q) = %q; want about:blank", url, got)
		}
	}
}

// A protocol-relative URL carries no scheme, so the schemeless branch would
// admit it — but the browser resolves it against the current page's scheme and
// lands OFF-ORIGIN. '\' is WHATWG's lenient normalisation of '/' for special
// schemes, so all four two-separator forms resolve identically.
func TestSanitizeURLRejectsProtocolRelative(t *testing.T) {
	rejected := []string{
		"//evil.example/x",
		`/\evil.example/x`,
		`\\evil.example/x`,
		`\/evil.example/x`,
		"//",
		"  //evil.example/x", // rejection survives whitespace trimming
	}
	for _, url := range rejected {
		if got, ok := SanitizeURL(url); ok {
			t.Errorf("SanitizeURL(%q) = (%q, true); want rejected", url, got)
		}
		if got := SanitizeURLOrBlank(url); got != "about:blank" {
			t.Errorf("SanitizeURLOrBlank(%q) = %q; want about:blank", url, got)
		}
	}
}

func TestSanitizeURLKeepsSingleSlashRelativePaths(t *testing.T) {
	accepted := []string{"/", "/a", "/foo//bar", "./rel", "page", "#frag", "foo/bar"}
	for _, url := range accepted {
		if got, ok := SanitizeURL(url); !ok || got != url {
			t.Errorf("SanitizeURL(%q) = (%q, %v); want (%q, true)", url, got, ok, url)
		}
	}
	// An absolute URL whose authority legitimately uses "//" is unaffected.
	if got, ok := SanitizeURL("https://ok.example/x"); !ok || got != "https://ok.example/x" {
		t.Errorf("SanitizeURL(https://ok.example/x) = (%q, %v); want accepted", got, ok)
	}
	// The empty string passes through — a valid same-page href.
	if got, ok := SanitizeURL(""); !ok || got != "" {
		t.Errorf(`SanitizeURL("") = (%q, %v); want ("", true)`, got, ok)
	}
}

func TestSanitizeMarkdownHTMLStripsDangerousBlocks(t *testing.T) {
	cases := []struct{ in, want string }{
		{"<p>a</p><script>alert(1)</script><p>b</p>", "<p>a</p><p>b</p>"},
		{`<a href="x" onclick="evil()">t</a>`, `<a href="x">t</a>`},
		{`<a href="javascript:go()">t</a>`, `<a href="about:blankgo()">t</a>`},
		// Prose containing "on<letter>" after whitespace survives (tag-interior anchor).
		{"<p>the only one</p>", "<p>the only one</p>"},
	}
	for _, c := range cases {
		if got := sanitizeMarkdownHTML(c.in); got != c.want {
			t.Errorf("sanitizeMarkdownHTML(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

// The dangerous-element sweep searches a case-folded COPY and slices the
// resulting byte offsets out of the ORIGINAL. strings.ToLower is not byte-length
// preserving (U+0130 is two UTF-8 bytes and lowercases to three), so a single
// such character before the tag used to shift the removal window and leave a
// fragment of the element behind.
func TestSanitizeMarkdownHTMLIsByteAlignedUnderCaseFolding(t *testing.T) {
	cases := []struct{ in, want string }{
		{"İ<script>alert(1)</script>", "İ"},
		{"<p>İİ</p><SCRIPT>x</SCRIPT><p>b</p>", "<p>İİ</p><p>b</p>"},
		{"İ<iframe src='x'></iframe>b", "İb"},
	}
	for _, c := range cases {
		if got := sanitizeMarkdownHTML(c.in); got != c.want {
			t.Errorf("sanitizeMarkdownHTML(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}
