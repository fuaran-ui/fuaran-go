package wire

import (
	"fmt"
	"strings"

	"github.com/fuaran-ui/fuaran-go/canonical"
)

// The row feed's two encode refusals (WIRE_FORMAT.md §5 "The two encode
// refusals", Phase 1960). A DataGrid / Chart `source` payload is a list of row
// objects; a cell holding an Int outside ±2^53 has no JSON number that holds it
// exactly, so it is REFUSED with a typed RowCellRefusedError naming the cell
// rather than written as the nearest double, a different number. Every other
// position keeps rule 5's floor (appendInt), which the specification states for
// payloads that are not rows. The wire model holds no date value, so the other
// refusal, a date-time naming no instant, cannot reach this encoder.

// ExactIntegerLimit is 2^53: every integer in [-2^53, 2^53] is exactly a double.
const ExactIntegerLimit = int64(1) << 53

// RowCellRefusedError names a row cell the row-feed encoder refuses: its code,
// the row's zero-based position in the feed, and the cell's key.
type RowCellRefusedError struct {
	Code  string
	Row   int
	Key   string
	Value int64
}

func (e *RowCellRefusedError) Error() string {
	return fmt.Sprintf("row %d, cell '%s': the integer %d is outside +/-2^53, so no JSON number holds it exactly; carry it as a string (%s)",
		e.Row, e.Key, e.Value, e.Code)
}

// checkRowPayload refuses the first out-of-range Int cell of a row payload, in
// feed order then Ordinal key order. A payload that is not a list of rows (the
// legacy "<opaque>" sentinel) carries no cell to refuse.
func checkRowPayload(payload Value) error {
	rows, ok := payload.(Arr)
	if !ok {
		return nil
	}
	for i, r := range rows {
		row, ok := r.(Obj)
		if !ok {
			continue
		}
		keys := make([]string, 0, len(row.Fields))
		for k := range row.Fields {
			keys = append(keys, k)
		}
		canonical.SortKeys(keys)
		for _, k := range keys {
			if n, ok := row.Fields[k].(Int); ok && (int64(n) > ExactIntegerLimit || int64(n) < -ExactIntegerLimit) {
				return &RowCellRefusedError{Code: "ROW_CELL_INEXACT_INTEGER", Row: i, Key: k, Value: int64(n)}
			}
		}
	}
	return nil
}

// checkRowFeed walks a `source` binding to the row payloads it carries: a
// Static's value, a State's defaultValue, and a Local's initialFrom.
func checkRowFeed(binding Value) error {
	b, ok := binding.(Obj)
	if !ok {
		return nil
	}
	switch b.Tag {
	case "Static":
		return checkRowPayload(b.Fields["value"])
	case "State":
		return checkRowPayload(b.Fields["defaultValue"])
	case "Local":
		return checkRowFeed(b.Fields["initialFrom"])
	}
	return nil
}

// isRowFeedKind reports whether a kind object's `source` is a row feed.
func isRowFeedKind(o Obj) bool { return o.Tag == "DataGrid" || o.Tag == "Chart" }

// EncodeRows renders a row feed's canonical bytes, as a DataGrid / Chart
// `source` writes them, or the RowCellRefusedError for its first refused cell.
func EncodeRows(rows Arr) (string, error) {
	if err := checkRowPayload(rows); err != nil {
		return "", err
	}
	var sb strings.Builder
	if err := appendValue(&sb, rows); err != nil {
		return "", err
	}
	return sb.String(), nil
}
