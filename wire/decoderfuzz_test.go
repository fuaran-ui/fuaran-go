package wire

// Decoder robustness fuzz — this host's leg.
//
// The threat model's load-bearing claim is that decoding is TOTAL: a malformed
// or hostile input yields a structured, typed error, never a panic and never a
// hang. Until a fuzz leg exists on a host, that claim rests there on a CURATED
// reject corpus — inputs an author chose, which is evidence about the author's
// imagination rather than about the decoder.
//
// ── Two harnesses, deliberately ─────────────────────────────────────────────
//
// TestDecoderFuzz drives a DETERMINISTIC stream of generated hostile inputs
// covering the five input families the reference host's harness defines, so the
// classes are covered systematically and reproducibly on every `go test`.
//
// FuzzDecodeNode / FuzzDecodeOp are NATIVE testing.F targets over the same
// entry points. Under a plain `go test` they replay their seed corpus — which is
// exactly the generated stream above, so the two agree on what they cover. Under
// `go test -fuzz=Fuzz... -fuzztime=…` the runtime's coverage-guided mutation
// takes over, which is something no port of another host's generator can do,
// and which is why this leg is a native harness rather than a transliteration.
//
// ── The invariants, per input ───────────────────────────────────────────────
//
//  1. Totality      — Decode returns a *DecodeError or a value. An escaping
//     panic is a counterexample. (`recoverDecode` already converts the decoder's
//     own panics; this harness is what proves it, and it also covers the encode
//     half of the round trip, which has no such guard.)
//  2. Termination   — it returns inside a time budget.
//  3. Bounded work  — see TestDecoderFuzzAllocationBudget, which measures
//     allocated bytes directly. The main stream carries the cheap proxy (output
//     amplification); the allocation measurement stops the world and is spent
//     where it bites.
//  4. Fixed point   — an accepted input's canonical form re-decodes and
//     re-encodes to itself, fuzzed over the reachable accept-space rather than
//     pinned by fixtures.
//
// ── Determinism ─────────────────────────────────────────────────────────────
//
// SplitMix64, not math/rand: replayability is the whole point of the seed, and
// math/rand's global source is explicitly not a stable stream across releases.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// ─── Deterministic PRNG ─────────────────────────────────────────────────────

type fuzzRng struct{ s uint64 }

const fuzzGolden uint64 = 0x9E3779B97F4A7C15

func newFuzzRng(seed uint64) *fuzzRng {
	if seed == 0 {
		return &fuzzRng{s: fuzzGolden}
	}
	return &fuzzRng{s: seed}
}

func (r *fuzzRng) nextU64() uint64 {
	r.s += fuzzGolden
	z := r.s
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// next is uniform in [0, n); 0 for a non-positive n so no caller has to guard.
func (r *fuzzRng) next(n int) int {
	if n <= 1 {
		return 0
	}
	return int(r.nextU64() % uint64(n))
}

// rangeIn is uniform in [lo, hi], inclusive.
func (r *fuzzRng) rangeIn(lo, hi int) int {
	if hi <= lo {
		return lo
	}
	return lo + r.next(hi-lo+1)
}

func (r *fuzzRng) boolean() bool { return r.nextU64()%2 == 1 }

func (r *fuzzRng) pick(xs []string) string { return xs[r.next(len(xs))] }

// ─── Corpus seeds + vocabulary ──────────────────────────────────────────────

// Built-in seeds, so the harness is self-sufficient: the go-red self-test must
// not depend on the shared corpus being checked out alongside this repo in order
// to prove that the harness can fail.
var fuzzBuiltinSeeds = []string{
	`{"id":"a","kind":{"$type":"Heading","level":1,"text":"x","variant":"Standard"}}`,
	`{"id":"b","kind":{"$type":"Box","children":[],"layout":{"$type":"Auto"},"role":"Group"}}`,
	`{"id":"c","kind":{"$type":"Markdown","source":"# hi"}}`,
	`{"$type":"RemoveNode","path":["a"]}`,
	`{"$type":"Batch","ops":[]}`,
	"{}",
	"[]",
	"null",
	"",
}

// fuzzCorpusRoot walks up from the working directory looking for the shared
// corpus. "" keeps the repo standalone-testable: a corpus-less checkout gets a
// working harness with a narrower seed pool, never a failure.
func fuzzCorpusRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		manifest := filepath.Join(dir, "wire-format-fixtures", "manifest.json")
		if _, err := os.Stat(manifest); err == nil {
			return filepath.Dir(manifest)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// loadFuzzSeeds returns every corpus payload the harness can find, as raw text.
// READ-ONLY by construction: the fuzz never writes into the corpus. A REJECT
// fixture is the most productive seed there is, since it already sits one edit
// away from the refusal boundary the fuzz is probing.
func loadFuzzSeeds(corpus string) []string {
	seeds := append([]string(nil), fuzzBuiltinSeeds...)
	if corpus == "" {
		return seeds
	}
	for _, family := range []string{"nodes", "ops", "reject", "lenient"} {
		matches, err := filepath.Glob(filepath.Join(corpus, family, "*.json"))
		if err != nil {
			continue
		}
		sort.Strings(matches)
		for _, path := range matches {
			if strings.HasSuffix(path, ".expected.json") {
				continue
			}
			if raw, err := os.ReadFile(path); err == nil {
				seeds = append(seeds, string(raw))
			}
		}
	}
	return seeds
}

var fuzzFallbackVocab = []string{
	"Box", "Heading", "Markdown", "Metric", "Badge",
	"Form", "Button", "DataGrid", "Chart", "Custom",
}

// loadFuzzVocabulary reads the wire vocabulary the near-miss generators aim just
// beside from the corpus MANIFEST, so a newly-admitted kind is fuzzed the day it
// lands rather than whenever someone remembers to extend a literal list here.
func loadFuzzVocabulary(corpus string) []string {
	if corpus != "" {
		if raw, err := os.ReadFile(filepath.Join(corpus, "manifest.json")); err == nil {
			var m struct {
				Kinds []string `json:"kinds"`
			}
			if json.Unmarshal(raw, &m) == nil && len(m.Kinds) > 0 {
				return m.Kinds
			}
		}
	}
	return fuzzFallbackVocab
}

// ─── Alphabets ──────────────────────────────────────────────────────────────

// Note the last three entries. A Go string is a byte sequence, not a sequence of
// code points, so this host can carry inputs two of its sibling hosts cannot
// even hold: a WTF-8 lone surrogate, a bare continuation byte, and 0xFF, which
// no UTF-8 sequence contains. That is a genuine advantage of fuzzing here rather
// than a difference to be smoothed away — an invalid-UTF-8 payload reaches this
// decoder in production and cannot reach some of the others.
var fuzzHostileChars = []string{
	"{", "}", "[", "]", "\"", ":", ",", "\\", "/", "-", "+", ".",
	"e", "E", "0", "9", "n", "t", "f", " ", "\t", "\n", "\r",
	"\x00", "\x7f", "\ufeff", "\u2028", "é", "中",
	"\xed\xa0\x80", "\x80", "\xff",
}

// The JSON-ESCAPE entries below are six-character TEXT (backslash, u, four
// hex digits), not the code points they denote — a decoder's unescape path is
// what they are aimed at, so writing them as the characters would test the
// wrong thing.
var fuzzHostileTokens = []string{
	"null", "true", "false", "{}", "[]", "\"\"", "-0",
	"1e999", "-1e999", "1E-999", "NaN", "Infinity", "-Infinity",
	"0x10", "00", "01", "1.2.3", "+1", ".5", "5.",
	"\\u0000", "\\uD800", "\\uFFFF", "\\x41", "\\", "\\\"",
	"\"$type\":\"\"", "\"$type\":null", "\"id\":\"\"", "\"id\":null", "\"id\":[]",
	"\"kind\":\"Heading\"", "\"children\":\"x\"",
	",", ":", "[", "]", "{", "}", "\"", "'", "/*", "*/", "//",
	"\x00", "\ufeff", "\xed\xa0\x80", "\r\n",
}

// REAL wire keys, so a generated near-miss reaches deep into the typed decoders
// instead of bouncing off the first MISSING_FIELD.
var fuzzWireKeys = []string{
	"id", "kind", "$type", "children", "layout", "role", "text", "level",
	"variant", "source", "value", "label", "fields", "items", "columns",
	"rows", "onSubmit", "onClick", "required", "binding", "style", "props",
	"state", "ops", "path", "node", "index", "target", "name", "format",
	"unit", "min", "max", "options", "spec", "__proto__", "constructor", "", " ",
}

var fuzzScalarLiterals = []string{
	"0", "-1", "1e308", "-1e308", "1e999", "3.141592653589793",
	"true", "false", "null", `""`, `"x"`, `"Standard"`, `"Group"`,
	"9007199254740993", "-0.0",
}

// nearMiss produces a near-miss of a real vocabulary word: the class of input a
// model emitter actually produces, and the class a curated reject corpus is
// worst at covering, because a human writing fixtures reaches for obvious
// garbage.
func fuzzNearMiss(r *fuzzRng, word string) string {
	if word == "" {
		return "x"
	}
	switch r.next(8) {
	case 0:
		return strings.ToLower(word)
	case 1:
		return strings.ToUpper(word)
	case 2:
		return word + "s"
	case 3:
		return word[:len(word)-1]
	case 4:
		return word + " "
	case 5:
		return " " + word
	case 6:
		i := r.next(len(word))
		return word[:i] + word[i+1:]
	default:
		i := r.next(len(word))
		return word[:i] + r.pick(fuzzHostileChars) + word[i:]
	}
}

// ─── Mutators ───────────────────────────────────────────────────────────────
//
// Each corrupts a seed payload. Named individually so a reported counterexample
// records WHICH transformation produced it: a find whose provenance is only "the
// fuzzer did something" is markedly harder to act on.

var fuzzMutatorNames = []string{
	"flip-char", "delete-span", "insert-token", "duplicate-span", "truncate",
	"transpose", "repeat-structural", "retype-value", "near-miss-type",
	"delete-key", "duplicate-key", "escape-injection", "prefix-junk", "suffix-junk",
}

func fuzzNearMissType(r *fuzzRng, vocab []string, s string) string {
	const marker = `"$type":"`
	var positions []int
	for i := strings.Index(s, marker); i >= 0; {
		positions = append(positions, i)
		next := strings.Index(s[i+len(marker):], marker)
		if next < 0 {
			break
		}
		i = i + len(marker) + next
	}
	if len(positions) == 0 {
		// No discriminator to corrupt — append one rather than returning the
		// input untouched. A silently no-op mutator quietly shrinks the effective
		// iteration count and nothing reports that it did.
		return s + `{"$type":"` + fuzzNearMiss(r, r.pick(vocab)) + `"}`
	}
	start := positions[r.next(len(positions))] + len(marker)
	closeIdx := strings.IndexByte(s[start:], '"')
	if closeIdx < 0 {
		return s
	}
	closeIdx += start
	replacement := fuzzNearMiss(r, r.pick(vocab))
	if r.boolean() {
		replacement = fuzzNearMiss(r, s[start:closeIdx])
	}
	return s[:start] + replacement + s[closeIdx:]
}

// fuzzDeleteKey deletes a whole "key":value pair, cutting from the key's opening
// quote to just past the next comma.
func fuzzDeleteKey(r *fuzzRng, s string) string {
	var positions []int
	for i := 0; ; {
		j := strings.Index(s[i:], `":`)
		if j < 0 {
			break
		}
		positions = append(positions, i+j)
		i = i + j + 2
	}
	if len(positions) == 0 {
		return s
	}
	colon := positions[r.next(len(positions))]
	closeQuote := colon
	for closeQuote > 0 && s[closeQuote] != '"' {
		closeQuote--
	}
	openQuote := closeQuote - 1
	for openQuote > 0 && s[openQuote] != '"' {
		openQuote--
	}
	cutFrom := openQuote
	if cutFrom < 0 {
		cutFrom = 0
	}
	cutTo := colon + 8
	if comma := strings.IndexByte(s[colon:], ','); comma >= 0 {
		cutTo = colon + comma + 1
	}
	if cutTo > len(s) {
		cutTo = len(s)
	}
	return s[:cutFrom] + s[cutTo:]
}

type fuzzConfig struct {
	// name lets a reported find's replay line reconstruct the exact
	// configuration as well as the exact seed. Without it the replay command is
	// only approximately right, which is worse than obviously wrong.
	name string
	// maxPayloadChars caps a generated payload. The bounded gate run keeps this
	// small so the suite stays quick; the long run raises it past the §21 string
	// bound so that bound is actually crossed.
	maxPayloadChars int
	// heavyEveryN: one in this many inputs is a deliberately pathological payload.
	heavyEveryN int
}

var fuzzBoundedConfig = fuzzConfig{name: "bounded", maxPayloadChars: 48 * 1024, heavyEveryN: 120}
var fuzzLongConfig = fuzzConfig{name: "long", maxPayloadChars: 2 * 1024 * 1024, heavyEveryN: 25}

func fuzzMutateOnce(r *fuzzRng, vocab []string, cfg fuzzConfig, s string) (string, string) {
	name := r.pick(fuzzMutatorNames)
	n := len(s)
	var result string

	switch {
	case name == "flip-char" && n > 0:
		i := r.next(n)
		result = s[:i] + r.pick(fuzzHostileChars) + s[i+1:]
	case name == "delete-span" && n > 1:
		i := r.next(n)
		result = s[:i] + s[i+min(n-i, r.rangeIn(1, 8)):]
	case name == "insert-token":
		i := r.next(n + 1)
		result = s[:i] + r.pick(fuzzHostileTokens) + s[i:]
	case name == "duplicate-span" && n > 1:
		i := r.next(n)
		take := min(n-i, r.rangeIn(1, 64))
		at := r.next(n + 1)
		result = s[:at] + s[i:i+take] + s[at:]
	case name == "truncate" && n > 1:
		result = s[:r.next(n)]
	case name == "transpose" && n > 2:
		i := r.next(n - 1)
		result = s[:i] + string(s[i+1]) + string(s[i]) + s[i+2:]
	case name == "repeat-structural":
		ch := r.pick([]string{"[", "{", `"`, "]", "}", ","})
		count := min(r.rangeIn(2, 4096), max(2, cfg.maxPayloadChars/4))
		at := r.next(n + 1)
		result = s[:at] + strings.Repeat(ch, count) + s[at:]
	case name == "retype-value" && n > 0:
		i := r.next(n)
		result = s[:i] + r.pick(fuzzScalarLiterals) + s[i+min(n-i, r.rangeIn(1, 12)):]
	case name == "near-miss-type":
		result = fuzzNearMissType(r, vocab, s)
	case name == "delete-key":
		result = fuzzDeleteKey(r, s)
	case name == "duplicate-key" && n > 4:
		// A duplicated key is a real emitter defect and a classic cross-host
		// parser divergence (first-wins vs last-wins vs refuse) — §20 of the wire
		// specification records the measured matrix and PROPOSES a rule. Fuzzing
		// it for panics is in scope here; asserting which behaviour is correct is
		// not, until that rule is ratified.
		i := strings.IndexByte(s, '"')
		j := -1
		if i >= 0 {
			if k := strings.IndexByte(s[i:], ','); k >= 0 {
				j = i + k
			}
		}
		if j < 0 {
			result = s
		} else {
			result = s[:j+1] + s[i:j] + "," + s[j+1:]
		}
	case name == "escape-injection" && n > 0:
		i := r.next(n)
		result = s[:i] + r.pick([]string{`\u`, `\uD800`, `\u00`, `\`, `\/`, `\b\f`}) + s[i:]
	case name == "prefix-junk":
		var b strings.Builder
		for k := r.rangeIn(1, 16); k > 0; k-- {
			b.WriteString(r.pick(fuzzHostileChars))
		}
		result = b.String() + s
	case name == "suffix-junk":
		var b strings.Builder
		for k := r.rangeIn(1, 16); k > 0; k-- {
			b.WriteString(r.pick(fuzzHostileChars))
		}
		result = s + b.String()
	default:
		result = s + r.pick(fuzzHostileChars)
	}

	if len(result) > cfg.maxPayloadChars {
		result = result[:cfg.maxPayloadChars]
	}
	return name, result
}

// ─── Structure-aware generation ─────────────────────────────────────────────

func fuzzGenValue(r *fuzzRng, depth int, b *strings.Builder, vocab []string, cfg fuzzConfig) {
	if b.Len() > cfg.maxPayloadChars {
		b.WriteString("0")
		return
	}
	if depth <= 0 {
		b.WriteString(r.pick(fuzzScalarLiterals))
		return
	}
	switch branch := r.next(12); {
	case branch <= 3:
		b.WriteString(r.pick(fuzzScalarLiterals))
	case branch <= 7:
		b.WriteString("{")
		for i, n := 0, r.rangeIn(0, 5); i < n; i++ {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString(`"` + r.pick(fuzzWireKeys) + `":`)
			fuzzGenValue(r, depth-1, b, vocab, cfg)
		}
		b.WriteString("}")
	case branch <= 10:
		b.WriteString("[")
		for i, n := 0, r.rangeIn(0, 5); i < n; i++ {
			if i > 0 {
				b.WriteString(",")
			}
			fuzzGenValue(r, depth-1, b, vocab, cfg)
		}
		b.WriteString("]")
	default:
		// A plausible node shell around a wrong interior: the shape that gets
		// furthest into the typed decoders before it fails, and so the one most
		// likely to reach code a shallow syntax reject never does.
		b.WriteString(`{"id":"g","kind":{"$type":"`)
		b.WriteString(fuzzNearMiss(r, r.pick(vocab)))
		b.WriteString(`","`)
		b.WriteString(r.pick(fuzzWireKeys) + `":`)
		fuzzGenValue(r, depth-1, b, vocab, cfg)
		b.WriteString("}}")
	}
}

// fuzzGenPathological takes depth, width and string length past the §21 limits.
// Every payload is assembled as TEXT: building one as a nested value would blow
// the harness's own stack while CONSTRUCTING the input, which proves nothing
// about the decoder.
func fuzzGenPathological(r *fuzzRng, cfg fuzzConfig) string {
	capChars := cfg.maxPayloadChars
	switch r.next(9) {
	case 0:
		n := min(capChars/2, r.rangeIn(64, 200000))
		return strings.Repeat("[", n) + strings.Repeat("]", n)
	case 1:
		n := min(capChars/6, r.rangeIn(64, 100000))
		return strings.Repeat(`{"a":`, n) + "1" + strings.Repeat("}", n)
	case 2:
		// Unterminated as well as over-deep: the depth guard must fire on the way
		// DOWN, before truncation is ever reached.
		n := min(capChars/2, r.rangeIn(64, 200000))
		return strings.Repeat("[", n)
	case 3:
		// Deep NODE nesting rather than deep JSON — crosses the tree depth bound
		// while staying far inside the JSON one, isolating the tree limit.
		acc := `{"id":"leaf","kind":{"$type":"Heading","level":1,"text":"x","variant":"Standard"}}`
		for i, depth := 1, r.rangeIn(2, 400); i <= depth; i++ {
			if len(acc) >= capChars {
				break
			}
			acc = `{"id":"n` + strconv.Itoa(i) + `","kind":{"$type":"Box","children":[` + acc + `],"layout":{"$type":"Auto"},"role":"Group"}}`
		}
		return acc
	case 4:
		n := min(capChars/2, r.rangeIn(1000, 200000))
		return `{"id":"a","kind":[` + strings.TrimSuffix(strings.Repeat("1,", n), ",") + `]}`
	case 5:
		n := min(capChars, r.rangeIn(1000, 1200000))
		return `{"id":"a","kind":{"$type":"Heading","level":1,"text":"` + strings.Repeat("x", n) + `","variant":"Standard"}}`
	case 6:
		acc := `{"$type":"Batch","ops":[]}`
		for i, depth := 0, r.rangeIn(2, 300); i < depth; i++ {
			if len(acc) >= capChars {
				break
			}
			acc = `{"$type":"Batch","ops":[` + acc + `]}`
		}
		return acc
	case 7:
		// Escape-heavy: nearly every character an escape, so the unescape path
		// does the work rather than the structural walk.
		n := min(capChars/6, r.rangeIn(500, 100000))
		return `{"id":"a","kind":{"$type":"Markdown","source":"` + strings.Repeat(`A`, n) + `"}}`
	default:
		n := min(capChars/4, r.rangeIn(500, 50000))
		var b strings.Builder
		b.WriteString("{")
		for i := 0; i < n; i++ {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString(`"k` + strconv.Itoa(i) + `":1`)
		}
		b.WriteString("}")
		return b.String()
	}
}

type fuzzGenerated struct {
	payload string
	origin  string
}

// fuzzGenerate is deterministic in (seed, iteration, cfg) — the replay contract.
// Every branch draws from the same rng, so ADDING a family renumbers the stream;
// that is why a reported find carries its payload too and replay is the backstop
// rather than the primary record.
func fuzzGenerate(r *fuzzRng, seeds, vocab []string, cfg fuzzConfig, iteration int) fuzzGenerated {
	if iteration%cfg.heavyEveryN == 0 {
		return fuzzGenerated{payload: fuzzGenPathological(r, cfg), origin: "pathological"}
	}
	switch branch := r.next(10); {
	case branch <= 1:
		var b strings.Builder
		fuzzGenValue(r, r.rangeIn(1, 6), &b, vocab, cfg)
		return fuzzGenerated{payload: b.String(), origin: "structured-generation"}
	case branch == 2:
		var b strings.Builder
		for k := r.rangeIn(0, 200); k > 0; k-- {
			b.WriteString(r.pick(fuzzHostileChars))
		}
		return fuzzGenerated{payload: b.String(), origin: "raw-junk"}
	case branch == 3:
		// Crossover: prefix of one seed, suffix of another. Produces half-valid
		// documents no single-seed mutation reaches.
		a, c := r.pick(seeds), r.pick(seeds)
		i, j := 0, 0
		if len(a) > 0 {
			i = r.next(len(a))
		}
		if len(c) > 0 {
			j = r.next(len(c))
		}
		return fuzzGenerated{payload: a[:i] + c[j:], origin: "crossover"}
	default:
		acc := r.pick(seeds)
		names := make([]string, 0, 4)
		for k := r.rangeIn(1, 4); k > 0; k-- {
			var name string
			name, acc = fuzzMutateOnce(r, vocab, cfg, acc)
			names = append(names, name)
		}
		return fuzzGenerated{payload: acc, origin: "mutation:" + strings.Join(names, "+")}
	}
}

// ─── Subjects + verdicts ────────────────────────────────────────────────────

// fuzzSubjectResult is what one decode entry point did with one input.
// Deliberately string-typed: the harness compares canonical FORMS, so it needs
// no access to the tree types and both entry points share one machinery.
type fuzzSubjectResult struct {
	refusedCode   string // "" when accepted
	canonical     string
	reDecoded     string
	reDecodedCode string // non-empty when the decoder's OWN output is refused
}

// fuzzSubject is one decode entry point, or a deliberately-broken stand-in. run
// is allowed — required, in the self-test's case — to panic: recovering is the
// harness's job.
type fuzzSubject struct {
	name string
	run  func(string) fuzzSubjectResult
}

func fuzzCodeOf(err error) string {
	var de *DecodeError
	if e, ok := err.(*DecodeError); ok {
		de = e
	}
	if de == nil {
		// A non-DecodeError from a total decoder is itself a finding, so it is
		// reported under a name that cannot be mistaken for a canonical code.
		return "NON-STRUCTURED-ERROR"
	}
	return string(de.Code)
}

var fuzzNodeSubject = fuzzSubject{
	name: "DecodeNode",
	run: func(input string) fuzzSubjectResult {
		node, err := DecodeNode(input)
		if err != nil {
			return fuzzSubjectResult{refusedCode: fuzzCodeOf(err)}
		}
		canonical, err := EncodeNode(node)
		if err != nil {
			return fuzzSubjectResult{canonical: "", reDecodedCode: "ENCODE-FAILED:" + err.Error()}
		}
		again, err := DecodeNode(canonical)
		if err != nil {
			return fuzzSubjectResult{canonical: canonical, reDecodedCode: fuzzCodeOf(err)}
		}
		second, err := EncodeNode(again)
		if err != nil {
			return fuzzSubjectResult{canonical: canonical, reDecodedCode: "ENCODE-FAILED:" + err.Error()}
		}
		return fuzzSubjectResult{canonical: canonical, reDecoded: second}
	},
}

var fuzzOpSubject = fuzzSubject{
	name: "DecodeOp",
	run: func(input string) fuzzSubjectResult {
		op, err := DecodeOp(input)
		if err != nil {
			return fuzzSubjectResult{refusedCode: fuzzCodeOf(err)}
		}
		canonical, err := EncodeOp(op)
		if err != nil {
			return fuzzSubjectResult{canonical: "", reDecodedCode: "ENCODE-FAILED:" + err.Error()}
		}
		again, err := DecodeOp(canonical)
		if err != nil {
			return fuzzSubjectResult{canonical: canonical, reDecodedCode: fuzzCodeOf(err)}
		}
		second, err := EncodeOp(again)
		if err != nil {
			return fuzzSubjectResult{canonical: canonical, reDecodedCode: "ENCODE-FAILED:" + err.Error()}
		}
		return fuzzSubjectResult{canonical: canonical, reDecoded: second}
	},
}

// BOTH public entry points, since the totality claim is made about the decoder,
// not about one of its two doors.
var fuzzRealSubjects = []fuzzSubject{fuzzNodeSubject, fuzzOpSubject}

// fuzzVerdict's kind is the coarse class; detail is for the report. "rejected"
// and "clean" are both PASSES — a fuzz harness that treated refusal as failure
// would be asserting the opposite of the claim under test.
type fuzzVerdict struct {
	kind   string
	detail string
}

// fuzzKnownNonFiniteHole names the ONE defect this harness is permitted to
// observe without failing, and it is named rather than numbered so a reader
// meets the reason at the point of the exclusion.
//
// The wire specification's §7 requires a decoder to accept the quoted `"NaN"` /
// `"Infinity"` / `"-Infinity"` sentinels at a float slot, and §5 requires every
// host to EMIT them. This host emits them and does not accept them at every such
// slot — so `decode → encode → decode` does not close on a document carrying a
// non-finite number, and this harness finds one within a few thousand generated
// inputs (`{"…","viewBox":{…,"width":-1e999}}` reduces to exactly that).
//
// It is EXCLUDED here for three reasons, and none of them is that it is
// unimportant. It is already recorded in the specification as a §7 conformance
// defect rather than an open question. It spans more than this host, so fixing
// it here alone would manufacture a new divergence of precisely the kind the
// cross-host parity work exists to close. And the phase that shipped this
// harness declares itself additive — evidence and gates — so widening a
// decoder's accept set across every float slot is not its change to make.
//
// The exclusion is deliberately keyed on the CAUSE (a non-finite sentinel in the
// canonical form), not on a fixture id or an iteration number: the seed pool is
// the shared corpus, so the generated stream renumbers whenever the corpus
// moves, and an exclusion keyed to an iteration would silence a different defect
// next week. It is COUNTED and PRINTED on every run, and it disappears on its own
// the moment the decoder accepts what it emits.
const fuzzKnownNonFiniteHole = "known-nonfinite-roundtrip-hole"

func fuzzIsKnownNonFiniteHole(canonical string) bool {
	return strings.Contains(canonical, `"NaN"`) ||
		strings.Contains(canonical, `"Infinity"`) ||
		strings.Contains(canonical, `"-Infinity"`)
}

func (v fuzzVerdict) isCounterexample() bool {
	return v.kind != "rejected" && v.kind != "clean" && v.kind != fuzzKnownNonFiniteHole
}

type fuzzBudgets struct {
	softTimeMs               float64
	amplificationFloorChars  int
	maxAmplification         int
	allocationFloorBytes     uint64
	allocationBytesPerCharIn uint64
}

var fuzzDefaultBudgets = fuzzBudgets{
	softTimeMs:              3000,
	amplificationFloorChars: 64 * 1024,
	maxAmplification:        64,
	// Used only by TestDecoderFuzzAllocationBudget. A floor plus a per-character
	// rate, and the two bind in different places: below the floor the fixed cost
	// of a decode dominates and per-character ratios are meaningless; above it the
	// rate binds, and it is the rate that catches super-linear work.
	allocationFloorBytes:     16 * 1024 * 1024,
	allocationBytesPerCharIn: 512,
}

type fuzzMeasured struct {
	verdict        fuzzVerdict
	elapsedMs      float64
	canonicalChars int
}

// fuzzCheck runs one input through one subject and judges it against every
// invariant. Every panic is recovered HERE and nowhere else, which is what makes
// "no panic escapes" a measured property rather than a hope.
func fuzzCheck(subject fuzzSubject, budgets fuzzBudgets, input string) (m fuzzMeasured) {
	started := time.Now()
	defer func() {
		if rec := recover(); rec != nil {
			m = fuzzMeasured{
				verdict: fuzzVerdict{
					kind:   fmt.Sprintf("escaped-%T", rec),
					detail: fmt.Sprint(rec),
				},
				elapsedMs: float64(time.Since(started).Microseconds()) / 1000.0,
			}
		}
	}()

	result := subject.run(input)
	elapsed := float64(time.Since(started).Microseconds()) / 1000.0

	// Order matters: an input that both ran long AND over-amplified is reported
	// as the time breach, because that is the one an operator has to act on first.
	if elapsed > budgets.softTimeMs {
		return fuzzMeasured{fuzzVerdict{"timed-out", fmt.Sprintf("decode returned only after %.0f ms", elapsed)}, elapsed, 0}
	}
	if result.refusedCode != "" {
		return fuzzMeasured{fuzzVerdict{"rejected", result.refusedCode}, elapsed, 0}
	}

	budget := budgets.maxAmplification * len(input)
	if budget < budgets.amplificationFloorChars {
		budget = budgets.amplificationFloorChars
	}
	if len(result.canonical) > budget {
		detail := fmt.Sprintf("canonical form is %d chars, budget %d", len(result.canonical), budget)
		return fuzzMeasured{fuzzVerdict{"over-amplified", detail}, elapsed, len(result.canonical)}
	}
	if result.reDecodedCode != "" {
		detail := "the decoder's own output re-decodes as " + result.reDecodedCode
		if fuzzIsKnownNonFiniteHole(result.canonical) {
			return fuzzMeasured{fuzzVerdict{fuzzKnownNonFiniteHole, detail}, elapsed, len(result.canonical)}
		}
		return fuzzMeasured{fuzzVerdict{"canonical-refused", detail}, elapsed, len(result.canonical)}
	}
	if result.canonical != result.reDecoded {
		detail := fmt.Sprintf("first canonical form %d chars, second %d", len(result.canonical), len(result.reDecoded))
		return fuzzMeasured{fuzzVerdict{"fixed-point-broken", detail}, elapsed, len(result.canonical)}
	}
	return fuzzMeasured{fuzzVerdict{kind: "clean"}, elapsed, len(result.canonical)}
}

// ─── The run ────────────────────────────────────────────────────────────────

type fuzzCounterexample struct {
	subject   string
	iteration int
	origin    string
	verdict   fuzzVerdict
	payload   string
	// The pre-minimisation length, so the report says how far the reduction got.
	original int
}

func (c fuzzCounterexample) describe(seed uint64, configName string) string {
	preview := c.payload
	if len(preview) > 300 {
		preview = preview[:300] + " ...(truncated)"
	}
	return fmt.Sprintf(
		"subject: %s\nseed: %d, iteration: %d, config: %s\norigin: %s\nverdict: %s — %s\n"+
			"length: %d chars original, %d minimised\ninput: %q\n\n"+
			"Counterexample policy: fix the decoder, then land the input as a permanent\nreject fixture in the shared corpus, "+
			"so every conformant host inherits the case\nrather than only this one.",
		c.subject, seed, c.iteration, configName, c.origin, c.verdict.kind, c.verdict.detail, c.original, len(c.payload), preview)
}

type fuzzRunStats struct {
	Iterations                int            `json:"iterations"`
	Inputs                    int            `json:"inputs"`
	CorpusSeeds               int            `json:"corpusSeeds"`
	CorpusPresent             bool           `json:"corpusPresent"`
	RejectCodes               map[string]int `json:"rejectCodes"`
	Accepted                  int            `json:"accepted"`
	MaxDecodeMs               float64        `json:"maxDecodeMs"`
	MaxCanonicalAmplification float64        `json:"maxCanonicalAmplification"`
	ElapsedSeconds            float64        `json:"elapsedSeconds"`
	Seed                      string         `json:"seed"`
	Config                    string         `json:"config"`
	Host                      string         `json:"host"`
	EntryPoints               []string       `json:"entryPoints"`
	Counterexamples           int            `json:"counterexamples"`
	// The one EXCLUDED defect class, counted and published rather than dropped:
	// an exclusion nobody can see reads as "found nothing".
	KnownNonFiniteHoles int    `json:"knownNonFiniteRoundTripHoles"`
	GeneratedAt         string `json:"generatedAt"`

	finds []fuzzCounterexample
}

// fuzzRun drives iterations generated inputs through every subject. subjects is
// a parameter precisely so the go-red self-test drives the IDENTICAL machinery
// with a broken stand-in: a fuzz harness nobody has ever seen fail is decoration.
// fuzzVerdictClass is the coarse class a minimisation holds steady. It
// deliberately drops the payload-specific detail: a smaller input that fails the
// same WAY is the reduction we want, and demanding byte-identical detail would
// refuse almost every candidate.
func fuzzVerdictClass(v fuzzVerdict) string {
	if !v.isCounterexample() {
		return "held"
	}
	return v.kind
}

// fuzzMinimise is delta-debugging by span deletion: repeatedly cut a chunk and
// keep the cut if the input still fails the same WAY. Bounded by a candidate
// count AND a wall clock, because the class most worth minimising (a time
// breach) is exactly the one where each probe is expensive.
//
// Cuts land on UTF-8 boundaries. A Go string can hold invalid UTF-8 — this
// harness deliberately generates some — but a cut that split a well-formed
// sequence would produce a *different* defect class and the reduction would
// wander away from the finding it started with.
func fuzzMinimise(classify func(string) string, target, input string) string {
	clock := time.Now()
	best := input
	granularity := 2
	budget := 400

	boundaryAtOrAfter := func(s string, i int) int {
		for i < len(s) && !utf8.RuneStart(s[i]) {
			i++
		}
		return min(i, len(s))
	}

	for budget > 0 && time.Since(clock) < 25*time.Second {
		chunk := max(1, len(best)/granularity)
		reduced := false
		i := 0
		for i < len(best) && budget > 0 && time.Since(clock) < 25*time.Second {
			to := boundaryAtOrAfter(best, min(i+chunk, len(best)))
			candidate := best[:i] + best[to:]
			budget--
			if len(candidate) > 0 && classify(candidate) == target {
				best = candidate
				reduced = true
			} else {
				i = to
			}
		}
		if reduced {
			granularity = max(2, granularity/2)
		} else if chunk > 1 {
			granularity *= 2
		} else {
			break
		}
	}
	return best
}

// fuzzReproDir is the harness's own source directory, so a persisted repro lands
// where `git status` shows it rather than inside a build cache the next clean
// build empties.
func fuzzReproDir() string {
	if dir, err := os.Getwd(); err == nil {
		return filepath.Join(dir, "fuzz-repros")
	}
	return "fuzz-repros"
}

// fuzzPersist writes a counterexample's payload plus the metadata needed to act
// on it. The report line truncates at 300 characters, which is right for a log
// and useless for a repro; a find nobody can reproduce is a find nobody fixes.
func fuzzPersist(dir string, c fuzzCounterexample, seed uint64, configName string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	stem := fmt.Sprintf("%s-%s-seed%d-iter%d", fuzzVerdictClass(c.verdict), c.subject, seed, c.iteration)
	safe := []rune(stem)
	for i, r := range safe {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			safe[i] = '-'
		}
	}
	base := filepath.Join(dir, string(safe))
	if err := os.WriteFile(base+".input.txt", []byte(c.payload), 0o644); err != nil {
		return "", err
	}
	notes := c.describe(seed, configName) + "\n\nThe payload is beside this file as `.input.txt`.\n"
	if err := os.WriteFile(base+".md", []byte(notes), 0o644); err != nil {
		return "", err
	}
	return base + ".input.txt", nil
}

func fuzzRun(subjects []fuzzSubject, budgets fuzzBudgets, cfg fuzzConfig, seed uint64, iterations int, seeds, vocab []string, minimiseFinds bool) fuzzRunStats {
	r := newFuzzRng(seed)
	started := time.Now()
	stats := fuzzRunStats{
		RejectCodes: map[string]int{},
		CorpusSeeds: len(seeds),
		Seed:        strconv.FormatUint(seed, 10),
		Config:      cfg.name,
		Host:        "fuaran-go",
	}
	for _, s := range subjects {
		stats.EntryPoints = append(stats.EntryPoints, s.name)
	}

	for i := 1; i <= iterations; i++ {
		g := fuzzGenerate(r, seeds, vocab, cfg, i)
		for _, subject := range subjects {
			m := fuzzCheck(subject, budgets, g.payload)
			stats.Inputs++
			if m.elapsedMs > stats.MaxDecodeMs {
				stats.MaxDecodeMs = m.elapsedMs
			}
			if m.canonicalChars > 0 && len(g.payload) > 0 {
				if ratio := float64(m.canonicalChars) / float64(len(g.payload)); ratio > stats.MaxCanonicalAmplification {
					stats.MaxCanonicalAmplification = ratio
				}
			}
			switch m.verdict.kind {
			case "rejected":
				stats.RejectCodes[m.verdict.detail]++
			case "clean":
				stats.Accepted++
			case fuzzKnownNonFiniteHole:
				stats.KnownNonFiniteHoles++
			default:
				payload := g.payload
				if minimiseFinds {
					payload = fuzzMinimise(func(candidate string) string {
						return fuzzVerdictClass(fuzzCheck(subject, budgets, candidate).verdict)
					}, fuzzVerdictClass(m.verdict), payload)
				}
				stats.finds = append(stats.finds, fuzzCounterexample{
					subject: subject.name, iteration: i, origin: g.origin, verdict: m.verdict, payload: payload,
					original: len(g.payload),
				})
			}
		}
		stats.Iterations = i
	}

	stats.ElapsedSeconds = time.Since(started).Seconds()
	stats.Counterexamples = len(stats.finds)
	stats.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	return stats
}

// fuzzSummarise is the one-line human summary, printed on every run, pass or
// fail: a harness whose output is only visible when it fails cannot be checked
// for having quietly stopped generating anything.
func fuzzSummarise(s fuzzRunStats) string {
	codes := make([]string, 0, len(s.RejectCodes))
	for code, n := range s.RejectCodes {
		codes = append(codes, fmt.Sprintf("%s=%d", code, n))
	}
	sort.Strings(codes)
	perIteration := 0
	if s.Iterations > 0 {
		perIteration = s.Inputs / s.Iterations
	}
	return fmt.Sprintf(
		"%d inputs (%d iterations x %d entry points) in %.1f s — accepted %d, refused [%s], %d counterexamples, "+
			"%d known non-finite round-trip holes (§7, EXCLUDED); max decode %.0f ms; max canonical amplification %.1f x",
		s.Inputs, s.Iterations, perIteration, s.ElapsedSeconds, s.Accepted, strings.Join(codes, " "),
		len(s.finds), s.KnownNonFiniteHoles, s.MaxDecodeMs, s.MaxCanonicalAmplification)
}

// ─── The gate ───────────────────────────────────────────────────────────────

const fuzzSeed uint64 = 1023

func fuzzEnvInt(t *testing.T, name string, fallback int) int {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		t.Fatalf("%s: %q is not a positive integer", name, raw)
	}
	return n
}

// TestDecoderFuzz is the bounded gate run. The long form is
//
//	FUARAN_FUZZ_LONG=1 FUARAN_FUZZ_ITERATIONS=250000 \
//	  FUARAN_FUZZ_EVIDENCE=<file> go test ./wire -run TestDecoderFuzz -timeout 2h
//
// which is what makes the published totality figures regenerable by a scheduled
// job rather than by someone remembering to re-run them.
func TestDecoderFuzz(t *testing.T) {
	corpus := fuzzCorpusRoot()
	seeds := loadFuzzSeeds(corpus)
	vocab := loadFuzzVocabulary(corpus)

	cfg := fuzzBoundedConfig
	iterations := 4000
	if os.Getenv("FUARAN_FUZZ_LONG") == "1" {
		cfg = fuzzLongConfig
		iterations = 250000
	}
	iterations = fuzzEnvInt(t, "FUARAN_FUZZ_ITERATIONS", iterations)

	stats := fuzzRun(fuzzRealSubjects, fuzzDefaultBudgets, cfg, fuzzSeed, iterations, seeds, vocab, true)
	stats.CorpusPresent = corpus != ""
	t.Logf("[decoder-fuzz] %s", fuzzSummarise(stats))

	if path := strings.TrimSpace(os.Getenv("FUARAN_FUZZ_EVIDENCE")); path != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("creating the evidence directory: %v", err)
		}
		raw, err := json.MarshalIndent(stats, "", "  ")
		if err != nil {
			t.Fatalf("marshalling the evidence record: %v", err)
		}
		if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
			t.Fatalf("writing the evidence record: %v", err)
		}
	}

	for i, c := range stats.finds {
		if i >= 5 {
			t.Errorf("... and %d further counterexamples", len(stats.finds)-5)
			break
		}
		detail := c.describe(fuzzSeed, cfg.name)
		// The report line truncates at 300 characters, which is right for a log and
		// useless for a repro. A find nobody can reproduce is a find nobody fixes.
		if path, err := fuzzPersist(fuzzReproDir(), c, fuzzSeed, cfg.name); err == nil {
			detail += "\n\nrepro persisted at " + path
		} else {
			detail += "\n\n(could not persist the repro: " + err.Error() + ")"
		}
		t.Errorf("counterexample — the refusal contract does not hold:\n%s", detail)
	}

	// A run that generated nothing would report zero counterexamples and look
	// identical to a clean one. Pin the work actually done.
	if stats.Iterations != iterations || stats.Inputs != iterations*len(fuzzRealSubjects) {
		t.Fatalf("the run did not cover what it claims: %d iterations, %d inputs", stats.Iterations, stats.Inputs)
	}
	// Both outcomes must occur. A stream that only ever refuses never reaches the
	// fixed-point invariant; one that only ever accepts is not hostile.
	if stats.Accepted == 0 {
		t.Error("no generated input was ACCEPTED — the fixed-point invariant was never exercised")
	}
	if len(stats.RejectCodes) == 0 {
		t.Error("no generated input was REFUSED — the stream is not hostile")
	}
}

// TestDecoderFuzzAllocationBudget measures the third invariant directly.
//
// runtime.ReadMemStats stops the world, so it is spent on the pathological
// family rather than across every iteration: that family is precisely where an
// adversarially-shaped small document could amplify into a large heap, and it is
// the only one whose cost is worth paying to see. The main stream carries the
// cheap output-amplification proxy instead, and the two together are the
// invariant — neither half is the whole of it.
func TestDecoderFuzzAllocationBudget(t *testing.T) {
	corpus := fuzzCorpusRoot()
	seeds := loadFuzzSeeds(corpus)
	vocab := loadFuzzVocabulary(corpus)
	cfg := fuzzConfig{name: "pathological-probe", maxPayloadChars: 64 * 1024, heavyEveryN: 1}
	r := newFuzzRng(fuzzSeed)

	worstRatio := 0.0
	for i := 1; i <= 40; i++ {
		payload := fuzzGenerate(r, seeds, vocab, cfg, i).payload
		if len(payload) == 0 {
			continue
		}
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		for _, subject := range fuzzRealSubjects {
			fuzzCheck(subject, fuzzDefaultBudgets, payload)
		}
		runtime.ReadMemStats(&after)

		allocated := after.TotalAlloc - before.TotalAlloc
		budget := fuzzDefaultBudgets.allocationBytesPerCharIn * uint64(len(payload))
		if budget < fuzzDefaultBudgets.allocationFloorBytes {
			budget = fuzzDefaultBudgets.allocationFloorBytes
		}
		if ratio := float64(allocated) / float64(len(payload)); ratio > worstRatio {
			worstRatio = ratio
		}
		if allocated > budget {
			t.Errorf("a %d-character pathological input allocated %d bytes, budget %d (%.0f bytes per input character)",
				len(payload), allocated, budget, float64(allocated)/float64(len(payload)))
		}
	}
	// A probe that measured nothing would pass this test silently.
	if worstRatio <= 0 {
		t.Fatal("the allocation probe measured nothing")
	}
	t.Logf("[decoder-fuzz] worst pathological allocation: %.0f bytes per input character", worstRatio)
}

// ─── Go-red: the harness fails when the decoder is broken ───────────────────
//
// Permanent, not a one-off demonstration at authoring time. Each mutant breaks
// ONE invariant, and the inverse pin proves each is PARTIAL — a mutant that
// broke every input would make the harness look sensitive while testing nothing.

func fuzzEveryNth(n int, name string, broken func(string) fuzzSubjectResult) fuzzSubject {
	ok := fuzzSubjectResult{refusedCode: "INVALID_JSON"}
	return fuzzSubject{name: name, run: func(input string) fuzzSubjectResult {
		if len(input)%n == 0 {
			return broken(input)
		}
		return ok
	}}
}

// The slow mutant is measured against a DELIBERATELY TIGHT budget rather than
// the shipped three-second one. Sleeping past the real budget would cost three
// seconds per firing — the sort of cost that gets a go-red test deleted rather
// than fixed. What is under test is the harness's ability to see a decode that
// returned past ITS budget, and that is exactly as true at 5 ms as at 3 s.
var fuzzTightTimeBudget = func() fuzzBudgets {
	b := fuzzDefaultBudgets
	b.softTimeMs = 5
	return b
}()

func fuzzMutants() []struct {
	subject fuzzSubject
	budgets fuzzBudgets
} {
	big := strings.Repeat("x", fuzzDefaultBudgets.amplificationFloorChars+1)
	return []struct {
		subject fuzzSubject
		budgets fuzzBudgets
	}{
		{fuzzEveryNth(3, "mutant:panics", func(string) fuzzSubjectResult {
			panic("deliberate: the decoder let a panic escape")
		}), fuzzDefaultBudgets},
		{fuzzEveryNth(5, "mutant:slow", func(string) fuzzSubjectResult {
			time.Sleep(time.Duration(fuzzTightTimeBudget.softTimeMs+20) * time.Millisecond)
			return fuzzSubjectResult{refusedCode: "INVALID_JSON"}
		}), fuzzTightTimeBudget},
		{fuzzEveryNth(7, "mutant:amplifies", func(string) fuzzSubjectResult {
			return fuzzSubjectResult{canonical: big, reDecoded: big}
		}), fuzzDefaultBudgets},
		{fuzzEveryNth(11, "mutant:canonical-refused", func(string) fuzzSubjectResult {
			return fuzzSubjectResult{canonical: "{}", reDecodedCode: "INVALID_JSON"}
		}), fuzzDefaultBudgets},
		{fuzzEveryNth(13, "mutant:fixed-point-broken", func(string) fuzzSubjectResult {
			return fuzzSubjectResult{canonical: `{"a":1}`, reDecoded: `{"a":2}`}
		}), fuzzDefaultBudgets},
	}
}

func TestDecoderFuzzGoesRedOnABrokenDecoder(t *testing.T) {
	corpus := fuzzCorpusRoot()
	seeds := loadFuzzSeeds(corpus)
	vocab := loadFuzzVocabulary(corpus)

	for _, m := range fuzzMutants() {
		t.Run(m.subject.name, func(t *testing.T) {
			stats := fuzzRun([]fuzzSubject{m.subject}, m.budgets, fuzzBoundedConfig, fuzzSeed, 200, seeds, vocab, false)
			if len(stats.finds) == 0 {
				t.Fatalf("%s produced no counterexample — the harness cannot see this defect class", m.subject.name)
			}
			// The inverse pin, in the same place as the claim it qualifies.
			if len(stats.finds) >= stats.Inputs {
				t.Fatalf("%s broke EVERY input — it proves nothing about the harness's discrimination", m.subject.name)
			}
		})
	}
}

func TestDecoderFuzzCallsAWellFormedNodeGood(t *testing.T) {
	// The floor under everything above: the machinery must call a GOOD input
	// good. A harness that reported every input as a counterexample would pass
	// every go-red test in this file.
	good := `{"id":"a","kind":{"$type":"Heading","level":1,"text":"x","variant":"Standard"}}`
	m := fuzzCheck(fuzzRealSubjects[0], fuzzDefaultBudgets, good)
	if m.verdict.kind != "clean" {
		t.Fatalf("a well-formed node decoded as %q (%s)", m.verdict.kind, m.verdict.detail)
	}
}

// ─── Native coverage-guided targets ─────────────────────────────────────────
//
// Seeded from the SAME generated stream the deterministic harness drives, so a
// plain `go test` (which replays only the seed corpus) covers the five input
// families, and `go test -fuzz` extends them with coverage-guided mutation the
// deterministic generator cannot reach.

func fuzzSeedCorpus(f *testing.F, decoder string) {
	f.Helper()
	corpus := fuzzCorpusRoot()
	seeds := loadFuzzSeeds(corpus)
	vocab := loadFuzzVocabulary(corpus)
	r := newFuzzRng(fuzzSeed)
	// A tight cap: the native engine re-runs every seed on every `go test`, and a
	// 2 MiB pathological seed would put that cost in the ordinary gate for no
	// coverage the deterministic run does not already have.
	cfg := fuzzConfig{name: "seed-" + decoder, maxPayloadChars: 4096, heavyEveryN: 20}
	for i := 1; i <= 200; i++ {
		f.Add(fuzzGenerate(r, seeds, vocab, cfg, i).payload)
	}
	for _, s := range fuzzBuiltinSeeds {
		f.Add(s)
	}
}

// fuzzAssertTotal is the invariant both native targets assert. It is the same
// contract fuzzCheck measures, minus the budgets: the native engine imposes its
// own timeout and memory limit, so re-imposing ours would report the engine's
// own throttling as a decoder defect.
func fuzzAssertTotal(t *testing.T, subject fuzzSubject, input string) {
	t.Helper()
	result := subject.run(input)
	if result.refusedCode != "" {
		if result.refusedCode == "NON-STRUCTURED-ERROR" {
			t.Fatalf("%s refused with a non-DecodeError: %q", subject.name, input)
		}
		return
	}
	if result.reDecodedCode != "" {
		if fuzzIsKnownNonFiniteHole(result.canonical) {
			// The one excluded class — see fuzzKnownNonFiniteHole. Skipped rather
			// than passed silently, so `go test -fuzz` reports how often the
			// coverage-guided engine reaches it.
			t.Skip("known §7 non-finite round-trip hole")
		}
		t.Fatalf("%s: the decoder's own canonical output re-decodes as %s: %q", subject.name, result.reDecodedCode, input)
	}
	if result.canonical != result.reDecoded {
		t.Fatalf("%s: the canonical form is not a fixed point: %q", subject.name, input)
	}
}

func FuzzDecodeNode(f *testing.F) {
	fuzzSeedCorpus(f, "node")
	f.Fuzz(func(t *testing.T, input string) { fuzzAssertTotal(t, fuzzNodeSubject, input) })
}

func FuzzDecodeOp(f *testing.F) {
	fuzzSeedCorpus(f, "op")
	f.Fuzz(func(t *testing.T, input string) { fuzzAssertTotal(t, fuzzOpSubject, input) })
}
