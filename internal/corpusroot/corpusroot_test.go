package corpusroot

import (
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func corpusDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"fixtures":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestResolveOrder(t *testing.T) {
	named, other, empty := corpusDir(t), corpusDir(t), t.TempDir()

	// A walk start with a sibling corpus beside a fake repository.
	estate := t.TempDir()
	sibling := filepath.Join(estate, DirName)
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sibling, "manifest.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(estate, "fuaran-go", "conformance")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name, explicit string
		env            map[string]string
		start, want    string
	}{
		{"nothing named walks to the sibling", "", nil, repo, sibling},
		{"an empty variable counts as unset", "", map[string]string{EnvVar: "  "}, repo, sibling},
		{"the variable is honoured", "", map[string]string{EnvVar: named}, repo, named},
		{"an explicit argument beats the variable", other, map[string]string{EnvVar: empty}, repo, other},
		{"nothing named and nothing found is a standalone checkout", "", nil, empty, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Resolve(c.explicit, env(c.env), c.start)
			if err != nil {
				t.Fatalf("unexpected refusal: %v", err)
			}
			if got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestResolveRefusesANamedRootHoldingNoCorpus(t *testing.T) {
	empty := t.TempDir()
	repo := t.TempDir()
	_, err := Resolve("", env(map[string]string{EnvVar: empty}), repo)
	if err == nil || !strings.Contains(err.Error(), EnvVar) || !strings.Contains(err.Error(), filepath.Base(empty)) {
		t.Fatalf("want a refusal naming %s and the path, got %v", EnvVar, err)
	}
	if _, err := Resolve(empty, env(nil), repo); err == nil {
		t.Fatal("an explicit argument naming no corpus must be refused")
	}
}

// moduleRoot is the directory holding go.mod.
func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..")
}

// alteredCopy copies the real corpus to scratch and changes ONE round-trip
// fixture's expected bytes, returning the copy and the fixture id.
func alteredCopy(t *testing.T, real string) (string, string) {
	t.Helper()
	dst := filepath.Join(t.TempDir(), DirName)
	err := filepath.WalkDir(real, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(real, path)
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), raw, 0o644)
	})
	if err != nil {
		t.Fatalf("copying the corpus: %v", err)
	}
	var m struct {
		Fixtures []struct{ ID, Kind, ExpectedFile string } `json:"fixtures"`
	}
	raw, err := os.ReadFile(filepath.Join(dst, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for _, fx := range m.Fixtures {
		if fx.Kind != "node-round-trip" || fx.ExpectedFile == "" {
			continue
		}
		path := filepath.Join(dst, filepath.FromSlash(fx.ExpectedFile))
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(strings.Replace(string(body), "{", "{ ", 1)), 0o644); err != nil {
			t.Fatal(err)
		}
		return dst, fx.ID
	}
	t.Fatal("the corpus holds no node round-trip fixture")
	return "", ""
}

// runRoundTrip runs the conformance package's round-trip leg in a child
// process with FUARAN_WIRE_FIXTURES set to corpus.
func runRoundTrip(t *testing.T, corpus string) (string, error) {
	t.Helper()
	cmd := exec.Command("go", "test", "./conformance", "-run", "^TestRoundTrip$", "-count=1")
	cmd.Dir = moduleRoot(t)
	cmd.Env = append(os.Environ(), EnvVar+"="+corpus)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestConformanceLegsCertifyTheNamedCorpus is the end-to-end proof: the real
// round-trip leg, pointed at a scratch corpus with one fixture changed, fails
// on exactly that fixture; pointed at a directory holding no corpus, it fails
// with the refusal rather than certifying the sibling clone.
func TestConformanceLegsCertifyTheNamedCorpus(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns go test; skipped under -short")
	}
	wd, _ := os.Getwd()
	real, err := Resolve("", os.Getenv, wd)
	if err != nil {
		t.Fatal(err)
	}
	if real == "" {
		t.Skip("wire-format-fixtures corpus not found alongside the repo; skipping (standalone checkout)")
	}

	altered, id := alteredCopy(t, real)
	out, err := runRoundTrip(t, altered)
	if err == nil || !strings.Contains(out, id) {
		t.Fatalf("the round-trip leg did not see %s changed in the corpus %s names (err=%v):\n%s", id, EnvVar, err, out)
	}

	out, err = runRoundTrip(t, t.TempDir())
	if err == nil || !strings.Contains(out, "does not name a conformance corpus") {
		t.Fatalf("the round-trip leg did not refuse a %s naming no corpus (err=%v):\n%s", EnvVar, err, out)
	}
}
