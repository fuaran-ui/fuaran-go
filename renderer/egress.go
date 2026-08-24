package renderer

import "strings"

// Destination policy — typed egress allowlists (WIRE_FORMAT.md §14.1).
//
// sanitize.go answers "is this URL SAFE TO HAVE". It does not answer "is this
// DESTINATION one the composition declared", and only the second question
// closes exfiltration. `https://collector.example/?s=<bound state>` passes
// every check in the floor: the scheme is allowlisted, the host is well-formed,
// there is no script anywhere in it. Put it in an image src and the browser
// contacts it with NO user act at all — rendering IS the request — carrying
// whatever the tree interpolated into the query string.
//
// So the floor gains a second, orthogonal gate: a scheme allowlist says what a
// URL may BE, and an origin allowlist says where it may GO. Both are positive
// lists; neither substitutes for the other, and this one runs after the other
// because there is no point asking where an unsafe URL points.
//
// Two shapes are deliberate and worth stating, because both look like
// omissions:
//
//   - A rule names a HOST, never a scheme and never a path. Scheme is already
//     reduced to the allowlisted set by the floor, and every "scheme wildcard"
//     spelling anyone reaches for (`*://`, `http*://`, `https?://`) parses
//     differently on different hosts — which makes the wildcard itself the
//     vulnerability. Path scoping is likewise refused: a path is not a security
//     boundary, and a policy that appears to bound one invites reliance on a
//     bound it does not have.
//   - The policy is HOST-CONSTRUCTED and never carried on the wire. A policy an
//     emission can supply is a policy a hostile emission can widen, which is
//     not a policy.

// EgressClass is one class of destination a rule can be scoped to. The value IS
// the wire spelling — what a refusal records and what §14.1 names.
type EgressClass string

const (
	// EgressHyperlink is a rendered href the user must ACT on — a link, a
	// markdown anchor, an autolink.
	EgressHyperlink EgressClass = "hyperlink"
	// EgressMedia is a rendered src the browser fetches with NO user act: an
	// image, a stylesheet, a media element. THE exfiltration class — a
	// destination here is contacted merely by rendering the tree, which is why
	// it is scoped separately from EgressHyperlink rather than folded in.
	EgressMedia EgressClass = "media"
	// EgressRoute is a navigation the tree asks for.
	EgressRoute EgressClass = "route"
	// EgressDownload is a file download the tree asks for.
	EgressDownload EgressClass = "download"
	// EgressFileRead is a file READ the tree asks for. It carries no URL of its
	// own, but is scoped here so a policy can speak about it in the same
	// vocabulary.
	EgressFileRead EgressClass = "fileRead"
)

// egressClassesAll is every class, in wire order. Used by AllowOrigin when a
// rule is declared without a class scope (which means "every class").
var egressClassesAll = []EgressClass{
	EgressHyperlink,
	EgressMedia,
	EgressRoute,
	EgressDownload,
	EgressFileRead,
}

// OriginMatch selects how an EgressOrigin's host is compared.
type OriginMatch int

const (
	// OriginExact matches exactly this host. `example.com` matches
	// `example.com` and nothing else — not `a.example.com`, not
	// `notexample.com`.
	OriginExact OriginMatch = iota
	// OriginSuffix matches this host and any subdomain of it. `example.com`
	// matches `example.com` and `a.b.example.com`; it never matches
	// `notexample.com`, because the match requires a label boundary. This is
	// the "registrable suffix" spelling — a suffix, not a substring, and not a
	// wildcard.
	OriginSuffix
)

// EgressOrigin is one allowed destination. Hosts only — no scheme, no port, no
// path.
type EgressOrigin struct {
	Match OriginMatch
	Host  string
}

// ExactHost declares an origin matched exactly.
func ExactHost(host string) EgressOrigin {
	return EgressOrigin{Match: OriginExact, Host: host}
}

// HostSuffix declares an origin matched at a label boundary.
func HostSuffix(suffix string) EgressOrigin {
	return EgressOrigin{Match: OriginSuffix, Host: suffix}
}

// EgressRule is one rule: an origin, and the classes it is declared FOR.
type EgressRule struct {
	Origin EgressOrigin
	// Classes this origin is allowed for. An EMPTY slice allows no class — a
	// rule that names nothing permits nothing, which is the only reading
	// consistent with a positive list. AllowOrigin's variadic reads an omitted
	// class list as "every class"; the two readings are deliberately split
	// across the constructor and the struct, because the struct is data and
	// says exactly what it lists while the helper says what a caller writing
	// one line means.
	Classes []EgressClass
}

// EgressPolicy is a typed egress allowlist.
type EgressPolicy struct {
	Rules []EgressRule
	// AllowAnyOrigin permits EVERY network origin, and Rules is not consulted
	// at all.
	//
	// This is the escape hatch, and it is a FIELD rather than the absence of
	// rules on purpose: an empty allowlist must read as "nothing is declared",
	// never as "everything is fine". Those are opposite postures, and an empty
	// list is what a half-built policy looks like — conflating them would make
	// forgetting to declare anything indistinguishable from deciding not to.
	AllowAnyOrigin bool
	// AllowLocal permits SAME-ORIGIN destinations (a relative path, a fragment,
	// an empty URL). True in both shipped policies: a tree pointing at its own
	// host has not left, and denying it would make ordinary in-app links
	// unrenderable. A host serving several tenants from one origin sets this
	// false and declares what it means.
	AllowLocal bool
	// AllowNonNetwork permits destinations with no network host (`mailto:`,
	// `tel:`). False by default: `mailto:` IS an egress channel — a body
	// parameter carries arbitrary text off the machine — and it has no host for
	// a rule to name, so it cannot be allowlisted, only permitted wholesale.
	AllowNonNetwork bool
}

// DenyNonLocalEgress denies every destination that leaves the origin.
//
// THE DEFAULT FOR A DECODED (WIRE) TREE. An emission cannot declare its own
// egress, so absent a host's declaration it gets none.
//
// A function rather than a package-level value: an EgressPolicy carries a
// slice, so a shared variable would be mutable by any consumer that reached it
// — a shipped default that can be widened in place is not a default.
func DenyNonLocalEgress() EgressPolicy {
	return EgressPolicy{AllowLocal: true}
}

// PermissiveEgress permits every destination.
//
// The posture for a HAND-AUTHORED tree, where the author is the trust boundary.
// Named rather than default so reaching it is a deliberate, greppable act.
func PermissiveEgress() EgressPolicy {
	return EgressPolicy{AllowAnyOrigin: true, AllowLocal: true, AllowNonNetwork: true}
}

// AllowOrigin returns a copy of the policy declaring an origin for a set of
// classes. An OMITTED class list is taken as EVERY class — the ergonomic
// reading of "allow this origin", distinct from an EgressRule whose Classes is
// empty, which permits nothing.
//
// The receiver is copied, slices included: a policy handed out must not be
// widened by a later call that happened to share its backing array.
func (p EgressPolicy) AllowOrigin(origin EgressOrigin, classes ...EgressClass) EgressPolicy {
	scoped := classes
	if len(scoped) == 0 {
		scoped = egressClassesAll
	}
	owned := make([]EgressClass, len(scoped))
	copy(owned, scoped)

	rules := make([]EgressRule, len(p.Rules), len(p.Rules)+1)
	copy(rules, p.Rules)
	p.Rules = append(rules, EgressRule{Origin: origin, Classes: owned})
	return p
}

// DestinationKind is what a URL resolves to, once the scheme floor has accepted
// it.
type DestinationKind int

const (
	// DestinationLocal is same-origin: a relative path, a fragment, an empty
	// URL.
	DestinationLocal DestinationKind = iota
	// DestinationRemote is an absolute network destination at a host —
	// lowercased, with userinfo, port and any trailing root dot removed.
	DestinationRemote
	// DestinationNonNetwork is a scheme with no network host for a rule to name
	// (`mailto:`, `tel:`).
	DestinationNonNetwork
	// DestinationRejected is the scheme floor's refusal, or a network scheme
	// with no extractable host.
	DestinationRejected
)

// Destination is a classified URL: the kind, plus the host or scheme where
// there is one.
type Destination struct {
	Kind   DestinationKind
	Host   string
	Scheme string
}

// networkSchemes are the schemes that reach a host a rule can name. A scheme
// the floor allows but that is absent here (`mailto`, `tel`) is non-network.
var networkSchemes = map[string]bool{"http": true, "https": true, "ftp": true, "sftp": true}

// normalizeHost trims, lowercases, and drops a single trailing root dot
// (`example.com.` and `example.com` are the same host to a resolver, so they
// must be the same host to a policy — otherwise the dotted spelling walks
// straight past an exact rule).
//
// strings.ToLower rather than the byte-wise asciiLower used by the raw-HTML
// sweep: this result is compared against a rule's host, not sliced back out of
// the original, so byte-length preservation buys nothing and an ASCII-only fold
// would leave a non-ASCII host normalising differently here than on the
// sibling hosts.
func normalizeHost(h string) string {
	t := strings.ToLower(strings.TrimSpace(h))
	return strings.TrimSuffix(t, ".")
}

// authorityHost extracts the host from an absolute URL's authority,
// WHATWG-style: '\' counts as '/' when locating the authority, userinfo before
// the LAST '@' is discarded, a port is dropped, and an IPv6 literal keeps its
// brackets.
//
// The LAST '@' is load-bearing rather than fussy:
// `https://good.example@evil.example/x` is a request to `evil.example`, and a
// naive first-'@' split reads it as the opposite — which is the classic
// credential-confusion spelling an allowlist exists to refuse.
func authorityHost(url string) (string, bool) {
	colon := strings.IndexByte(url, ':')
	if colon < 0 {
		return "", false
	}
	i, slashes := colon+1, 0
	for i < len(url) && (url[i] == '/' || url[i] == '\\') {
		slashes++
		i++
	}
	if slashes < 2 {
		return "", false
	}
	start := i
	for i < len(url) && url[i] != '/' && url[i] != '\\' && url[i] != '?' && url[i] != '#' {
		i++
	}
	authority := url[start:i]
	if at := strings.LastIndexByte(authority, '@'); at >= 0 {
		authority = authority[at+1:]
	}
	if authority == "" {
		return "", false
	}
	if strings.HasPrefix(authority, "[") {
		close := strings.IndexByte(authority, ']')
		if close < 0 {
			return "", false
		}
		return strings.ToLower(authority[:close+1]), true
	}
	if port := strings.IndexByte(authority, ':'); port >= 0 {
		authority = authority[:port]
	}
	n := normalizeHost(authority)
	if n == "" {
		return "", false
	}
	return n, true
}

// ClassifyDestination resolves a URL to the destination a policy reasons about.
// Runs the scheme floor FIRST — there is nothing to say about where an unsafe
// URL points.
func ClassifyDestination(url string) Destination {
	safe, ok := SanitizeURL(url)
	if !ok {
		return Destination{Kind: DestinationRejected}
	}
	if safe == "" {
		return Destination{Kind: DestinationLocal}
	}
	scheme, hasScheme := extractScheme(safe)
	if !hasScheme {
		// No scheme reaching here is same-origin: SanitizeURL has already
		// refused every protocol-relative spelling, which is the one schemeless
		// shape that leaves the origin.
		return Destination{Kind: DestinationLocal}
	}
	if !networkSchemes[scheme] {
		return Destination{Kind: DestinationNonNetwork, Scheme: scheme}
	}
	host, ok := authorityHost(safe)
	if !ok {
		return Destination{Kind: DestinationRejected}
	}
	return Destination{Kind: DestinationRemote, Host: host}
}

// originMatches reports whether this rule's origin matches this (already
// normalised) host.
func originMatches(origin EgressOrigin, host string) bool {
	h := normalizeHost(origin.Host)
	if h == "" {
		return false
	}
	if origin.Match == OriginSuffix {
		return host == h || strings.HasSuffix(host, "."+h)
	}
	return host == h
}

// IsDeclaredOrigin reports whether this host is declared for this class by this
// policy.
func (p EgressPolicy) IsDeclaredOrigin(cls EgressClass, host string) bool {
	host = normalizeHost(host)
	if host == "" {
		return false
	}
	if p.AllowAnyOrigin {
		return true
	}
	for _, rule := range p.Rules {
		if !originMatches(rule.Origin, host) {
			continue
		}
		for _, c := range rule.Classes {
			if c == cls {
				return true
			}
		}
	}
	return false
}

// EgressVerdictKind is why a destination was refused, or that it was not.
type EgressVerdictKind int

const (
	// EgressAllowed carries the NORMALISED URL to emit — the same string
	// SanitizeURL would have returned, so an accepting call site needs no
	// second pass.
	EgressAllowed EgressVerdictKind = iota
	// EgressUnsafeURL means the scheme floor rejected it before policy was ever
	// consulted.
	EgressUnsafeURL
	// EgressUndeclaredOrigin is a network destination whose host this policy
	// does not declare for this class.
	EgressUndeclaredOrigin
	// EgressLocalDenied is a same-origin destination under a policy that denies
	// local egress.
	EgressLocalDenied
	// EgressNonNetworkDenied is a hostless scheme under a policy that denies
	// non-network egress.
	EgressNonNetworkDenied
)

// EgressVerdict is the answer for one destination under one policy.
//
// A refusal carries the HOST or the SCHEME and the CLASS — never the path or
// the query, which is exactly where an exfiltrated payload would be sitting. A
// refusal record outlives the session, so one that quoted the query string
// would become the disclosure it exists to prevent.
type EgressVerdict struct {
	Kind EgressVerdictKind
	// URL is the normalised URL to emit; set on EgressAllowed only.
	URL string
	// Host is set on EgressUndeclaredOrigin only.
	Host string
	// Scheme is set on EgressNonNetworkDenied only.
	Scheme string
	// Class is the class asked about; set on every refusal but EgressUnsafeURL.
	Class EgressClass
}

// CheckDestination is the whole check: scheme floor, then destination policy,
// for one class.
func (p EgressPolicy) CheckDestination(cls EgressClass, url string) EgressVerdict {
	allowed := func() EgressVerdict {
		safe, _ := SanitizeURL(url)
		return EgressVerdict{Kind: EgressAllowed, URL: safe}
	}
	switch dest := ClassifyDestination(url); dest.Kind {
	case DestinationRejected:
		return EgressVerdict{Kind: EgressUnsafeURL}
	case DestinationLocal:
		if p.AllowLocal {
			return allowed()
		}
		return EgressVerdict{Kind: EgressLocalDenied, Class: cls}
	case DestinationNonNetwork:
		if p.AllowNonNetwork {
			return allowed()
		}
		return EgressVerdict{Kind: EgressNonNetworkDenied, Scheme: dest.Scheme, Class: cls}
	default:
		if p.IsDeclaredOrigin(cls, dest.Host) {
			return allowed()
		}
		return EgressVerdict{Kind: EgressUndeclaredOrigin, Host: dest.Host, Class: cls}
	}
}

// EgressRefusalURL is the href / src a REFUSED destination renders as.
//
// Deliberately NOT the bare "about:blank" SanitizeURLOrBlank emits: a silent
// neuter is indistinguishable from an authoring mistake, and "nothing happened"
// and "this was refused" are different facts. The fragment is inert in every
// browser and greppable in a rendered document.
const EgressRefusalURL = "about:blank#fuaran-egress-refused"

// EgressRefusalAttribute is the attribute name an emission site attaches beside
// a refused destination.
const EgressRefusalAttribute = "data-fuaran-egress-refused"

// SanitizeURLForEgress is the ONE-CALL RENDER SEAM: the URL to emit, plus the
// attribute pairs that record a refusal in the document itself. An emission
// site adopts the policy by replacing its SanitizeURLOrBlank call with this one
// and splicing the returned pairs onto the element — which is the whole
// adoption, per call site.
//
// A pair is (name, value), so the returned slice is spliceable by any host
// whatever attribute type it builds — the exported analogue of the internal
// attr list this package's own call sites use.
//
// It differs from markdownDestination in ONE verdict, deliberately: the UNSAFE
// case (the scheme floor's own refusal) renders EgressRefusalURL with an
// `unsafe-url` marker here, where the markdown seam keeps the bare
// "about:blank" it has always emitted. The markdown bytes are pinned by a
// cross-host corpus and re-spelling them would churn a shared contract inside a
// change about egress; a node call site is pinned by nothing of the sort, and a
// refused destination that is INVISIBLE in the document is exactly what the
// refusal shape exists to end. The sibling hosts draw the line in the same
// place, so the divergence is between the two seams, not between the hosts.
func (p EgressPolicy) SanitizeURLForEgress(cls EgressClass, url string) (string, [][2]string) {
	verdict := p.CheckDestination(cls, url)
	if verdict.Kind == EgressAllowed {
		return verdict.URL, nil
	}
	name, value, ok := verdict.RefusalMarker()
	if !ok {
		return EgressRefusalURL, nil
	}
	return EgressRefusalURL, [][2]string{{name, value}}
}

// RefusalMarker returns the attribute name and value recording this verdict,
// and false when the destination was allowed. The VALUE names the class and —
// where there is one — the host or the scheme; it never carries the URL.
func (v EgressVerdict) RefusalMarker() (name, value string, ok bool) {
	switch v.Kind {
	case EgressAllowed:
		return "", "", false
	case EgressUnsafeURL:
		return EgressRefusalAttribute, "unsafe-url", true
	case EgressUndeclaredOrigin:
		return EgressRefusalAttribute, string(v.Class) + ":" + v.Host, true
	case EgressLocalDenied:
		return EgressRefusalAttribute, string(v.Class) + ":local", true
	default:
		return EgressRefusalAttribute, string(v.Class) + ":" + v.Scheme, true
	}
}
