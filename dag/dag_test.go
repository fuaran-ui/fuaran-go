package dag

import (
	"sort"
	"testing"

	"github.com/fuaran-ui/fuaran-go/opstream"
	"github.com/fuaran-ui/fuaran-go/wire"
)

func TestDagRecordRoundTrip(t *testing.T) {
	// A two-parent merge node with an outcome hash + prompt id exercises every
	// optional field and the Ordinal key order. The agent actor is the
	// four-member case — the one whose PINNED (unsorted) member order a
	// key-sorting encoder would silently rewrite.
	in := `{"actor":{"kind":"agent","model":"claude","version":"4.8","id":"planner"},` +
		`"hash":"h3","op":{"$type":"RemoveNode","target":"x"},` +
		`"outcomeHash":"o1","parents":["h1","h2"],"promptId":"p-9",` +
		`"resultEnvelope":{"$type":"Success"},"streamId":"s","timestamp":1700000000,` +
		`"tombstoned":false}`
	record, err := DecodeDagRecord(in)
	if err != nil {
		t.Fatalf("DecodeDagRecord: %v", err)
	}
	if len(record.Parents) != 2 || record.OutcomeHash == nil || *record.OutcomeHash != "o1" {
		t.Errorf("decoded record wrong: %+v", record)
	}
	agent, ok := record.Actor.(opstream.AgentActor)
	if !ok || agent.Model != "claude" || agent.Version != "4.8" || agent.ID != "planner" {
		t.Errorf("actor not decoded as the typed agent: %+v", record.Actor)
	}
	got, err := EncodeDagRecord(record)
	if err != nil {
		t.Fatalf("EncodeDagRecord: %v", err)
	}
	if got != in {
		t.Errorf("round trip diverged:\n got %s\nwant %s", got, in)
	}
}

func TestDagRecordFailureEnvelopeAndMissingField(t *testing.T) {
	in := `{"actor":{"kind":"human","id":"u"},` +
		`"hash":"h","op":{"$type":"RemoveNode","target":"x"},"parents":[],` +
		`"resultEnvelope":{"$type":"Failure","code":"E","message":"m"},` +
		`"streamId":"s","timestamp":1,"tombstoned":true}`
	record, err := DecodeDagRecord(in)
	if err != nil {
		t.Fatalf("DecodeDagRecord: %v", err)
	}
	if record.Result.Kind != "Failure" || record.Result.Code != "E" || !record.Tombstoned {
		t.Errorf("failure envelope not decoded: %+v", record.Result)
	}
	if human, ok := record.Actor.(opstream.HumanActor); !ok || human.ID != "u" {
		t.Errorf("actor not decoded as the typed human: %+v", record.Actor)
	}
	if got, _ := EncodeDagRecord(record); got != in {
		t.Errorf("round trip diverged:\n got %s\nwant %s", got, in)
	}

	// A missing required field is a MISSING_FIELD decode error.
	_, err = DecodeDagRecord(`{"actor":{"kind":"human","id":"u"},"hash":"h","parents":[],` +
		`"streamId":"s","timestamp":1}`)
	var de *wire.DecodeError
	if !asDecodeError(err, &de) || de.Code != wire.CodeMissingField || de.Path != "$.op" {
		t.Errorf("expected MISSING_FIELD at $.op, got %v", err)
	}
}

// ─── The typed actor on the DAG record (Phase 1144 / 1168) ───────────────────
//
// These pin what the four curated `dag/` corpus fixtures do not reach. The
// corpus is the oracle for the BYTES; the refusal contract has no reject vector
// in that family, so it is pinned here.

// The top-level keys are Ordinal-sorted, which is what puts `actor` at the FRONT
// where the pre-1144 `userId` sat at the back. Pinned over a record carrying
// every optional member, so a future key that sorted ahead of `actor` fails here
// rather than shifting bytes silently.
func TestDagRecordTopLevelKeysAreOrdinalSorted(t *testing.T) {
	in := `{"actor":{"kind":"agent","model":"claude","version":"4.8","id":"planner"},` +
		`"hash":"h3","op":{"$type":"RemoveNode","target":"x"},"outcomeHash":"o1",` +
		`"parents":["h1","h2"],"promptId":"p-9",` +
		`"resultEnvelope":{"$type":"Failure","code":"E","message":"m"},` +
		`"streamId":"s","timestamp":1700000000,"tombstoned":true}`
	record, err := DecodeDagRecord(in)
	if err != nil {
		t.Fatalf("DecodeDagRecord: %v", err)
	}
	got, err := EncodeDagRecord(record)
	if err != nil {
		t.Fatalf("EncodeDagRecord: %v", err)
	}
	if got != in {
		t.Fatalf("round trip diverged:\n got %s\nwant %s", got, in)
	}

	keys := topLevelKeys(got)
	want := []string{"actor", "hash", "op", "outcomeHash", "parents", "promptId",
		"resultEnvelope", "streamId", "timestamp", "tombstoned"}
	if len(keys) != len(want) {
		t.Fatalf("top-level keys: got %v, want %v", keys, want)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("top-level key order: got %v, want %v", keys, want)
		}
	}
	if !sort.StringsAreSorted(keys) {
		t.Errorf("top-level keys are not Ordinal-sorted: %v", keys)
	}
}

// topLevelKeys returns the emitted object's depth-1 keys in emission order.
// Deliberately a scan of the BYTES rather than a re-parse: the property under
// test is what the encoder wrote, and a parser that sorted or reordered would
// hide exactly the defect this pins.
func topLevelKeys(encoded string) []string {
	var keys []string
	depth, inString, escaped, keyStart, atDepth1 := 0, false, false, 0, false
	for i := 0; i < len(encoded); i++ {
		c := encoded[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
				if atDepth1 {
					keys = append(keys, encoded[keyStart:i])
				}
				atDepth1 = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
			keyStart = i + 1
			atDepth1 = depth == 1 && i > 0 && (encoded[i-1] == '{' || encoded[i-1] == ',')
		case '{', '[':
			depth++
		case '}', ']':
			depth--
		}
	}
	return keys
}

// The actor's OWN members keep their pinned order (`kind` first, then the case
// fields) — deliberately NOT Ordinal-sorted, which would emit
// {"id":…,"kind":…,"model":…,"version":…} and break every host's bytes.
func TestNestedActorKeepsItsPinnedMemberOrder(t *testing.T) {
	for _, tc := range []struct{ name, actor string }{
		{"agent", `{"kind":"agent","model":"claude","version":"4.8","id":"planner"}`},
		{"human", `{"kind":"human","id":"u1"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := `{"actor":` + tc.actor +
				`,"hash":"h","op":{"$type":"RemoveNode","target":"x"},"parents":[],` +
				`"resultEnvelope":{"$type":"Success"},"streamId":"s","timestamp":1,` +
				`"tombstoned":false}`
			record, err := DecodeDagRecord(in)
			if err != nil {
				t.Fatalf("DecodeDagRecord: %v", err)
			}
			got, err := EncodeDagRecord(record)
			if err != nil {
				t.Fatalf("EncodeDagRecord: %v", err)
			}
			if got != in {
				t.Errorf("pinned actor member order not preserved:\n got %s\nwant %s", got, in)
			}
		})
	}
}

// A pre-1144 `userId` envelope is refused BY NAME, not lifted to a HumanActor.
// A lift would mint a record whose stored `hash` no host can reproduce, turning
// a clear refusal here into a silent verification failure downstream.
func TestPre1144UserIDEnvelopeIsRefusedByName(t *testing.T) {
	in := `{"hash":"h","op":{"$type":"RemoveNode","target":"x"},"parents":[],` +
		`"resultEnvelope":{"$type":"Success"},"streamId":"s","timestamp":1,` +
		`"tombstoned":false,"userId":"u1"}`
	_, err := DecodeDagRecord(in)
	var de *wire.DecodeError
	if !asDecodeError(err, &de) {
		t.Fatalf("expected a *wire.DecodeError, got %v", err)
	}
	if de.Code != wire.CodeMissingField || de.Path != "$.actor" {
		t.Fatalf("expected MISSING_FIELD at $.actor, got %s at %s", de.Code, de.Path)
	}
	if !contains(de.Message, "userId") || !contains(de.Message, "do not carry forward") {
		t.Errorf("the refusal must name the cause and the consequence, got: %s", de.Message)
	}
}

// `actor` is required outright: an envelope carrying neither `actor` nor the
// legacy `userId` is a plain missing-field refusal, held distinct from the
// pre-1144 one above so the two diagnoses stay tellable apart.
func TestRecordWithNoActorAtAllIsRefused(t *testing.T) {
	in := `{"hash":"h","op":{"$type":"RemoveNode","target":"x"},"parents":[],` +
		`"resultEnvelope":{"$type":"Success"},"streamId":"s","timestamp":1,` +
		`"tombstoned":false}`
	_, err := DecodeDagRecord(in)
	var de *wire.DecodeError
	if !asDecodeError(err, &de) {
		t.Fatalf("expected a *wire.DecodeError, got %v", err)
	}
	if de.Code != wire.CodeMissingField || de.Path != "$.actor" {
		t.Fatalf("expected MISSING_FIELD at $.actor, got %s at %s", de.Code, de.Path)
	}
	if contains(de.Message, "userId") {
		t.Errorf("must not blame userId when there was none: %s", de.Message)
	}
}

// Every malformed actor is NAMED, never defaulted — the actor is inside the
// reference host's content address, so a guessed one silently invalidates the
// record's own hash.
func TestMalformedActorIsNamedNeverDefaulted(t *testing.T) {
	cases := []struct {
		name  string
		actor string
		code  wire.DecodeErrorCode
		path  string
	}{
		{"not an object", `"u1"`, wire.CodeWrongType, "$.actor"},
		{"no discriminator", `{"id":"u1"}`, wire.CodeMissingField, "$.actor.kind"},
		{"kind outside the closed pair", `{"kind":"robot","id":"u1"}`, wire.CodeUnknownDUCase, "$.actor.kind"},
		{"human missing its field", `{"kind":"human"}`, wire.CodeMissingField, "$.actor.id"},
		{"agent missing a case field", `{"kind":"agent","model":"claude","id":"p"}`, wire.CodeMissingField, "$.actor.version"},
		{"case field of the wrong type", `{"kind":"human","id":7}`, wire.CodeWrongType, "$.actor.id"},
		{"kind of the wrong type", `{"kind":7,"id":"u1"}`, wire.CodeWrongType, "$.actor.kind"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := `{"actor":` + tc.actor +
				`,"hash":"h","op":{"$type":"RemoveNode","target":"x"},"parents":[],` +
				`"resultEnvelope":{"$type":"Success"},"streamId":"s","timestamp":1,` +
				`"tombstoned":false}`
			_, err := DecodeDagRecord(in)
			var de *wire.DecodeError
			if !asDecodeError(err, &de) {
				t.Fatalf("expected a *wire.DecodeError, got %v", err)
			}
			if de.Code != tc.code || de.Path != tc.path {
				t.Errorf("expected %s at %s, got %s at %s", tc.code, tc.path, de.Code, de.Path)
			}
		})
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func asDecodeError(err error, target **wire.DecodeError) bool {
	de, ok := err.(*wire.DecodeError)
	if ok {
		*target = de
	}
	return ok
}
