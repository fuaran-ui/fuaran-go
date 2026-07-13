package wire

import (
	"strconv"
	"strings"

	"github.com/fuaran-ui/fuaran-go/canonical"
)

// The §15 wire versioning envelope. An artefact may be wrapped as
// {"$payload":<Node|TreeOp>,"$profile":"<name>@<major>.<minor>"}. A consumer
// negotiates the authored profile against its own core@1.0:
//
//   - Current  (same name+major, authored minor ≤ ours) — decode fully.
//   - Behind   (same name+major, authored minor > ours)  — tolerate: an unknown
//     kind becomes a transport-only preserved payload whose verbatim bytes
//     re-encode identically (must-ignore-but-preserve).
//   - Foreign  (different name, or different major)       — hard-refuse
//     (FOREIGN_PROFILE) — never silently mis-decode.
//
// The transport-only preserve is decode-only: there is no encoder entry point
// that mints an unknown kind, so the closed authoring surface stays intact.

// HostProfile is the profile this host implements.
const HostProfile = "core@1.0"

// Negotiation is the outcome of comparing an authored profile against the host.
type Negotiation string

const (
	// NegotiationCurrent — the host is at or ahead of the authored profile.
	NegotiationCurrent Negotiation = "Current"
	// NegotiationBehind — the host is behind (authored minor ahead); tolerate.
	NegotiationBehind Negotiation = "Behind"
	// NegotiationForeign — an incompatible namespace / major; refuse.
	NegotiationForeign Negotiation = "Foreign"
)

// Envelope is a decoded §15 versioned artefact: the payload (a decoded Node for
// Current, or the verbatim-preserved value for a Behind unknown kind) and the
// authored profile + the negotiation outcome.
type Envelope struct {
	Payload     Value
	Profile     string
	Negotiation Negotiation
}

func parseProfile(p string) (name string, major, minor int, ok bool) {
	at := strings.IndexByte(p, '@')
	if at <= 0 {
		return "", 0, 0, false
	}
	name = p[:at]
	ver := p[at+1:]
	dot := strings.IndexByte(ver, '.')
	if dot < 0 {
		return "", 0, 0, false
	}
	maj, e1 := strconv.Atoi(ver[:dot])
	minr, e2 := strconv.Atoi(ver[dot+1:])
	if e1 != nil || e2 != nil {
		return "", 0, 0, false
	}
	return name, maj, minr, true
}

// Negotiate compares an authored profile against the host's core@1.0.
func Negotiate(profile string) Negotiation {
	name, major, minor, ok := parseProfile(profile)
	if !ok || name != "core" || major != 1 {
		return NegotiationForeign
	}
	if minor <= 0 {
		return NegotiationCurrent
	}
	return NegotiationBehind
}

// DecodeEnvelope decodes a §15 versioned artefact: negotiate the profile, then
// decode the payload (Current → strict node decode; Behind → tolerate an
// unknown kind by preserving it verbatim). A Foreign profile returns a
// *DecodeError with CodeForeignProfile at $.$profile.
func DecodeEnvelope(canonicalJSON string) (env Envelope, err error) {
	raw, perr := ParseCanonical(canonicalJSON)
	if perr != nil {
		return Envelope{}, perr
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		return Envelope{}, &DecodeError{Code: CodeWrongType, Path: "$", Message: "expected an object at $"}
	}
	rawProfile, ok := obj["$profile"]
	if !ok {
		return Envelope{}, &DecodeError{Code: CodeMissingField, Path: "$.$profile", Message: "missing required field '$profile'"}
	}
	profile, ok := rawProfile.(string)
	if !ok {
		return Envelope{}, &DecodeError{Code: CodeWrongType, Path: "$.$profile", Message: "$profile must be a string"}
	}
	payloadRaw, ok := obj["$payload"]
	if !ok {
		return Envelope{}, &DecodeError{Code: CodeMissingField, Path: "$.$payload", Message: "missing required field '$payload'"}
	}

	negotiation := Negotiate(profile)
	if negotiation == NegotiationForeign {
		return Envelope{}, &DecodeError{
			Code: CodeForeignProfile, Path: "$.$profile",
			Message: "foreign profile '" + profile + "' — a different namespace or major version, hard-refused",
		}
	}

	var payload Value
	node, decErr := DecodeNodeValue(payloadRaw)
	switch {
	case decErr == nil:
		payload = node
	case negotiation == NegotiationBehind && isUnknownKind(decErr):
		// Must-ignore-but-preserve: an unknown kind a behind consumer meets is
		// preserved verbatim, so re-encoding reproduces the producer's bytes.
		payload = ValueFromParsed(payloadRaw)
	default:
		return Envelope{}, rerootPayloadErr(decErr)
	}
	return Envelope{Payload: payload, Profile: profile, Negotiation: negotiation}, nil
}

func isUnknownKind(err error) bool {
	de, ok := err.(*DecodeError)
	return ok && de.Code == CodeWrongNodeKind
}

func rerootPayloadErr(err error) error {
	de, ok := err.(*DecodeError)
	if !ok {
		return err
	}
	return &DecodeError{Code: de.Code, Path: "$.$payload" + de.Path[1:], Message: de.Message, ExpectedShape: de.ExpectedShape}
}

// EncodeEnvelope re-encodes an envelope to canonical wire JSON:
// {"$payload":<payload>,"$profile":<profile>} ("$payload" sorts before
// "$profile" under the Ordinal key order).
func EncodeEnvelope(env Envelope) (string, error) {
	payloadJSON, err := EncodeValue(env.Payload)
	if err != nil {
		return "", err
	}
	return `{"$payload":` + payloadJSON + `,"$profile":` + canonical.EscapeString(env.Profile) + "}", nil
}
