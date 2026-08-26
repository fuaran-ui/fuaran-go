package wire

import (
	"strings"
	"testing"
)

// Phase 687 — the CLOSE of the migration window Phase 681 opened.
//
// 0.4.0 removed the ordinal from InsertChild and MoveNode: both append, and
// ReorderChildren states order by naming child ids. Through the window every
// decoder ACCEPTED AND IGNORED a legacy `position` / `newPosition` so the hosts
// could adopt independently. Every host is now positionless and no emitter
// produces the field, so the tolerance is withdrawn: it is a decode error,
// named at its own path.
//
// The corpus fixtures (reject-op-insertchild-retired-position,
// reject-op-movenode-retired-newposition) certify code + path. These add what
// the corpus deliberately does not assert for op-side rejects — the didactic
// text — and the cross-host ORDERING guarantee, which no single fixture can
// express because its payload is otherwise well-formed by design.

func TestRetiredPositionRefusedByName(t *testing.T) {
	for _, tc := range []struct {
		name     string
		input    string
		wantPath string
	}{
		{
			name:     "InsertChild carries the retired position",
			input:    `{"$type":"InsertChild","child":{"id":"n","kind":{"$type":"Markdown","text":"x"}},"parentId":"p","position":3}`,
			wantPath: "$.position",
		},
		{
			name:     "MoveNode carries the retired newPosition",
			input:    `{"$type":"MoveNode","newParentId":"q","newPosition":2,"target":"n"}`,
			wantPath: "$.newPosition",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeOp(tc.input)
			if err == nil {
				t.Fatalf("the retired field was accepted — the migration window is closed")
			}
			de, ok := err.(*DecodeError)
			if !ok {
				t.Fatalf("want a *DecodeError, got %T", err)
			}
			if de.Code != CodeWrongType {
				t.Errorf("code: got %v, want %v", de.Code, CodeWrongType)
			}
			if de.Path != tc.wantPath {
				t.Errorf("path: got %q, want %q — the error must name the retired field", de.Path, tc.wantPath)
			}
			// The didactic names what to reach for instead. A refusal that only
			// says "no" sends the author looking for a spelling.
			if !strings.Contains(de.Message, "ReorderChildren") {
				t.Errorf("message does not name ReorderChildren: %q", de.Message)
			}
		})
	}
}

// The retired field is named AHEAD of any other defect in the same op. Without
// this ordering an author who also omitted a required field would fix that and
// meet this one only on the next run. Fixed identically across all five hosts.
func TestRetiredPositionOutranksMissingRequiredField(t *testing.T) {
	_, err := DecodeOp(`{"$type":"InsertChild","position":0}`)
	if err == nil {
		t.Fatal("an op missing parentId AND carrying position decoded")
	}
	de, ok := err.(*DecodeError)
	if !ok {
		t.Fatalf("want a *DecodeError, got %T", err)
	}
	if de.Path != "$.position" {
		t.Errorf("path: got %q, want %q — the retired field wins over the missing required field", de.Path, "$.position")
	}
}

// The positionless form still decodes and re-encodes as the identity.
func TestPositionlessFormRoundTrips(t *testing.T) {
	const current = `{"$type":"MoveNode","newParentId":"q","target":"n"}`
	op, err := DecodeOp(current)
	if err != nil {
		t.Fatalf("canonical MoveNode refused: %v", err)
	}
	got, err := EncodeOp(op)
	if err != nil {
		t.Fatalf("re-encode failed: %v", err)
	}
	if got != current {
		t.Errorf("decode -> re-encode is not the identity: got %q, want %q", got, current)
	}
}
