package styleobserver

import (
	tm "github.com/fuaran-ui/fuaran-go/thememanifest"
)

// The observer implementation — the PURE tier only.
//
// Headless boundary (deliberate, do not regress): the sibling Python/F# hosts
// also ship a BROWSER observer that reads LIVE getComputedStyle + layout metrics
// (Pyodide / Fable running client-side in the browser, with a live
// MutationObserver). That tier is out of a headless Go host's reach BY NATURE —
// there is no live DOM to read — so it is intentionally NOT ported. The Go host
// ships only the pure, substrate-free InMemoryStyleObserver: it consumes SUPPLIED
// resolved-style facts (a StyleInput per node), never a live DOM. Everything a
// browser observer would compute from live style is identical once the resolved
// colours are supplied — DeriveStyleFlags is the shared core — so the pure tier
// is the complete headless-meaningful surface.

// Subscriber receives (nodeID, observation) on each emission.
type Subscriber func(nodeID string, obs StyleObservation)

func withManifest(manifest *tm.ThemeManifest, obs StyleObservation) StyleObservation {
	if manifest == nil {
		return obs
	}
	obs.Flags = append(append([]StyleFlag{}, obs.Flags...), PerNodeFlags(*manifest, obs)...)
	return obs
}

type fixtureEntry struct {
	input  StyleInput
	parent *string
}

// InMemoryStyleObserver is the fixture-driven observer — substrate-free, for
// tests + non-browser hosts. It walks a parent-pointer graph for ObserveTree.
type subscription struct {
	id int
	fn Subscriber
}

type InMemoryStyleObserver struct {
	options     StyleObserverOptions
	manifest    *tm.ThemeManifest
	registry    map[string]fixtureEntry
	order       []string // registration order — deterministic BFS + children walk
	lastFlags   map[string][]StyleFlag
	subscribers []subscription
	nextSubID   int
}

// NewInMemoryStyleObserver constructs an observer with the given options and an
// optional manifest (nil for the manifest-free tier).
func NewInMemoryStyleObserver(options StyleObserverOptions, manifest *tm.ThemeManifest) *InMemoryStyleObserver {
	return &InMemoryStyleObserver{
		options:   options,
		manifest:  manifest,
		registry:  map[string]fixtureEntry{},
		lastFlags: map[string][]StyleFlag{},
	}
}

func (o *InMemoryStyleObserver) toObs(nodeID string, inp StyleInput) StyleObservation {
	return withManifest(o.manifest, ToStyleObservation(o.options, nodeID, inp))
}

func (o *InMemoryStyleObserver) emit(nodeID string, obs StyleObservation) {
	for _, sub := range o.subscribers {
		func(s Subscriber) {
			// A throwing subscriber must not poison its siblings.
			defer func() { _ = recover() }()
			s(nodeID, obs)
		}(sub.fn)
	}
}

// RegisterFixture registers or replaces a fixture; fires an initial emission
// unconditionally. A nil parent registers a root.
func (o *InMemoryStyleObserver) RegisterFixture(nodeID string, inp StyleInput, parent *string) {
	if _, exists := o.registry[nodeID]; !exists {
		o.order = append(o.order, nodeID)
	}
	o.registry[nodeID] = fixtureEntry{input: inp, parent: parent}
	obs := o.toObs(nodeID, inp)
	o.lastFlags[nodeID] = obs.Flags
	o.emit(nodeID, obs)
}

// Update replaces a registered node's input, honouring EmitOnFlagChangeOnly. A
// no-op if the node is absent.
func (o *InMemoryStyleObserver) Update(nodeID string, inp StyleInput) {
	existing, ok := o.registry[nodeID]
	if !ok {
		return
	}
	o.registry[nodeID] = fixtureEntry{input: inp, parent: existing.parent}
	obs := o.toObs(nodeID, inp)
	previous := o.lastFlags[nodeID]
	o.lastFlags[nodeID] = obs.Flags
	shouldEmit := true
	if o.options.EmitOnFlagChangeOnly {
		shouldEmit = !FlagsEqual(obs.Flags, previous)
	}
	if shouldEmit {
		o.emit(nodeID, obs)
	}
}

// Observe returns the current observation for a node, or nil if absent.
func (o *InMemoryStyleObserver) Observe(nodeID string) *StyleObservation {
	entry, ok := o.registry[nodeID]
	if !ok {
		return nil
	}
	obs := o.toObs(nodeID, entry.input)
	return &obs
}

// ObserveTree returns the root + every descendant in BFS, registration order.
func (o *InMemoryStyleObserver) ObserveTree(rootID string) []StyleObservation {
	if _, ok := o.registry[rootID]; !ok {
		return nil
	}
	children := map[string][]string{}
	for _, nodeID := range o.order {
		if p := o.registry[nodeID].parent; p != nil {
			children[*p] = append(children[*p], nodeID)
		}
	}
	var acc []StyleObservation
	queue := []string{rootID}
	for len(queue) > 0 {
		nodeID := queue[0]
		queue = queue[1:]
		if entry, ok := o.registry[nodeID]; ok {
			acc = append(acc, o.toObs(nodeID, entry.input))
		}
		queue = append(queue, children[nodeID]...)
	}
	return acc
}

// Subscribe registers a handler and returns an unsubscribe func.
func (o *InMemoryStyleObserver) Subscribe(handler Subscriber) func() {
	id := o.nextSubID
	o.nextSubID++
	o.subscribers = append(o.subscribers, subscription{id: id, fn: handler})
	return func() {
		for i, s := range o.subscribers {
			if s.id == id {
				o.subscribers = append(o.subscribers[:i], o.subscribers[i+1:]...)
				return
			}
		}
	}
}

// Register creates a baseline entry with no fixture so a mount hook does not crash.
func (o *InMemoryStyleObserver) Register(nodeID string) {
	if _, ok := o.registry[nodeID]; !ok {
		o.RegisterFixture(nodeID, BaselineStyleInput(), nil)
	}
}

// Unregister removes a node and its cached flags.
func (o *InMemoryStyleObserver) Unregister(nodeID string) {
	delete(o.registry, nodeID)
	delete(o.lastFlags, nodeID)
	for i, id := range o.order {
		if id == nodeID {
			o.order = append(o.order[:i], o.order[i+1:]...)
			break
		}
	}
}
