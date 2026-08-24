package wire

import "strconv"

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

	// MaxStringLength bounds a single decoded JSON string, in characters.
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
)

// walkState carries one decode call's §21 counters. Created per call, threaded
// through the decoder, never shared — see the concurrency note above.
//
// The node and op axes are counted SEPARATELY. §21.5's note for implementers is
// explicit that bounding the node decoder is not sufficient: Batch makes the op
// decoder self-recursive on its own axis, and the syntactic bound only LOOKS
// like adequate cover for it. On the reference host, 2.6 KB of nested Batches
// killed the process with every node-side guard already in place.
type walkState struct {
	nodeDepth int
	nodes     int
	opDepth   int
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
			if len(v) > MaxStringLength {
				failExpecting(
					CodeLimitExceeded,
					"$",
					"a string is longer than the wire limit MaxStringLength = "+itoa(MaxStringLength),
					"strings of no more than "+itoa(MaxStringLength)+" characters",
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
				if len(key) > MaxStringLength {
					failExpecting(
						CodeLimitExceeded,
						"$",
						"a key is longer than the wire limit MaxStringLength = "+itoa(MaxStringLength),
						"keys of no more than "+itoa(MaxStringLength)+" characters",
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
				escaped = true
			case c == '"':
				inString = false
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
