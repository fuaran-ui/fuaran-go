package renderer

import "testing"

// The shared markdown corpus certifies the seam end-to-end; these cover the
// policy's own decision rules directly, so a defect in one of them names itself
// instead of surfacing as a diverged byte in a rendered document.

func TestHostSuffixMatchesAtLabelBoundaryOnly(t *testing.T) {
	policy := DenyNonLocalEgress().AllowOrigin(HostSuffix("docs.example"), EgressHyperlink)
	cases := []struct {
		host string
		want bool
	}{
		{"docs.example", true},
		{"eu.docs.example", true},
		{"a.b.docs.example", true},
		{"notdocs.example", false},
		{"docs.example.evil", false},
		{"xdocs.example", false},
	}
	for _, c := range cases {
		if got := policy.IsDeclaredOrigin(EgressHyperlink, c.host); got != c.want {
			t.Errorf("IsDeclaredOrigin(hyperlink, %q) = %v, want %v — a suffix is matched at a label boundary, never as a substring", c.host, got, c.want)
		}
	}
}

func TestExactHostDoesNotMatchSubdomains(t *testing.T) {
	policy := DenyNonLocalEgress().AllowOrigin(ExactHost("cdn.example"), EgressMedia)
	if !policy.IsDeclaredOrigin(EgressMedia, "cdn.example") {
		t.Error("exact host must match itself")
	}
	for _, host := range []string{"a.cdn.example", "notcdn.example", "cdn.example.evil"} {
		if policy.IsDeclaredOrigin(EgressMedia, host) {
			t.Errorf("exact host must not match %q", host)
		}
	}
}

func TestRulesAreClassScoped(t *testing.T) {
	policy := DenyNonLocalEgress().
		AllowOrigin(ExactHost("cdn.example"), EgressMedia).
		AllowOrigin(HostSuffix("docs.example"), EgressHyperlink)

	if !policy.IsDeclaredOrigin(EgressMedia, "cdn.example") {
		t.Error("cdn.example is declared for media")
	}
	if policy.IsDeclaredOrigin(EgressHyperlink, "cdn.example") {
		t.Error("cdn.example is declared for media only — a hyperlink to it must be refused")
	}
	if !policy.IsDeclaredOrigin(EgressHyperlink, "docs.example") {
		t.Error("docs.example is declared for hyperlink")
	}
	if policy.IsDeclaredOrigin(EgressMedia, "docs.example") {
		t.Error("docs.example is declared for hyperlink only — its image must be refused")
	}
}

// A rule whose class list is empty permits nothing — the only reading
// consistent with a positive list. An OMITTED list on AllowOrigin means every
// class; the two readings are deliberately different.
func TestEmptyRuleClassesPermitNothingButOmittedMeansAll(t *testing.T) {
	explicitlyEmpty := DenyNonLocalEgress()
	explicitlyEmpty.Rules = []EgressRule{{Origin: ExactHost("cdn.example"), Classes: nil}}
	for _, cls := range egressClassesAll {
		if explicitlyEmpty.IsDeclaredOrigin(cls, "cdn.example") {
			t.Errorf("a rule naming no class must permit nothing, but %s passed", cls)
		}
	}

	omitted := DenyNonLocalEgress().AllowOrigin(ExactHost("cdn.example"))
	for _, cls := range egressClassesAll {
		if !omitted.IsDeclaredOrigin(cls, "cdn.example") {
			t.Errorf("AllowOrigin with no class list means every class, but %s was refused", cls)
		}
	}
}

// AllowOrigin returns a copy; a policy already handed out must not be widened
// by a later call that happened to share its backing array.
func TestAllowOriginDoesNotMutateItsReceiver(t *testing.T) {
	base := DenyNonLocalEgress().AllowOrigin(ExactHost("cdn.example"), EgressMedia)
	_ = base.AllowOrigin(ExactHost("evil.example"), EgressMedia)
	if base.IsDeclaredOrigin(EgressMedia, "evil.example") {
		t.Error("AllowOrigin widened the policy it was called on")
	}
	if len(base.Rules) != 1 {
		t.Errorf("base policy grew to %d rules", len(base.Rules))
	}
}

// `https://good.example@evil.example/x` is a request to evil.example. A naive
// first-'@' split reads it as the opposite — the classic credential-confusion
// spelling an allowlist exists to refuse.
func TestAuthorityHostDiscardsUserinfoBeforeTheLastAt(t *testing.T) {
	cases := []struct{ url, want string }{
		{"https://evil.example/x", "evil.example"},
		{"https://good.example@evil.example/x", "evil.example"},
		{"https://a@b@evil.example/x", "evil.example"},
		{"https://user:pw@evil.example:8443/x", "evil.example"},
		{"https://evil.example:8443/x", "evil.example"},
		{"https://EVIL.Example./x", "evil.example"},
		{"https://evil.example", "evil.example"},
		{"https://evil.example?q=1", "evil.example"},
		{"https://evil.example#f", "evil.example"},
		// '\' counts as '/' when locating and ending the authority, as the
		// WHATWG parser does for special schemes.
		{`https:\\evil.example\x`, "evil.example"},
		{`https://evil.example\x`, "evil.example"},
		// An IPv6 literal keeps its brackets; the port is still dropped.
		{"https://[2001:db8::1]:8443/x", "[2001:db8::1]"},
	}
	for _, c := range cases {
		got, ok := authorityHost(c.url)
		if !ok || got != c.want {
			t.Errorf("authorityHost(%q) = (%q, %v), want (%q, true)", c.url, got, ok, c.want)
		}
	}
	for _, url := range []string{"https:/evil.example/x", "mailto:person@example.com", "/relative/path", "https://@/x"} {
		if got, ok := authorityHost(url); ok {
			t.Errorf("authorityHost(%q) = (%q, true), want no host", url, got)
		}
	}
}

// `example.com.` and `example.com` are the same host to a resolver, so they
// must be the same host to a policy — otherwise the dotted spelling walks
// straight past an exact rule.
func TestTrailingRootDotNormalises(t *testing.T) {
	policy := DenyNonLocalEgress().AllowOrigin(ExactHost("cdn.example."), EgressMedia)
	for _, host := range []string{"cdn.example", "cdn.example.", "CDN.Example.", "  cdn.example  "} {
		if !policy.IsDeclaredOrigin(EgressMedia, host) {
			t.Errorf("host %q should normalise onto the declared cdn.example", host)
		}
	}
	// Only ONE trailing dot is dropped, and the drop is not a general
	// dot-stripping: two dots is a different (malformed) host.
	if policy.IsDeclaredOrigin(EgressMedia, "cdn.example..") {
		t.Error(`"cdn.example.." must not normalise onto "cdn.example"`)
	}
	v := DenyNonLocalEgress().CheckDestination(EgressHyperlink, "https://Collector.Example./x?s=1")
	if v.Kind != EgressUndeclaredOrigin || v.Host != "collector.example" {
		t.Errorf("refusal recorded host %q (kind %v), want the normalised collector.example", v.Host, v.Kind)
	}
}

func TestCheckDestinationVerdicts(t *testing.T) {
	deny := DenyNonLocalEgress()
	permissive := PermissiveEgress()

	// The scheme floor runs FIRST — there is nothing to say about where an
	// unsafe URL points, and its answer is the same under every policy.
	for _, p := range []EgressPolicy{deny, permissive} {
		if v := p.CheckDestination(EgressHyperlink, "javascript:alert(1)"); v.Kind != EgressUnsafeURL {
			t.Errorf("javascript: must be EgressUnsafeURL, got %v", v.Kind)
		}
	}

	// Same-origin is permitted by both shipped policies: a tree pointing at its
	// own host has not left.
	if v := deny.CheckDestination(EgressHyperlink, "/guide#top"); v.Kind != EgressAllowed || v.URL != "/guide#top" {
		t.Errorf("same-origin link = (%v, %q), want allowed /guide#top", v.Kind, v.URL)
	}

	// mailto: has no host for a rule to name, so it can only be permitted
	// wholesale — and the refusal records the SCHEME.
	v := deny.CheckDestination(EgressHyperlink, "mailto:hello@collector.example")
	if v.Kind != EgressNonNetworkDenied || v.Scheme != "mailto" {
		t.Errorf("mailto under deny = (%v, %q), want NonNetworkDenied mailto", v.Kind, v.Scheme)
	}
	if got := permissive.CheckDestination(EgressHyperlink, "mailto:hello@collector.example"); got.Kind != EgressAllowed {
		t.Errorf("mailto under permissive = %v, want allowed", got.Kind)
	}

	// AllowLocal false is the several-tenants-on-one-origin posture.
	noLocal := EgressPolicy{}
	if got := noLocal.CheckDestination(EgressMedia, "/p.png"); got.Kind != EgressLocalDenied || got.Class != EgressMedia {
		t.Errorf("local under a policy denying local = (%v, %v), want LocalDenied media", got.Kind, got.Class)
	}
}

// A refusal names the class and the host or scheme — never the path or the
// query, which is where an exfiltrated payload would be sitting.
func TestRefusalMarkerCarriesNoPathOrQuery(t *testing.T) {
	cases := []struct {
		verdict EgressVerdict
		want    string
	}{
		{DenyNonLocalEgress().CheckDestination(EgressHyperlink, "https://collector.example/x?s=secret"), "hyperlink:collector.example"},
		{DenyNonLocalEgress().CheckDestination(EgressMedia, "https://collector.example/p.png?who=me"), "media:collector.example"},
		{DenyNonLocalEgress().CheckDestination(EgressHyperlink, "mailto:hello@collector.example?body=secret"), "hyperlink:mailto"},
		{EgressPolicy{}.CheckDestination(EgressMedia, "/p.png?who=me"), "media:local"},
		{DenyNonLocalEgress().CheckDestination(EgressHyperlink, "javascript:alert(1)"), "unsafe-url"},
	}
	for _, c := range cases {
		name, value, ok := c.verdict.RefusalMarker()
		if !ok {
			t.Fatalf("verdict %v should carry a refusal marker", c.verdict.Kind)
		}
		if name != EgressRefusalAttribute {
			t.Errorf("marker name = %q, want %q", name, EgressRefusalAttribute)
		}
		if value != c.want {
			t.Errorf("marker value = %q, want %q", value, c.want)
		}
	}
	if _, _, ok := PermissiveEgress().CheckDestination(EgressHyperlink, "https://collector.example/x").RefusalMarker(); ok {
		t.Error("an allowed destination carries no marker")
	}
}

func TestClassifyDestination(t *testing.T) {
	cases := []struct {
		url    string
		kind   DestinationKind
		detail string
	}{
		{"", DestinationLocal, ""},
		{"/guide", DestinationLocal, ""},
		{"#top", DestinationLocal, ""},
		{"https://cdn.example/p.png", DestinationRemote, "cdn.example"},
		{"ftp://files.example/x", DestinationRemote, "files.example"},
		{"mailto:a@b.example", DestinationNonNetwork, "mailto"},
		{"tel:+441234", DestinationNonNetwork, "tel"},
		{"javascript:alert(1)", DestinationRejected, ""},
		{"//evil.example/x", DestinationRejected, ""},
		// A network scheme with no extractable host is rejected, not local.
		{"https:/evil.example/x", DestinationRejected, ""},
	}
	for _, c := range cases {
		got := ClassifyDestination(c.url)
		detail := got.Host
		if got.Kind == DestinationNonNetwork {
			detail = got.Scheme
		}
		if got.Kind != c.kind || detail != c.detail {
			t.Errorf("ClassifyDestination(%q) = (%v, %q), want (%v, %q)", c.url, got.Kind, detail, c.kind, c.detail)
		}
	}
}

// The pure function is the permissive case, and the seam is threaded rather
// than global — so two policies render the same source concurrently without
// either seeing the other's.
func TestMarkdownToHTMLIsThePermissiveCase(t *testing.T) {
	const source = "[the report](https://collector.example/x?s=secret)"
	permissive := MarkdownToHTMLWithEgress(PermissiveEgress(), source)
	if pure := MarkdownToHTML(source); pure != permissive {
		t.Errorf("MarkdownToHTML diverged from the permissive render:\n got %q\nwant %q", pure, permissive)
	}
	if denied := MarkdownToHTMLWithEgress(DenyNonLocalEgress(), source); denied == permissive {
		t.Error("the denying policy rendered the same bytes as the permissive one")
	}
	// And the permissive render is unchanged afterwards — no state carried.
	if again := MarkdownToHTML(source); again != permissive {
		t.Error("a denying render left state behind that changed the next permissive one")
	}
}
