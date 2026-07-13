package dag

import (
	"testing"

	"github.com/fuaran-ui/fuaran-go/wire"
)

func TestDagRecordRoundTrip(t *testing.T) {
	// A two-parent merge node with an outcome hash + prompt id exercises every
	// optional field and the Ordinal key order.
	in := `{"hash":"h3","op":{"$type":"RemoveNode","target":"x"},` +
		`"outcomeHash":"o1","parents":["h1","h2"],"promptId":"p-9",` +
		`"resultEnvelope":{"$type":"Success"},"streamId":"s","timestamp":1700000000,` +
		`"tombstoned":false,"userId":"u"}`
	record, err := DecodeDagRecord(in)
	if err != nil {
		t.Fatalf("DecodeDagRecord: %v", err)
	}
	if len(record.Parents) != 2 || record.OutcomeHash == nil || *record.OutcomeHash != "o1" {
		t.Errorf("decoded record wrong: %+v", record)
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
	in := `{"hash":"h","op":{"$type":"RemoveNode","target":"x"},"parents":[],` +
		`"resultEnvelope":{"$type":"Failure","code":"E","message":"m"},` +
		`"streamId":"s","timestamp":1,"tombstoned":true,"userId":"u"}`
	record, err := DecodeDagRecord(in)
	if err != nil {
		t.Fatalf("DecodeDagRecord: %v", err)
	}
	if record.Result.Kind != "Failure" || record.Result.Code != "E" || !record.Tombstoned {
		t.Errorf("failure envelope not decoded: %+v", record.Result)
	}
	if got, _ := EncodeDagRecord(record); got != in {
		t.Errorf("round trip diverged:\n got %s\nwant %s", got, in)
	}

	// A missing required field is a MISSING_FIELD decode error.
	_, err = DecodeDagRecord(`{"hash":"h","parents":[],"streamId":"s","timestamp":1,"userId":"u"}`)
	var de *wire.DecodeError
	if !asDecodeError(err, &de) || de.Code != wire.CodeMissingField || de.Path != "$.op" {
		t.Errorf("expected MISSING_FIELD at $.op, got %v", err)
	}
}

func asDecodeError(err error, target **wire.DecodeError) bool {
	de, ok := err.(*wire.DecodeError)
	if ok {
		*target = de
	}
	return ok
}
