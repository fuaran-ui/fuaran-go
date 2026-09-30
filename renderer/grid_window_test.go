package renderer

// The DataGrid row window and the declared total (WIRE_FORMAT.md, "Row window
// and declared total"; the contract is Phase 1892, this host's adoption Phase
// 1912).
//
// Two halves. The corpus half runs the grid-window/ behaviour vectors — whose
// expected answers the corpus computes from the specification's rules, not from
// any host — through THIS host's own grid sort, page slice, descriptor reader
// and window function, exactly as the family's description prescribes. The
// render half pins what the vectors cannot reach: the ARIA annotations, the
// declared-total pager, and that a grid naming no window key is unchanged.
//
// Why the render half lives HERE and not in render-fidelity.json (decided in
// Phase 1919): the shared fidelity table's DataGrid entry carries no window
// obligation, deliberately. The specification states the annotations as a
// SHOULD, and the table's obligations are closed-vocabulary claims every host
// that renders the kind must assert or be red over; the rows and the total —
// the part every host MUST agree on — are already certified cross-host by the
// grid-window/ vectors above. Promoting the annotations to an obligation is a
// specification change first (SHOULD to MUST), not a table edit.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/fuaran-ui/fuaran-go/wire"
)

// gridWindowCorpusEnvVar redirects the corpus root, the name every host's
// suite honours; unset, the corpus is found by a walk up from the package.
const gridWindowCorpusEnvVar = "FUARAN_WIRE_FIXTURES"

// findGridWindowCorpus returns the corpus root, or "" when there is none (a
// standalone checkout, where the leg skips). A declared root that holds no
// manifest is a misconfiguration and fails rather than skipping.
func findGridWindowCorpus(t *testing.T) string {
	t.Helper()
	if declared := os.Getenv(gridWindowCorpusEnvVar); declared != "" {
		if _, err := os.Stat(filepath.Join(declared, "manifest.json")); err != nil {
			t.Fatalf("%s=%q does not name a conformance corpus (no manifest.json under it)", gridWindowCorpusEnvVar, declared)
		}
		return declared
	}
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		candidate := filepath.Join(dir, "wire-format-fixtures")
		if _, err := os.Stat(filepath.Join(candidate, "manifest.json")); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// vectorValue lifts a JSON value into the wire model the way a host store
// holds one: an integer literal is an Int, any other number a Float.
func vectorValue(raw any) wire.Value {
	switch v := raw.(type) {
	case nil:
		return wire.Null{}
	case bool:
		return wire.Bool(v)
	case string:
		return wire.Str(v)
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return wire.Int(i)
		}
		f, _ := v.Float64()
		return wire.Float(f)
	case []any:
		out := make(wire.Arr, len(v))
		for i, item := range v {
			out[i] = vectorValue(item)
		}
		return out
	case map[string]any:
		tag, _ := v["$type"].(string)
		fields := make(map[string]wire.Value, len(v))
		for k, item := range v {
			if tag != "" && k == "$type" {
				continue
			}
			fields[k] = vectorValue(item)
		}
		return wire.Obj{Tag: tag, Fields: fields}
	}
	return nil
}

// gridWindowExpected is a vector's four answers; Total is nil (JSON null)
// where the range's size is unknown.
type gridWindowExpected struct {
	Windowed bool     `json:"windowed"`
	Offset   int      `json:"offset"`
	RowIDs   []string `json:"rowIds"`
	Total    *int     `json:"total"`
}

type gridWindowVector struct {
	ID       string             `json:"id"`
	Input    map[string]any     `json:"input"`
	Expected gridWindowExpected `json:"expected"`
}

// runGridWindowVector takes one vector through this host's own pieces, in the
// family's order: sort, then (client slicing only) page, then the window.
func runGridWindowVector(t *testing.T, input map[string]any) presentedWindow {
	t.Helper()
	slicing, _ := input["slicing"].(string)
	var columns []wire.Obj
	for _, c := range input["columns"].([]any) {
		columns = append(columns, wire.Obj{Fields: map[string]wire.Value{"field": wire.Str(c.(string))}})
	}
	rows := gridObjRows(vectorValue(input["rows"]).(wire.Arr))
	if sortSpec, ok := input["sort"].(map[string]any); ok {
		direction, _ := sortSpec["direction"].(string)
		// sort.column is an index into input.columns.
		column, ok := gridJSONInt(vectorValue(sortSpec["column"]))
		if !ok || column < 0 || column >= len(columns) {
			t.Fatalf("sort column %v does not index the columns", sortSpec["column"])
		}
		rows = sortGridRows(columns, column, direction, rows)
	}
	if page, ok := input["page"].(map[string]any); ok && slicing == "client" {
		size, _ := gridJSONInt(vectorValue(page["size"]))
		number, _ := gridJSONInt(vectorValue(page["page"]))
		rows = sliceGridRowsToPage(size, number, rows)
	}
	// The raw descriptor and total go through the STATE read, as the renderer's do.
	values := map[string]wire.Value{}
	if w, ok := input["window"]; ok {
		values["vector-window"] = vectorValue(w)
	}
	if total, ok := input["rowTotal"]; ok {
		values["vector-total"] = vectorValue(total)
	}
	r := &renderer{sources: Sources(values)}
	hostWindows := slicing == "hostWindows"
	total, hasTotal := 0, false
	if hostWindows {
		total, hasTotal = r.resolveRowTotal(wire.Obj{Tag: "State", Fields: map[string]wire.Value{"key": wire.Str("vector-total")}})
	}
	descriptor, hasWindow := gridWindowOfValue(r.sources.Values["vector-window"])
	return presentGridWindow(hostWindows, total, hasTotal, descriptor, hasWindow, rows)
}

func TestGridWindowVectorsAgreeWithThisHostsWindowFunction(t *testing.T) {
	corpus := findGridWindowCorpus(t)
	if corpus == "" {
		t.Skipf("wire-format-fixtures corpus not found and %s is unset; "+
			"NO grid-window vector was certified in this run (standalone checkout)", gridWindowCorpusEnvVar)
	}
	raw, err := os.ReadFile(filepath.Join(corpus, "grid-window", "grid-window-vectors.json"))
	if err != nil {
		t.Fatalf("the corpus at %s holds no grid-window/grid-window-vectors.json - "+
			"a behaviour family cannot be certified by reading nothing: %v", corpus, err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var family struct {
		Vectors []gridWindowVector `json:"vectors"`
	}
	if err := dec.Decode(&family); err != nil {
		t.Fatalf("decoding the grid-window family: %v", err)
	}
	if len(family.Vectors) < 20 {
		t.Fatalf("the family carries its full vector set; read %d", len(family.Vectors))
	}
	for _, v := range family.Vectors {
		t.Run(v.ID, func(t *testing.T) {
			got := runGridWindowVector(t, v.Input)
			ids := make([]string, 0, len(got.rows))
			for _, row := range got.rows {
				id, _ := row.Fields["id"].(wire.Str)
				ids = append(ids, string(id))
			}
			if got.windowed != v.Expected.Windowed {
				t.Errorf("windowed = %v, want %v", got.windowed, v.Expected.Windowed)
			}
			if got.offset != v.Expected.Offset {
				t.Errorf("offset = %d, want %d", got.offset, v.Expected.Offset)
			}
			if strings.Join(ids, ",") != strings.Join(v.Expected.RowIDs, ",") {
				t.Errorf("rowIds = %v, want %v", ids, v.Expected.RowIDs)
			}
			switch {
			case v.Expected.Total == nil && got.hasTotal:
				t.Errorf("total = %d, want unknown", got.total)
			case v.Expected.Total != nil && !got.hasTotal:
				t.Errorf("total unknown, want %d", *v.Expected.Total)
			case v.Expected.Total != nil && got.total != *v.Expected.Total:
				t.Errorf("total = %d, want %d", got.total, *v.Expected.Total)
			}
		})
	}
}

// ─── The static renderer ──────────────────────────────────────────────────────

// windowGridRows is twelve rows r00..r11 with distinct scores.
func windowGridRows() string {
	parts := make([]string, 0, 12)
	for i := 0; i < 12; i++ {
		id := "r" + string(rune('0'+i/10)) + string(rune('0'+i%10))
		score := (i * 37) % 101
		parts = append(parts, `{"id":"`+id+`","score":`+strconv.Itoa(score)+`}`)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// windowGridJSON is a bound grid over those rows; extra is spliced into the
// kind as further members (a leading comma included by the caller's fragment).
func windowGridJSON(source, extra string) string {
	if source == "" {
		source = `{"$type":"State","defaultValue":` + windowGridRows() + `,"key":"rows"}`
	}
	return `{"id":"g","kind":{"$type":"DataGrid","columns":[` +
		`{"field":"id","kind":{"$type":"Text"},"label":"Id"},` +
		`{"field":"score","kind":{"$type":"Numeric"},"label":"Score"}],` +
		`"rowKeyField":"id","source":` + source + extra + `}}`
}

func renderWindowGrid(t *testing.T, doc string, values map[string]wire.Value) string {
	t.Helper()
	return renderHTML(t, mustDecode(t, doc), Sources(values))
}

func windowDescriptor(offset, count int) wire.Value {
	return wire.Obj{Fields: map[string]wire.Value{"offset": wire.Int(offset), "count": wire.Int(count)}}
}

var presentedRowIDs = regexp.MustCompile(`<td class="fuaran-grid-cell"><span>(r\d\d)</span>`)

func rowIDsOf(html string) []string {
	var out []string
	for _, m := range presentedRowIDs.FindAllStringSubmatch(html, -1) {
		out = append(out, m[1])
	}
	return out
}

func TestAnUnwindowedGridCarriesNoRowAnnotations(t *testing.T) {
	html := renderWindowGrid(t, windowGridJSON("", ""), nil)
	mustNotEmit(t, html, "aria-rowcount", "an unwindowed grid is unchanged")
	mustNotEmit(t, html, "aria-rowindex", "an unwindowed grid is unchanged")
	if got := len(rowIDsOf(html)); got != 12 {
		t.Errorf("presented %d rows, want 12", got)
	}
}

func TestAWindowKeyWithNoDescriptorRendersWhatTheGridRenderedBeforeSaveTheRange(t *testing.T) {
	// No descriptor is no window: every row, no annotation.
	html := renderWindowGrid(t, windowGridJSON("", `,"windowStateKey":"w"`), nil)
	if strings.Join(rowIDsOf(html), ",") != strings.Join(rowIDsOf(renderWindowGrid(t, windowGridJSON("", ""), nil)), ",") {
		t.Errorf("a window key with nothing held presents a different row set:\n%s", html)
	}
	mustNotEmit(t, html, "aria-rowindex", "no window is in effect")
}

func TestAClientWindowPresentsItsSliceWithRowAnnotations(t *testing.T) {
	html := renderWindowGrid(t, windowGridJSON("", `,"windowStateKey":"w"`),
		map[string]wire.Value{"w": windowDescriptor(4, 3)})
	mustEmit(t, html, `<table class="fuaran-grid" aria-rowcount="13">`, "the total plus the header row")
	if got := strings.Join(rowIDsOf(html), ","); got != "r04,r05,r06" {
		t.Errorf("rows = %s, want r04,r05,r06", got)
	}
	mustEmit(t, html, `aria-rowindex="6"`, "the first presented row is range row 4, aria row 6")
	mustEmit(t, html, `aria-rowindex="8"`, "the last presented row is range row 6, aria row 8")
}

func TestAWindowRangesOverTheSortedRows(t *testing.T) {
	html := renderWindowGrid(t,
		windowGridJSON("", `,"defaultSort":{"column":1,"direction":"desc"},"windowStateKey":"w"`),
		map[string]wire.Value{"w": windowDescriptor(0, 1)})
	top, best := 0, -1
	for i := 0; i < 12; i++ {
		if s := (i * 37) % 101; s > best {
			top, best = i, s
		}
	}
	want := "r" + string(rune('0'+top/10)) + string(rune('0'+top%10))
	if got := strings.Join(rowIDsOf(html), ","); got != want {
		t.Errorf("rows = %s, want %s (the highest score first)", got, want)
	}
}

func TestAWindowWithinAClientPageHasAnInertPager(t *testing.T) {
	html := renderWindowGrid(t, windowGridJSON("", `,"pageSize":5,"pageStateKey":"p","windowStateKey":"w"`),
		map[string]wire.Value{
			"w": windowDescriptor(0, 2),
			"p": wire.Obj{Fields: map[string]wire.Value{"page": wire.Int(3)}},
		})
	if got := strings.Join(rowIDsOf(html), ","); got != "r10,r11" {
		t.Errorf("rows = %s, want r10,r11 (page 3 of 12 rows at 5 a page)", got)
	}
	mustEmit(t, html, `<div class="fuaran-grid-paged">`, "a paged grid carries the pager")
	mustEmit(t, html, `Page 3 of 3`, "a client-paged grid counts its own rows")
	if got := strings.Count(html, `disabled=""`); got != 2 {
		t.Errorf("%d disabled steps, want 2 (the static floor's pager is inert)", got)
	}
}

func pageRowsValue() wire.Value {
	rows := vectorValueFromJSON(windowGridRows()).(wire.Arr)
	return rows[:5]
}

func vectorValueFromJSON(s string) wire.Value {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var raw any
	if err := dec.Decode(&raw); err != nil {
		panic(err)
	}
	return vectorValue(raw)
}

func TestAHostPagedWindowedGridWithADeclaredTotalStatesAndClampsItsPageCount(t *testing.T) {
	html := renderWindowGrid(t,
		windowGridJSON(`{"$type":"Query","dependsOn":["p"],"name":"orders"}`,
			`,"pageSize":5,"pageStateKey":"p","rowTotal":{"$type":"Query","name":"orders.total"},"windowStateKey":"w"`),
		map[string]wire.Value{
			"orders":       pageRowsValue(),
			"orders.total": wire.Int(42),
			"p":            wire.Obj{Fields: map[string]wire.Value{"page": wire.Int(99)}},
		})
	mustEmit(t, html, "Page 9 of 9", "a declared total states and clamps the page count")
	if got := len(rowIDsOf(html)); got != 5 {
		t.Errorf("presented %d rows, want 5 (the host returned the page; the grid does not slice it again)", got)
	}
}

func TestAHostPagedWindowedGridWithoutADeclaredTotalKeepsToPreviousAndNext(t *testing.T) {
	html := renderWindowGrid(t,
		windowGridJSON(`{"$type":"Query","dependsOn":["p"],"name":"orders"}`,
			`,"pageSize":5,"pageStateKey":"p","windowStateKey":"w"`),
		map[string]wire.Value{
			"orders": pageRowsValue(),
			"p":      wire.Obj{Fields: map[string]wire.Value{"page": wire.Int(4)}},
		})
	mustEmit(t, html, ">Page 4<", "no declared total, no page count")
	mustNotEmit(t, html, " of ", "no declared total, no page count")
}

func TestAHostWindowedGridSlicesNothingAndReportsTheDeclaredTotal(t *testing.T) {
	html := renderWindowGrid(t,
		windowGridJSON(`{"$type":"Query","dependsOn":["w"],"name":"orders"}`,
			`,"rowTotal":{"$type":"Query","name":"orders.total"},"windowStateKey":"w"`),
		map[string]wire.Value{
			"orders":       pageRowsValue(),
			"orders.total": wire.Int(5000),
			"w":            windowDescriptor(100, 5),
		})
	mustEmit(t, html, `<table class="fuaran-grid" aria-rowcount="5001">`, "the declared total plus the header row")
	mustEmit(t, html, `aria-rowindex="102"`, "the host's window starts at range row 100")
	mustEmit(t, html, `aria-rowindex="106"`, "and holds five rows")
	if got := strings.Count(html, "aria-rowindex="); got != 5 {
		t.Errorf("%d annotated rows, want 5", got)
	}
}

func TestAHostWindowedGridWithNoDeclaredTotalReportsAnUnknownCount(t *testing.T) {
	html := renderWindowGrid(t,
		windowGridJSON(`{"$type":"Query","dependsOn":["w"],"name":"orders"}`, `,"windowStateKey":"w"`),
		map[string]wire.Value{"orders": pageRowsValue(), "w": windowDescriptor(20, 5)})
	mustEmit(t, html, `<table class="fuaran-grid" aria-rowcount="-1">`, "an unknown total is -1, never guessed")
}
