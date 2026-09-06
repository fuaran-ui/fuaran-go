package canonical

import (
	"sort"
	"unicode/utf16"
)

// SortKeys orders object keys as the canonical encoder requires: by UTF-16
// code-unit comparison (WIRE_FORMAT.md §2 rule 2).
//
// "Ordinal" in rule 2 means UTF-16 code-unit order, and the distinction is
// observable exactly above the BMP. An astral key encodes to a surrogate pair
// beginning in U+D800–U+DBFF, so under UTF-16 it sorts BELOW a key starting in
// U+E000–U+FFFF that it sorts ABOVE under code-point or UTF-8-byte comparison.
// Go's sort.Strings is byte-wise over UTF-8, which equals code-point order — so
// it disagrees with the canonical order on precisely the objects that carry both
// kinds of key, and one document then canonicalises to two different byte
// sequences, and two different hash-chain and teleport digests, depending on
// which host encoded it.
//
// The wire vocabulary's own keys are ASCII, where all three orders coincide.
// This matters at the rule-12 structured-payload positions — Custom props,
// Notify / SetState / AiTool payloads, I18n args, row-feed field names — which
// is where an author-supplied non-BMP key actually arrives.
//
// The common case is paid for: a key of ASCII bytes needs no conversion, so the
// comparison falls back to byte order without allocating.
func SortKeys(keys []string) {
	sort.Slice(keys, func(i, j int) bool { return LessUTF16(keys[i], keys[j]) })
}

// LessUTF16 reports whether a sorts before b in UTF-16 code-unit order.
func LessUTF16(a, b string) bool {
	if isASCII(a) && isASCII(b) {
		return a < b
	}
	ua := utf16.Encode([]rune(a))
	ub := utf16.Encode([]rune(b))
	for i := 0; i < len(ua) && i < len(ub); i++ {
		if ua[i] != ub[i] {
			return ua[i] < ub[i]
		}
	}
	return len(ua) < len(ub)
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}
