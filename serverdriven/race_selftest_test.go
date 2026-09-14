//go:build race

package serverdriven

// ── The race leg's own go-red proof ────────────────────────────────────────
//
// CI runs `go test -race` over this package because `serverdriven`'s
// connection-guarding tests were written for the detector: the property they
// hold — that two goroutines cannot both be inside a connection's sequence
// counter, replay buffer and writer — is one only the detector can observe.
// The assertions beside them are secondary instruments; they catch a race
// through its arithmetic, and arithmetic can come out right by luck.
//
// That leaves the leg with the defect every gate has until it is proven
// otherwise: a detector that is not actually enabled reports nothing and the
// job is green. `CGO_ENABLED` unset on the runner, `-race` dropped from the
// step, a toolchain built without the race runtime — each of those turns the
// leg into decoration, and nothing in the leg itself would say so.
//
// So this test removes the guard and watches the leg go red, on every run
// rather than once: it writes a scratch module holding the shape Connection
// guards — a sequence counter, an appended buffer and an unsynchronised sink,
// driven from many goroutines — and runs the child under `go test -race` twice,
// once WITHOUT the mutex and once WITH it. The unguarded run must fail naming a
// DATA RACE; the guarded run must pass. Red in both directions, which is the
// posture `cmd/conformance-residue -selftest` and `run.ps1`'s launcher
// self-test already take here.
//
// What it does NOT claim, and the distinction matters: it proves the detector
// on this runner is live and would report the unguarded shape. It does not
// prove that `Connection.mu` is what guards the real thing — that is what the
// H-36 tests in `transport_floor_test.go` assert, and this test is what makes
// their instrument trustworthy. The `//go:build race` constraint means it
// compiles only under the detector, so the ordinary test job never pays for it;
// `go vet -tags race ./...` type-checks it on a machine with no C toolchain,
// and `run.ps1` runs that.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// scratchModule is the child module's go.mod. No requirements, so the child
// build needs no network and no module cache beyond the toolchain's own.
const scratchModule = "module raceselftest\n\ngo 1.26\n"

// unguardedTest mirrors what Connection owns — the seq counter, the replay
// buffer and the write count — with the mutex deliberately absent.
const unguardedTest = `package scratch

import (
	"sync"
	"testing"
)

type conn struct {
	seq    int
	buffer []int
	writes int
}

func (c *conn) handle() {
	c.seq++
	c.buffer = append(c.buffer, c.seq)
	c.writes++
}

func TestUnguardedConnectionShape(t *testing.T) {
	c := &conn{}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 64; j++ {
				c.handle()
			}
		}()
	}
	close(start)
	wg.Wait()
	if c.seq != 32*64 {
		t.Errorf("seq = %d, want %d", c.seq, 32*64)
	}
}
`

// guardedTest is the same shape with the guard restored — the inverse pin. If
// this one also reported a race the proof above would be vacuous: it would show
// the detector reports everything, not that it discriminates.
const guardedTest = `package scratch

import (
	"sync"
	"testing"
)

type conn struct {
	mu     sync.Mutex
	seq    int
	buffer []int
	writes int
}

func (c *conn) handle() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seq++
	c.buffer = append(c.buffer, c.seq)
	c.writes++
}

func TestGuardedConnectionShape(t *testing.T) {
	c := &conn{}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 64; j++ {
				c.handle()
			}
		}()
	}
	close(start)
	wg.Wait()
	if c.seq != 32*64 {
		t.Errorf("seq = %d, want %d", c.seq, 32*64)
	}
}
`

// runScratchRace writes a one-file module and runs it under the race detector,
// returning the combined output and whether the child exited zero.
func runScratchRace(t *testing.T, goBin, source string) (string, bool) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(scratchModule), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scratch_test.go"), []byte(source), 0o644); err != nil {
		t.Fatalf("write scratch_test.go: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, goBin, "test", "-race", "-count=1", ".")
	cmd.Dir = dir
	// GOPROXY=off keeps the child honest about needing nothing; GOFLAGS is
	// cleared so an outer -run or -tags cannot leak into it.
	cmd.Env = append(os.Environ(), "CGO_ENABLED=1", "GOPROXY=off", "GOFLAGS=")
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("the scratch race run did not finish inside its timeout:\n%s", out)
	}
	return string(out), err == nil
}

func TestRaceLegGoesRedWhenTheGuardIsRemoved(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		// Not a skip: this test only compiles under -race, which means it is
		// running inside `go test`, which means a toolchain exists. Not finding
		// it is a broken environment, and a skip here would retire the proof.
		t.Fatalf("the go toolchain is not on PATH, so the race leg cannot prove itself: %v", err)
	}

	out, ok := runScratchRace(t, goBin, unguardedTest)
	if ok {
		t.Errorf("the unguarded connection shape passed under -race. The detector is not reporting, so this leg is decoration:\n%s", out)
	}
	if !strings.Contains(out, "DATA RACE") {
		t.Errorf("the unguarded run failed without naming a DATA RACE, so it failed for some other reason and proves nothing about the detector:\n%s", out)
	}

	clean, ok := runScratchRace(t, goBin, guardedTest)
	if !ok {
		t.Errorf("the GUARDED connection shape failed under -race; the proof above would be vacuous:\n%s", clean)
	}
	if strings.Contains(clean, "DATA RACE") {
		t.Errorf("the guarded shape was reported as a race, so the detector is not discriminating:\n%s", clean)
	}
}
