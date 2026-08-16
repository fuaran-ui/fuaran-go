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

// §19 rule 1 — the WHATWG basic URL parser's own pre-parse normalisation.
//
// Control characters are written as escapes throughout: a raw C0 byte in source is
// invisible in review and does not survive a copy-paste, which is the wrong property
// for the payloads a security pin is made of.
func TestSanitizeURLNormalisesAsTheURLParserDoes(t *testing.T) {
	// V1 — an interior TAB / LF / CR BETWEEN the two slash-ish characters. Before
	// rule 1 normalised, "/<TAB>/host/x" had first two characters '/' and TAB, so
	// isProtocolRelative read an ordinary relative reference and accepted, while the
	// browser removed the tab by the URL Standard's step 2 and resolved "//host/x"
	// OFF-ORIGIN. Verified against the WHATWG parser: all twelve spellings below
	// resolve to https://evil.example/x.
	for _, c := range []string{"\t", "\n", "\r"} {
		for _, a := range []string{"/", `\`} {
			for _, b := range []string{"/", `\`} {
				url := a + c + b + "evil.example/x"
				if got, ok := SanitizeURL(url); ok {
					t.Errorf("V1 SanitizeURL(%q) = (%q, true); want rejected", url, got)
				}
			}
		}
	}
	if got, ok := SanitizeURL("/\t\r/\nevil.example/x"); ok {
		t.Errorf("V1 interleaved SanitizeURL = (%q, true); want rejected", got)
	}

	// V2 — a LEADING C0 control that is not whitespace. No native trim removes
	// U+0001 or NUL, so the two slashes sat at positions 1 and 2 and
	// isProtocolRelative never saw them; the parser removes them by step 1 and
	// resolves off-origin.
	for _, c := range []string{"\x01", "\x00", "\x1f"} {
		url := c + "//evil.example/x"
		if got, ok := SanitizeURL(url); ok {
			t.Errorf("V2 SanitizeURL(%q) = (%q, true); want rejected", url, got)
		}
	}

	// Step 1 is the whole C0-or-space range, at both ends. And rule 1's output is
	// what gets RETURNED — an accepted URL loses its interior tab.
	if got, ok := SanitizeURL("https://good.example/x\x01"); !ok || got != "https://good.example/x" {
		t.Errorf("trailing C0: got (%q, %v); want the trimmed URL", got, ok)
	}
	if got, ok := SanitizeURL("https://good.ex\tample/x"); !ok || got != "https://good.example/x" {
		t.Errorf("interior tab: got (%q, %v); want the tab removed from the emitted value", got, ok)
	}

	// U+000B and U+000C are removed at the EDGES by step 1 and KEPT in the interior
	// — the parser treats "/<VT>/host/x" as a same-origin path, and so must the
	// floor. Pinned because widening step 2 to "all C0" would over-reject here.
	for _, c := range []string{"\x0b", "\x0c"} {
		url := "/" + c + "/evil.example/x"
		if got, ok := SanitizeURL(url); !ok || got != url {
			t.Errorf("interior %q: got (%q, %v); want it kept as a same-origin path", c, got, ok)
		}
	}

	// ASCII-exact LOOSENS these, correctly: the parser keeps them and resolves an
	// ordinary same-origin path, where strings.TrimSpace removed them and the floor
	// then saw "//" and rejected. U+0085 is where JS diverged from Go, .NET, Python
	// and Rust; ASCII-exact ends the divergence in both directions.
	for _, c := range []string{"\u00a0", "\u0085"} {
		url := c + "//evil.example/x"
		if got, ok := SanitizeURL(url); !ok || got != url {
			t.Errorf("leading %q: got (%q, %v); want it kept (parser keeps it too)", c, got, ok)
		}
	}

	// Rule 2 is UNCHANGED and still stricter than the browser, which is why V1 and
	// V2 are off-origin navigation rather than script execution.
	for _, url := range []string{"java\tscript:alert(1)", "java\x0bscript:alert(1)"} {
		if got, ok := SanitizeURL(url); ok {
			t.Errorf("SanitizeURL(%q) = (%q, true); want rejected", url, got)
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
