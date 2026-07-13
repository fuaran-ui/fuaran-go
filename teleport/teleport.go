// Package teleport is the Go host of the §17 teleport state bundle — serialise
// a running app (the tree, its Binding.State values, an optional bounded
// op-history window, and the op-chain head hash) into one URL/QR-sized string
// and resume it on any device. The substrate for the Teleport demo (fill in an
// app on a laptop, scan a QR, keep going on a phone) and Send Me That App.
//
// String format (§17.1): FT1.<base64url(deflate(canonical-JSON envelope))> —
// raw RFC 1951 deflate (no zlib/gzip wrapper), unpadded base64url. This host
// encodes deterministically via compress/flate and decodes the full RFC 1951
// range (so a bundle from any standard deflate library interoperates); the
// cross-host byte-identical reference-encoder certification is a documented
// follow-on, so this host certifies its own round-trip + the digest / size /
// version rejects + the budget. stdlib-only.
package teleport

import (
	"bytes"
	"compress/flate"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"unicode/utf8"

	"github.com/fuaran-ui/fuaran-go/validator"
	"github.com/fuaran-ui/fuaran-go/wire"
)

// Format constants.
const (
	prefix        = "FT1."
	bundleVersion = "teleport@1"
	digestPrefix  = "fuaran-teleport:v1|"
	// maxInput bounds the encoded string accepted before any decompression work
	// (§17.4 step 1); maxInflate caps the inflated output (a deflate bomb).
	maxInput   = 64 * 1024
	maxInflate = 1024 * 1024
)

// Size budgets (§17.5), in encoded characters (= bytes; the string is ASCII).
const (
	BudgetQRHard        = 2953 // byte-mode capacity at QR version 40, EC level L
	BudgetQRComfortable = 1273 // ≈ version 25-L
	BudgetURLPractical  = 8000 // shared-link surfaces degrade beyond a few KB
)

// ErrorKind classifies a teleport decode/encode failure.
type ErrorKind string

const (
	KindOversize        ErrorKind = "Oversize"
	KindInvalidFormat   ErrorKind = "InvalidFormat"
	KindInvalidJSON     ErrorKind = "InvalidJson"
	KindInvalidEnvelope ErrorKind = "InvalidEnvelope"
	KindUnsupportedVer  ErrorKind = "UnsupportedVersion"
	KindDigestMismatch  ErrorKind = "DigestMismatch"
	KindTreeDecode      ErrorKind = "TreeDecode"
	KindHistoryDecode   ErrorKind = "HistoryDecode"
	KindTreeInvalid     ErrorKind = "TreeInvalid"
)

// Error is a typed, recoverable teleport failure.
type Error struct {
	Kind    ErrorKind
	Message string
}

func (e *Error) Error() string { return string(e.Kind) + ": " + e.Message }

func tErr(kind ErrorKind, message string) *Error { return &Error{Kind: kind, Message: message} }

// Bundle is a decoded teleport state bundle: the live tree, its state map, an
// optional bounded op-history window (newest-last, provenance only — resume
// does not re-apply it), and the optional op-chain head hash.
type Bundle struct {
	Tree      wire.Node
	State     map[string]wire.Value // omit when empty
	History   []wire.Obj            // omit when empty
	ChainHead *string               // omit when absent
}

// ── canonical envelope build ────────────────────────────────────────────────

// envelopeFields builds the envelope's canonical fields WITHOUT the digest (the
// digest pre-image). "bundle" + "tree" are always present; state / history /
// chainHead are omitted when empty.
func (b Bundle) envelopeFields() map[string]wire.Value {
	fields := map[string]wire.Value{
		"bundle": wire.Str(bundleVersion),
		"tree":   b.Tree,
	}
	if len(b.State) > 0 {
		state := make(map[string]wire.Value, len(b.State))
		for k, v := range b.State {
			state[k] = v
		}
		fields["state"] = wire.Obj{Fields: state}
	}
	if len(b.History) > 0 {
		history := make(wire.Arr, len(b.History))
		for i, op := range b.History {
			history[i] = op
		}
		fields["history"] = history
	}
	if b.ChainHead != nil {
		fields["chainHead"] = wire.Str(*b.ChainHead)
	}
	return fields
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// Encode serialises a bundle to its FT1 teleport string. Deterministic: the
// same bundle produces the same string.
func Encode(b Bundle) (string, error) {
	fields := b.envelopeFields()
	preimage, err := wire.EncodeValue(wire.Obj{Fields: fields})
	if err != nil {
		return "", err
	}
	fields["digest"] = wire.Str(sha256Hex(digestPrefix + preimage))
	envelopeJSON, err := wire.EncodeValue(wire.Obj{Fields: fields})
	if err != nil {
		return "", err
	}
	compressed, err := deflateBytes([]byte(envelopeJSON))
	if err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(compressed), nil
}

// EncodeWithin encodes a bundle and refuses (Oversize) if the result exceeds
// budget characters — the §17.5 QR/URL guard.
func EncodeWithin(b Bundle, budget int) (string, error) {
	s, err := Encode(b)
	if err != nil {
		return "", err
	}
	if len(s) > budget {
		return "", tErr(KindOversize, "encoded bundle exceeds the budget")
	}
	return s, nil
}

// ── decode → validate → resume (§17.4) ──────────────────────────────────────

// Decode runs the §17.4 pipeline in order, surfacing each failure as a typed
// *Error: size gate → unwrap → envelope shape/version → digest → wire decode →
// pre-emit validation → state re-seat.
func Decode(s string) (Bundle, error) {
	// 1 — size gate.
	if len(s) > maxInput {
		return Bundle{}, tErr(KindOversize, "encoded input exceeds the size gate")
	}
	// 2 — unwrap.
	if len(s) < len(prefix) || s[:len(prefix)] != prefix {
		return Bundle{}, tErr(KindInvalidFormat, "missing FT1. prefix")
	}
	compressed, err := base64.RawURLEncoding.DecodeString(s[len(prefix):])
	if err != nil {
		return Bundle{}, tErr(KindInvalidFormat, "invalid base64url")
	}
	inflated, err := inflateBytes(compressed)
	if err != nil {
		return Bundle{}, err // Oversize (bomb) / InvalidFormat
	}
	if !utf8.Valid(inflated) {
		return Bundle{}, tErr(KindInvalidFormat, "inflated payload is not valid UTF-8")
	}
	raw, perr := wire.ParseCanonical(string(inflated))
	if perr != nil {
		return Bundle{}, tErr(KindInvalidJSON, "envelope is not valid JSON")
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		return Bundle{}, tErr(KindInvalidEnvelope, "envelope is not an object")
	}
	// 3 — envelope shape + version.
	bundleTag, ok := obj["bundle"].(string)
	if !ok {
		return Bundle{}, tErr(KindInvalidEnvelope, "missing 'bundle' version")
	}
	if bundleTag != bundleVersion {
		return Bundle{}, tErr(KindUnsupportedVer, "unsupported bundle version '"+bundleTag+"'")
	}
	digest, ok := obj["digest"].(string)
	if !ok || !isHex64(digest) {
		return Bundle{}, tErr(KindInvalidEnvelope, "missing/invalid 'digest'")
	}
	if _, ok := obj["tree"]; !ok {
		return Bundle{}, tErr(KindInvalidEnvelope, "missing 'tree'")
	}
	// 4 — digest verification (before any payload decode).
	minusDigest := make(map[string]wire.Value, len(obj))
	for k, v := range obj {
		if k != "digest" {
			minusDigest[k] = wire.ValueFromParsed(v)
		}
	}
	preimage, err := wire.EncodeValue(wire.Obj{Fields: minusDigest})
	if err != nil {
		return Bundle{}, tErr(KindInvalidEnvelope, "cannot render the digest pre-image")
	}
	if sha256Hex(digestPrefix+preimage) != digest {
		return Bundle{}, tErr(KindDigestMismatch, "digest does not verify — the bundle was tampered or corrupted")
	}
	// 5 — standard wire decode of tree + each history op.
	tree, terr := wire.DecodeNodeValue(obj["tree"])
	if terr != nil {
		return Bundle{}, tErr(KindTreeDecode, terr.Error())
	}
	var history []wire.Obj
	if rawHistory, ok := obj["history"].([]any); ok {
		for _, item := range rawHistory {
			op, herr := wire.DecodeOpValue(item)
			if herr != nil {
				return Bundle{}, tErr(KindHistoryDecode, herr.Error())
			}
			history = append(history, op)
		}
	}
	// 6 — pre-emit validation (node-identity defects refuse).
	for _, f := range validator.ValidateNode(tree) {
		if f.Code == "EMPTY_NODE_ID" || f.Code == "DUPLICATE_NODE_ID" {
			return Bundle{}, tErr(KindTreeInvalid, "tree has a node-identity defect: "+f.Message)
		}
	}
	// 7 — state re-seat.
	var state map[string]wire.Value
	if rawState, ok := obj["state"].(map[string]any); ok {
		state = make(map[string]wire.Value, len(rawState))
		for k, v := range rawState {
			state[k] = wire.ValueFromParsed(v)
		}
	}
	var chainHead *string
	if ch, ok := obj["chainHead"].(string); ok {
		chainHead = &ch
	}
	return Bundle{Tree: tree, State: state, History: history, ChainHead: chainHead}, nil
}

func isHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// ── deflate helpers (raw RFC 1951) ──────────────────────────────────────────

func deflateBytes(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, flate.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func inflateBytes(data []byte) ([]byte, error) {
	r := flate.NewReader(bytes.NewReader(data))
	defer r.Close()
	out, err := io.ReadAll(io.LimitReader(r, maxInflate+1))
	if err != nil {
		return nil, tErr(KindInvalidFormat, "inflate failed")
	}
	if len(out) > maxInflate {
		return nil, tErr(KindOversize, "inflated output exceeds the decompression cap (bomb)")
	}
	return out, nil
}
