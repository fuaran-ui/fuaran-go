package wire

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// itoa keeps the limit messages readable without pulling fmt into this file.
func itoa(n int) string { return strconv.Itoa(n) }

// Decode-side resource limits for untrusted wire input (WIRE_FORMAT.md §21).
//
// WHY THIS EXISTS, and why it reads differently here than on the other hosts.
// §6 promises decoding is total: a malformed or hostile input yields a
// structured, typed error, never a crash. On TypeScript and Python that promise
// was outright false on shape — both drove their engines off the stack and threw
// an error outside the declared contract. Go did not: goroutine stacks grow, and
// encoding/json applies its own syntactic nesting cap before the walk gets deep,
// so measured behaviour was a refusal rather than a fatal error.
//
// What was wrong here was CONFORMANCE, in two specific ways, both measured:
//
//  1. Go accepted documents §21.2 rule 1 requires every host to REFUSE. A node
//     tree decoded happily at 1 000 levels, and was refused only around 4 000,
//     against a wire limit of 24. A tree vetted on this host would not decode on
//     another, which is the whole failure mode conformance limits exist to stop.
//
//  2. When it did refuse, it reported INVALID_JSON — which rule 2 explicitly
//     forbids for this. The input is well-formed and merely too large to walk,
//     and calling it malformed sends an author to repair the wrong thing.
//
// ── Why the state is threaded rather than package-level ─────────────────────
//
// The sibling hosts count depth in module-level counters, which is sound there
// because decoding is synchronous and both runtimes are single-threaded per
// call. It is NOT sound here. DecodeNode is a public API of a headless backend
// tier, so concurrent decode from multiple goroutines is the expected usage, and
// package-level counters would be a plain data race — one the race detector
// would flag and, worse, one that would silently mis-bound a decode under load
// rather than fail loudly.
//
// So walkState is created per call and threaded through the decoder. That is why
// fieldDecoder and the kind builders carry a *walkState they mostly ignore: the
// cost of the parameter is paid once, at compile time, in exchange for the
// guarantee that two concurrent decodes cannot see each other's counters.
//
// ── The figures ────────────────────────────────────────────────────────────
//
// These are protocol limits, not tuning knobs. Changing one is a protocol change
// — it moves in WIRE_FORMAT.md §21 and across every host, never here alone.
//
// The two depth numbers are separate because neither derives from the other: one
// tree level costs several JSON levels (a Box costs three — the node object, its
// children array, the child object), and a rule-12 structured payload nests
// freely WITHIN one node and consumes no node depth at all. A host must never
// report a node-depth breach as a syntax-depth breach.
//
// §21.4 records how MaxNodeDepth was derived on the reference host, by bisecting
// each walk's true overflow depth. It is not re-derived per host: it is a number
// in the format. A host that measures a tighter budget on some walk of its own
// bounds that walk under §21.2 rule 5 rather than proposing a smaller limit.
const (
	// MaxNodeDepth bounds NODE nesting (the root is depth 1). The same figure
	// bounds Batch nesting in the op decoder — a separate axis, counted on its
	// own, held to the same ceiling.
	MaxNodeDepth = 24

	// MaxJSONDepth bounds SYNTACTIC nesting: every { and [ counts, whether it
	// carries a node, a spec, or a rule-12 payload.
	MaxJSONDepth = 256

	// MaxStringLength bounds a single decoded JSON string, in Unicode CODE
	// POINTS (§21.6). Not in bytes, which is what Go's len() gives and what this
	// host counted until §21.6 pinned the unit: a 600 000-character CJK string is
	// 600 000 code points and 1 800 000 UTF-8 bytes, so a byte-counting host
	// refuses a document three other hosts accept. The unit has to be a property
	// of the text rather than of a host's string representation, and bytes make
	// the allowance depend on the alphabet the author writes in.
	MaxStringLength = 1048576

	// MaxArrayLength bounds a single JSON array's elements and a single JSON
	// object's members.
	MaxArrayLength = 100000

	// MaxNodes bounds the total node count of one document.
	//
	// Needed even once depth is bounded, because the depth, string and array
	// limits together still admit a document that is hostile by being WIDE — 24
	// levels of 100 000 siblings is within every other limit. Its cost is linear
	// in the input, but the constant is not: a decoded tree is far larger in
	// memory than the bytes that produced it.
	MaxNodes = 100000

	// MaxExprNodes bounds the ColExpr nodes in ONE Binding.Expr expression
	// (WIRE_FORMAT.md §21.8, Phase 1534). Counted per expression, not per
	// document: a tree may carry many Expr bindings, each bounded here, with the
	// whole still bounded by MaxDocumentBytes. A breach is LIMIT_EXCEEDED at the
	// path of the `expr` member.
	//
	// ONE count and not a count plus a depth: depth <= node count for every
	// expression, so an expression 600 deep is already 600 nodes and already
	// refused, and a second number would be one more figure to keep in step
	// across the hosts while refusing nothing this one does not.
	//
	// Its SCOPE is Binding.Expr and nothing else. A ColExpr inside a
	// Binding.Transform pipeline is NOT bounded by it, and was not bounded before
	// it either — stated rather than left to be inferred, because a limit whose
	// scope is guessed at is worse than no limit.
	MaxExprNodes = 512

	// MaxSkeletonRows bounds the value of ONE Skeleton node's `rows` slot
	// (WIRE_FORMAT.md §21.9, Phase 1666). Counted per node, not per document: a
	// tree may carry many Skeleton nodes, each bounded here, with the whole
	// still bounded by MaxNodes and MaxDocumentBytes. A breach is
	// LIMIT_EXCEEDED at the path of the `rows` member.
	//
	// It is the first bound here that a document breaches with four digits
	// rather than with bulk, and §21.8's argument applies more sharply because
	// this is not even an evaluation — the rows are simply not present in the
	// input. A server-side renderer emits one row of markup per count, so
	// {"$type":"Skeleton","rows":100000000} is a document well inside every
	// other limit (a handful of bytes, one node, three JSON levels) that names
	// a hundred million rendered rows. Every structural limit is satisfied, and
	// each is satisfied because none of them is looking at the value.
	//
	// §7.1 decides FIRST, and the ORDER is what keeps the two rules apart. §7.1
	// says what a typed integer slot can HOLD, and 2147483647 is finite,
	// fraction-free and inside signed 32-bit, so §7.1 admits it; this bound
	// then refuses it for the work it names. A non-integer therefore stays
	// WRONG_TYPE and a 32-bit-valid value past the bound is LIMIT_EXCEEDED —
	// never the reverse. Collapsing the two into a narrower integer read would
	// also refuse the at-the-bound document §21.2 rule 1 obliges every host to
	// accept.
	//
	// An UPPER bound only. A negative `rows` is not a resource breach — nothing
	// expands — and reporting one as LIMIT_EXCEEDED would be the
	// actively-wrong diagnosis rule 2 forbids. It is an authoring defect and
	// belongs to the pre-emit validator family (FUARAN150), which this package
	// does not implement.
	MaxSkeletonRows = 10000

	// MaxDocumentBytes bounds the UTF-8 byte length of one whole input
	// document (WIRE_FORMAT.md §21.7, adopted here in Phase 1653 — the
	// comment above already named it as the bound that catches what
	// MaxExprNodes does not, which it could not, because until now the
	// constant did not exist).
	//
	// It is the only §21 limit that bounds a document's TOTAL rather than the
	// shape of its walk, and it is needed because the five structural limits
	// compose MULTIPLICATIVELY: 100 000 array elements each holding a
	// 1 048 576-code-point string satisfies every one of them and is a hundred
	// gigabytes. Each individual check refuses nothing, because each
	// individual check is satisfied.
	//
	// BYTES, not code points — the one place a §21 unit differs from §21.6's,
	// deliberately. §21.6 bounds a VALUE the author wrote, so it is measured
	// in units of text; this bounds the CARRIAGE, which is what an attacker
	// sends and what a host allocates. A Go string is already UTF-8 bytes, so
	// len() IS the unit here and there is nothing to convert.
	//
	// Constrained from BELOW by MaxNodes: a document at exactly 100 000 nodes
	// is about 8 MB of small nodes, so an 8 MiB ceiling — which looks generous
	// beside a 1 MiB string bound — would refuse a document rule 1 requires
	// every host to ACCEPT, quietly lowering the node ceiling while leaving
	// its stated value in the table. 32 MiB leaves about 335 bytes per node
	// there.
	MaxDocumentBytes = 33554432
)

// walkState carries one decode call's §21 counters. Created per call, threaded
// through the decoder, never shared — see the concurrency note above.
//
// The node and op axes are counted SEPARATELY. §21.5's note for implementers is
// explicit that bounding the node decoder is not sufficient: Batch makes the op
// decoder self-recursive on its own axis, and the syntactic bound only LOOKS
// like adequate cover for it. On the reference host, 2.6 KB of nested Batches
// killed the process with every node-side guard already in place.
// The TREE-ITEM axis is the THIRD such axis, added by Phase 1120 for the same
// reason the op axis exists. A whole `Tree` hierarchy lives inside ONE node, so
// the node axis cannot see it at all — `enterNode` is never reached for a
// `TreeItem`, which is a record and not a `Node`. And at roughly two JSON levels
// per row the syntactic bound (256) is nowhere near reached by a hierarchy the
// node bound would already have refused. Counted separately, held to the same
// MaxNodeDepth ceiling, on the `TreeOp.Batch` precedent.
type walkState struct {
	nodeDepth int
	nodes     int
	opDepth   int
	itemDepth int
}

func newWalkState() *walkState { return &walkState{} }

// enterNode is called on the way DOWN, before the recursion that would breach
// the bound (§21.2 rule 4) — never afterwards from the tree that was built. A
// check that runs after the walk it is meant to bound has already paid the cost
// it exists to refuse.
func (w *walkState) enterNode(path string) {
	if w.nodeDepth >= MaxNodeDepth {
		failExpecting(
			CodeLimitExceeded,
			path,
			"node nesting deeper than the wire limit MaxNodeDepth = "+itoa(MaxNodeDepth),
			"a tree nesting nodes no more than "+itoa(MaxNodeDepth)+" levels deep",
		)
	}
	w.nodes++
	if w.nodes > MaxNodes {
		failExpecting(
			CodeLimitExceeded,
			path,
			"the document holds more than the wire limit MaxNodes = "+itoa(MaxNodes)+" nodes",
			"a tree of no more than "+itoa(MaxNodes)+" nodes in total",
		)
	}
	w.nodeDepth++
}

func (w *walkState) exitNode() { w.nodeDepth-- }

func (w *walkState) enterOp(path string) {
	if w.opDepth >= MaxNodeDepth {
		failExpecting(
			CodeLimitExceeded,
			path,
			"op nesting deeper than the wire limit MaxNodeDepth = "+itoa(MaxNodeDepth),
			"a Batch nesting ops no more than "+itoa(MaxNodeDepth)+" levels deep",
		)
	}
	w.opDepth++
}

func (w *walkState) exitOp() { w.opDepth-- }

// enterItem bounds `TreeItem` nesting (§21.5), on the way DOWN like every other
// axis. The path passed is the OFFENDING item's own path, so a document names
// the row at fault rather than the tree that holds it.
func (w *walkState) enterItem(path string) {
	if w.itemDepth >= MaxNodeDepth {
		failExpecting(
			CodeLimitExceeded,
			path,
			"tree-item nesting deeper than the wire limit MaxNodeDepth = "+itoa(MaxNodeDepth),
			"a tree nesting items no more than "+itoa(MaxNodeDepth)+" levels deep",
		)
	}
	w.itemDepth++
}

func (w *walkState) exitItem() { w.itemDepth-- }

// checkShape bounds syntactic depth, string length and array/object width over
// the already-parsed document.
//
// ITERATIVE over an explicit stack, deliberately: a recursive checker would be
// the very bug it is checking for, and would blow up on exactly the input it
// exists to refuse.
//
// The honest limit of this arrangement, stated rather than glossed: encoding/json
// has already materialised the document before this runs, so these three bounds
// are not "on the way down" in the sense rule 4 means for a hand-rolled parser.
// Two things make that acceptable rather than a hole. encoding/json's own
// nesting cap already bounds what it will build, so the unbounded case is closed
// upstream; and §21.1 is explicit that these limits bound STRUCTURE and not total
// payload size, with the transport-level byte cap remaining the host's own. The
// bounds that actually protect the recursive walk — node depth and node count —
// are enforced on the way down, in the decoder.
func checkShape(root any) {
	type frame struct {
		value any
		depth int
	}
	stack := []frame{{root, 1}}

	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		if f.depth > MaxJSONDepth {
			failExpecting(
				CodeLimitExceeded,
				"$",
				"JSON nesting deeper than the wire limit MaxJSONDepth = "+itoa(MaxJSONDepth),
				"a document nesting no more than "+itoa(MaxJSONDepth)+" levels deep",
			)
		}

		switch v := f.value.(type) {
		case string:
			if utf8.RuneCountInString(v) > MaxStringLength {
				failExpecting(
					CodeLimitExceeded,
					"$",
					"a string is longer than the wire limit MaxStringLength = "+itoa(MaxStringLength),
					"strings of no more than "+itoa(MaxStringLength)+" code points",
				)
			}
		case map[string]any:
			if len(v) > MaxArrayLength {
				failExpecting(
					CodeLimitExceeded,
					"$",
					"an object has more members than the wire limit MaxArrayLength = "+itoa(MaxArrayLength),
					"objects of no more than "+itoa(MaxArrayLength)+" members",
				)
			}
			for key, item := range v {
				// Keys are strings on the wire and are bounded like any other.
				if utf8.RuneCountInString(key) > MaxStringLength {
					failExpecting(
						CodeLimitExceeded,
						"$",
						"a key is longer than the wire limit MaxStringLength = "+itoa(MaxStringLength),
						"keys of no more than "+itoa(MaxStringLength)+" code points",
					)
				}
				stack = append(stack, frame{item, f.depth + 1})
			}
		case []any:
			if len(v) > MaxArrayLength {
				failExpecting(
					CodeLimitExceeded,
					"$",
					"an array is longer than the wire limit MaxArrayLength = "+itoa(MaxArrayLength),
					"arrays of no more than "+itoa(MaxArrayLength)+" elements",
				)
			}
			for _, item := range v {
				stack = append(stack, frame{item, f.depth + 1})
			}
		}
	}
}

// checkTextDepth bounds SYNTACTIC nesting by scanning the raw text, before the
// document is parsed at all.
//
// This exists because ordering turned out to matter more than it looks.
// encoding/json applies its OWN nesting cap and reports the breach as a syntax
// error, so a document deep enough to trip the standard library was refused as
// INVALID_JSON before any check of ours could see it — rule 2's exact
// prohibition, reached by accident rather than by choice. Scanning the text
// first means the §21 limit is what refuses a too-deep document, with the code
// §6 defines, whatever the standard library would have done with it.
//
// It is also the only bound on this host that is genuinely "on the way down" in
// rule 4's sense: nothing has been allocated when it fires.
//
// The SURROGATE rule (§20.2 row 6) is enforced here for that reason and one
// more. encoding/json lowers an unpaired \uD800-\uDBFF or \uDC00-\uDFFF escape
// to U+FFFD — silently, with no error anywhere — so the same bytes meant one
// thing to this host and another to a host that kept the code unit. That is one
// of the two rows that change what a document MEANS rather than whether it is
// accepted, which is why it cannot be left to the parser's discretion. And it
// cannot be checked afterwards either: once the string is assembled, a
// well-formed pair and two lone halves are indistinguishable, and on this host
// both have already become U+FFFD. So it is checked on the escape TEXT, where a
// high half must be followed IMMEDIATELY by a low half.
//
// A raw (unescaped) surrogate is refused too. It cannot occur in valid UTF-8,
// but a Go string is a byte sequence, so the WTF-8 encoding of one — ED A0..BF —
// would otherwise ride through this host as opaque bytes.
//
// String-aware, because a brace inside a string literal is not nesting. Escapes
// are skipped so a `\"` inside a string does not read as the closing quote.
func checkTextDepth(text string) {
	depth := 0
	inString := false
	escaped := false

	for i := 0; i < len(text); i++ {
		c := text[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				if isUnicodeEscape(text, i) {
					code := hexQuad(text, i+2)
					switch {
					case code >= 0xD800 && code <= 0xDBFF:
						if !isLowSurrogateEscapeAt(text, i+6) {
							failUnpairedSurrogate("HIGH", code,
								`a \uD800-\uDBFF escape must be followed immediately by a \uDC00-\uDFFF escape`)
						}
						i += 11 // the whole pair, minus the loop's own increment
						continue
					case code >= 0xDC00 && code <= 0xDFFF:
						// A low half is only ever consumed above, as the second
						// element of a pair — reaching it here means it is alone.
						failUnpairedSurrogate("LOW", code,
							`a \uDC00-\uDFFF escape must be preceded immediately by a \uD800-\uDBFF escape`)
					}
					i += 5
					continue
				}
				escaped = true
			case c == '"':
				inString = false
			case c == 0xED && i+2 < len(text) && text[i+1] >= 0xA0 && text[i+1] <= 0xBF:
				failUnpairedSurrogate("RAW",
					rune(0xD000|(int(text[i+1]&0x3F)<<6)|int(text[i+2]&0x3F)),
					"a surrogate code unit cannot appear in a UTF-8 document")
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{', '[':
			depth++
			if depth > MaxJSONDepth {
				failExpecting(
					CodeLimitExceeded,
					"$",
					"JSON nesting deeper than the wire limit MaxJSONDepth = "+itoa(MaxJSONDepth),
					"a document nesting no more than "+itoa(MaxJSONDepth)+" levels deep",
				)
			}
		case '}', ']':
			depth--
		}
	}
}

// isUnicodeEscape reports whether text[i] begins a well-formed \uXXXX escape.
// A malformed one is left to encoding/json, which is the authority on syntax.
func isUnicodeEscape(text string, i int) bool {
	if i+5 >= len(text) || text[i+1] != 'u' {
		return false
	}
	for j := i + 2; j < i+6; j++ {
		if !isHexDigit(text[j]) {
			return false
		}
	}
	return true
}

func isHexDigit(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// hexQuad reads the four hex digits at text[i:i+4]. Only called once
// isUnicodeEscape has confirmed they are there.
func hexQuad(text string, i int) rune {
	var v rune
	for j := i; j < i+4; j++ {
		c := text[j]
		switch {
		case c >= '0' && c <= '9':
			v = v<<4 | rune(c-'0')
		case c >= 'a' && c <= 'f':
			v = v<<4 | rune(c-'a'+10)
		default:
			v = v<<4 | rune(c-'A'+10)
		}
	}
	return v
}

// isLowSurrogateEscapeAt reports whether a \uDC00-\uDFFF escape begins at
// text[i] — the ONLY position at which a high half's partner may sit, since
// "immediately" is what tells a pair from two halves that happen to co-occur.
func isLowSurrogateEscapeAt(text string, i int) bool {
	if i >= len(text) || text[i] != '\\' || !isUnicodeEscape(text, i) {
		return false
	}
	code := hexQuad(text, i+2)
	return code >= 0xDC00 && code <= 0xDFFF
}

func failUnpairedSurrogate(half string, code rune, why string) {
	failExpecting(
		CodeInvalidJSON,
		"$",
		"an unpaired "+half+" surrogate U+"+strings.ToUpper(strconv.FormatInt(int64(code), 16))+
			" (WIRE_FORMAT.md §20.2 row 6): "+why,
		"every surrogate half paired, so the document names Unicode scalar values only",
	)
}

// checkDocumentBytes enforces §21.7, BEFORE parsing.
//
// One comparison on the input's length: a host that defers it has chosen to
// allocate the document twice for no benefit. The path is "$" — the breach is
// a property of the document, not of a position inside it, and there is no
// position to name because nothing has been parsed.
//
// The vector is HOST-LOCAL and deliberately not a corpus fixture: committing
// 32 MiB of padding to a shared repository to assert one integer comparison is
// a poor trade, and unlike the depth bounds this is not a recursion hazard. So
// wire/limits_document_test.go IS this host's conformance evidence for §21.7.
func checkDocumentBytes(canonicalJSON string) {
	if len(canonicalJSON) <= MaxDocumentBytes {
		return
	}
	failExpecting(
		CodeLimitExceeded,
		"$",
		"document is "+itoa(len(canonicalJSON))+" UTF-8 bytes, over the wire limit MaxDocumentBytes = "+itoa(MaxDocumentBytes),
		"a document of at most "+itoa(MaxDocumentBytes)+" UTF-8 bytes",
	)
}
