// pipeline.go — the capability pipeline's type check: a typed capability DAG
// declared as data, and the check a pipeline passes before anything runs. Twin
// of the reference's `CapabilityPipeline.typeCheck` and its wire reader (Core
// 0.43.0). This host runs no pipeline body; it checks one, which is what the
// shared capability-law vectors' `pipelineTypeCheck` case certifies.
//
// A pipeline is a list of nodes in declaration order: a `source` (a named data
// reference the host supplies, with its output space) or an `invoke` (a
// registered capability, the output space it declares, and its arguments, each
// a literal or an edge from an EARLIER node). The check is total and answers
// the first refusal in declaration order.
package function

import (
	"errors"
	"strconv"
	"unicode/utf16"
)

// The two node kinds and the two argument-source kinds, as their wire tags.
const (
	NodeSource       = "source"
	NodeInvoke       = "invoke"
	ArgFromLiteral   = "literal"
	ArgFromUpstream  = "fromNode"
	pipelineNodesKey = "nodes"
)

// ArgSource is where an invoke's argument comes from: a literal value (Kind
// ArgFromLiteral, Value set) or an earlier node's output (Kind ArgFromUpstream,
// Node set).
type ArgSource struct {
	Kind  string
	Value string
	Node  string
}

// PipelineArg binds one argument address of an invoke.
type PipelineArg struct {
	Addr   string
	Source ArgSource
}

// PipelineNode is one node: a source (DataRef set) or an invoke (CapabilityID
// and Args set). OutputType is the space the node declares it emits.
type PipelineNode struct {
	Kind         string
	ID           string
	DataRef      string
	CapabilityID string
	OutputType   *Space
	Args         []PipelineArg
}

// CapabilityPipeline is the pipeline document: its nodes in declaration order,
// which is the order they run in.
type CapabilityPipeline struct {
	Nodes []PipelineNode
}

// The closed set of pipeline refusals, as the reference names them.
const (
	ErrDuplicateNode             = "duplicateNode"
	ErrUnknownNode               = "unknownNode"
	ErrPipelineNoSuchCapability  = "pipelineNoSuchCapability"
	ErrPipelineArgRefused        = "pipelineArgRefused"
	ErrPipelineCycle             = "pipelineCycle"
	ErrEdgeTypeMismatch          = "edgeTypeMismatch"
	ErrPipelineForwardEdge       = "pipelineForwardEdge"
	ErrPipelineOutputContradicts = "pipelineOutputContradicts"
)

// PipelineError names why a pipeline does not type-check. Kind is one of the
// Err* pipeline constants; the other members are set as the kind uses them.
type PipelineError struct {
	Kind       string
	Node       string       // the node at fault; duplicateNode / unknownNode — the id
	Capability string       // pipelineNoSuchCapability / pipelineOutputContradicts
	Known      []string     // pipelineNoSuchCapability — the registered ids
	Arg        string       // edgeTypeMismatch / pipelineForwardEdge — the argument address
	Upstream   string       // pipelineForwardEdge — the later node the edge names
	Producer   string       // edgeTypeMismatch — the upstream output space's tag
	Consumer   string       // edgeTypeMismatch — the argument space's tag
	Cycle      []string     // pipelineCycle — the node ids around the cycle
	Reason     *InvokeError // pipelineArgRefused
	Declared   *Space       // pipelineOutputContradicts — the node's declared output
	Result     Returns      // pipelineOutputContradicts — what the capability answers
}

func (e *PipelineError) Error() string {
	switch e.Kind {
	case ErrPipelineArgRefused:
		return e.Kind + "(" + e.Node + "; " + e.Reason.Error() + ")"
	case ErrPipelineOutputContradicts:
		return e.Kind + "(" + e.Node + "; " + e.Capability + ")"
	case ErrPipelineNoSuchCapability:
		return e.Kind + "(" + e.Capability + ")"
	case ErrEdgeTypeMismatch:
		return e.Kind + "(" + e.Node + "." + e.Arg + "; " + e.Producer + " into " + e.Consumer + ")"
	case ErrPipelineForwardEdge:
		return e.Kind + "(" + e.Node + "." + e.Arg + "; " + e.Upstream + ")"
	default:
		return e.Kind + "(" + e.Node + ")"
	}
}

// SpaceSubsumes reports whether required admits every value available admits — is
// available a sub-space of required? The reference's `Space.subsumes` (Phase
// 295, DECISIONS D104), the relation the pipeline check asks: numeric ranges
// compare by bounds, and an int range widens into a float range its bounds
// contain; a string-length range admits a string-length range it bounds and an
// enum whose every member it bounds; an enum admits a subset enum; anyString
// admits every scalar space; an unconstrained tree space admits every tree
// space, a constrained one the same kind. Sound, and incomplete where the
// answer would need enumerating values.
//
// The function registry's spaceSubsumes (function.go) is the narrower relation
// the shared function-registry goldens certify across hosts, and is
// deliberately left as it is: moving FindBySignature onto this lattice changes
// what every host's registry matches, which is that family's decision to make.
func SpaceSubsumes(required, available *Space) bool {
	if required == nil || available == nil {
		return false
	}
	switch required.Kind {
	case "intRange":
		return available.Kind == "intRange" && required.Min <= available.Min && available.Max <= required.Max
	case "floatRange":
		return (available.Kind == "floatRange" || available.Kind == "intRange") &&
			required.Min <= available.Min && available.Max <= required.Max
	case "stringLen":
		switch available.Kind {
		case "stringLen":
			return required.Min <= available.Min && available.Max <= required.Max
		case "enum":
			for _, c := range available.Choices {
				// The length is the reference's: UTF-16 code units.
				n := float64(len(utf16.Encode([]rune(c))))
				if n < required.Min || n > required.Max {
					return false
				}
			}
			return true
		}
		return false
	case "enum":
		return available.Kind == "enum" && subset(available.Choices, required.Choices)
	case "anyString":
		switch available.Kind {
		case "intRange", "floatRange", "stringLen", "enum", "anyString":
			return true
		}
		return false
	case "slotTree":
		return available.Kind == "slotTree" && (required.SlotKind == "" || required.SlotKind == available.SlotKind)
	default:
		return false
	}
}

// outputStates reports whether an invoke's declared output admits every value
// of its capability's declared result (Core 0.43.0, Phase 642). A capability
// that answers nothing has no output a node could declare.
func outputStates(declared *Space, result Returns) bool {
	return !result.IsNothing() && SpaceSubsumes(declared, result.Value)
}

func upstreamsOf(n PipelineNode) []string {
	var ups []string
	for _, a := range n.Args {
		if a.Source.Kind == ArgFromUpstream {
			ups = append(ups, a.Source.Node)
		}
	}
	return ups
}

// TypeCheckPipeline checks a pipeline against the capabilities registry holds
// (the reference's `CapabilityPipeline.typeCheck`): node ids are unique; every
// invoke's capability resolves (default-deny); its declared output admits the
// capability's declared result (pipelineOutputContradicts); every argument is
// one the capability takes, once (duplicateArg), refused as ValidateArgs
// refuses it and wrapped pipelineArgRefused; a literal lies in its hole's
// space; an edge names a declared node EARLIER in declaration order — a
// self-edge or an edge closing a cycle is pipelineCycle naming the cycle, any
// other later node pipelineForwardEdge — whose output space feeds the
// argument's (SpaceSubsumes; otherwise edgeTypeMismatch); every required hole is
// bound. Total; nil accepts; the first refusal in declaration order answers.
func TypeCheckPipeline(registry *CapabilityRegistry, p CapabilityPipeline) *PipelineError {
	nodeByID := make(map[string]PipelineNode, len(p.Nodes))
	position := make(map[string]int, len(p.Nodes))
	count := make(map[string]int, len(p.Nodes))
	for _, n := range p.Nodes {
		count[n.ID]++
	}
	// The repeated id the reference names is the first, in declaration order,
	// of the ids that repeat.
	for _, n := range p.Nodes {
		if count[n.ID] > 1 {
			return &PipelineError{Kind: ErrDuplicateNode, Node: n.ID}
		}
	}
	for i, n := range p.Nodes {
		nodeByID[n.ID] = n
		position[n.ID] = i
	}

	// pathTo is the path from start along edges to a node whose edge reaches
	// target, if any: [start, …, x] with x -> target. Each node is visited once.
	pathTo := func(target, start string) []string {
		seen := map[string]bool{}
		var walk func(cur string) []string
		walk = func(cur string) []string {
			if seen[cur] {
				return nil
			}
			seen[cur] = true
			ups := upstreamsOf(nodeByID[cur])
			for _, u := range ups {
				if u == target {
					return []string{cur}
				}
			}
			for _, u := range ups {
				if path := walk(u); path != nil {
					return append([]string{cur}, path...)
				}
			}
			return nil
		}
		return walk(start)
	}

	for _, n := range p.Nodes {
		if n.Kind != NodeInvoke {
			continue
		}
		c, ok := registry.TryFind(n.CapabilityID)
		if !ok {
			known := make([]string, 0, len(registry.capabilities))
			for _, e := range registry.Enumerate() {
				known = append(known, e.ID)
			}
			return &PipelineError{Kind: ErrPipelineNoSuchCapability, Capability: n.CapabilityID, Known: known}
		}
		if !outputStates(n.OutputType, c.Result) {
			return &PipelineError{Kind: ErrPipelineOutputContradicts, Node: n.ID, Capability: n.CapabilityID,
				Declared: n.OutputType, Result: c.Result}
		}
		if e := invokeFault(n, c, nodeByID, position, pathTo); e != nil {
			return e
		}
	}
	return nil
}

// invokeFault checks one resolved invoke's arguments, in the reference's
// order: a repeated address first (at its second occurrence), then each
// argument in order, then the unbound required holes.
func invokeFault(n PipelineNode, c Capability, nodeByID map[string]PipelineNode, position map[string]int,
	pathTo func(target, start string) []string) *PipelineError {
	holes := c.Signature.Holes
	declared := make([]string, len(holes))
	for i, h := range holes {
		declared[i] = h.Addr
	}
	refused := func(e *InvokeError) *PipelineError {
		return &PipelineError{Kind: ErrPipelineArgRefused, Node: n.ID, Reason: e}
	}

	bound := make(map[string]bool, len(n.Args))
	for _, a := range n.Args {
		if bound[a.Addr] {
			return refused(&InvokeError{Kind: ErrDuplicateArg, Addr: a.Addr})
		}
		bound[a.Addr] = true
	}

	for _, a := range n.Args {
		if a.Source.Kind == ArgFromLiteral {
			if e := argFault(holes, declared, InvokeArg{Addr: a.Addr, Value: a.Source.Value}); e != nil {
				return refused(e)
			}
			continue
		}
		var hole *SigEntry
		for i := range holes {
			if holes[i].Addr == a.Addr {
				hole = &holes[i]
				break
			}
		}
		if hole == nil {
			return refused(&InvokeError{Kind: ErrUnknownArg, Addr: a.Addr, Declared: declared})
		}
		if hole.Space == nil {
			return refused(&InvokeError{Kind: ErrUninvocableArg, Addr: a.Addr})
		}
		up := a.Source.Node
		upNode, ok := nodeByID[up]
		switch {
		case !ok:
			return &PipelineError{Kind: ErrUnknownNode, Node: up}
		case up == n.ID:
			return &PipelineError{Kind: ErrPipelineCycle, Node: n.ID, Cycle: []string{n.ID}}
		case position[up] > position[n.ID]:
			if path := pathTo(n.ID, up); path != nil {
				return &PipelineError{Kind: ErrPipelineCycle, Node: n.ID, Cycle: append([]string{n.ID}, path...)}
			}
			return &PipelineError{Kind: ErrPipelineForwardEdge, Node: n.ID, Arg: a.Addr, Upstream: up}
		case !SpaceSubsumes(hole.Space, upNode.OutputType):
			return &PipelineError{Kind: ErrEdgeTypeMismatch, Node: n.ID, Arg: a.Addr,
				Producer: upNode.OutputType.Kind, Consumer: hole.Space.Kind}
		}
	}

	var unbound []string
	for _, h := range holes {
		if h.Required && !bound[h.Addr] {
			unbound = append(unbound, h.Addr)
		}
	}
	if len(unbound) > 0 {
		return refused(&InvokeError{Kind: ErrRequiredArgsUnbound, Addrs: unbound})
	}
	return nil
}

// ─── wire reader ────────────────────────────────────────────────────────────

func decodeOutputType(el map[string]any) (*Space, error) {
	o, err := objField(el, "outputType")
	if err != nil {
		return nil, err
	}
	space, err := decodeSpaceDecl(o)
	if err != nil {
		return nil, err
	}
	// A node's output is held to the well-formedness a hole's space is.
	if fault := spaceFault(space); fault != "" {
		return nil, errors.New("outputType: " + fault)
	}
	return space, nil
}

func decodeArgSource(el map[string]any) (ArgSource, error) {
	tag, err := strField(el, "$type")
	if err != nil {
		return ArgSource{}, err
	}
	switch tag {
	case ArgFromLiteral:
		v, err := strField(el, "value")
		return ArgSource{Kind: tag, Value: v}, err
	case ArgFromUpstream:
		node, err := strField(el, "node")
		return ArgSource{Kind: tag, Node: node}, err
	default:
		return ArgSource{}, errors.New("unknown argument source: " + tag)
	}
}

func decodePipelineNode(el map[string]any) (PipelineNode, error) {
	tag, err := strField(el, "$type")
	if err != nil {
		return PipelineNode{}, err
	}
	id, err := strField(el, "id")
	if err != nil {
		return PipelineNode{}, err
	}
	switch tag {
	case NodeSource:
		dataRef, err := strField(el, "dataRef")
		if err != nil {
			return PipelineNode{}, err
		}
		out, err := decodeOutputType(el)
		if err != nil {
			return PipelineNode{}, err
		}
		return PipelineNode{Kind: tag, ID: id, DataRef: dataRef, OutputType: out}, nil
	case NodeInvoke:
		capID, err := strField(el, "capabilityId")
		if err != nil {
			return PipelineNode{}, err
		}
		out, err := decodeOutputType(el)
		if err != nil {
			return PipelineNode{}, err
		}
		raw, ok := el["args"]
		if !ok {
			return PipelineNode{}, errors.New("missing field: args")
		}
		items, ok := raw.([]any)
		if !ok {
			return PipelineNode{}, errors.New("field is not an array: args")
		}
		args := make([]PipelineArg, len(items))
		for i, item := range items {
			o, ok := item.(map[string]any)
			if !ok {
				return PipelineNode{}, errors.New("args[" + strconv.Itoa(i) + "] is not an object")
			}
			addr, err := strField(o, "addr")
			if err != nil {
				return PipelineNode{}, err
			}
			srcObj, err := objField(o, "source")
			if err != nil {
				return PipelineNode{}, err
			}
			src, err := decodeArgSource(srcObj)
			if err != nil {
				return PipelineNode{}, err
			}
			args[i] = PipelineArg{Addr: addr, Source: src}
		}
		return PipelineNode{Kind: tag, ID: id, CapabilityID: capID, OutputType: out, Args: args}, nil
	default:
		return PipelineNode{}, errors.New("unknown pipeline node: " + tag)
	}
}

// DecodePipelineParsed decodes a pipeline document from an already-parsed JSON
// value (numbers as json.Number), as the reference's reader reads it. Total:
// every malformed input yields a named error. Parsing the text under the
// host's wire limits happens before the boundary (the function package).
func DecodePipelineParsed(raw any) (CapabilityPipeline, error) {
	el, ok := raw.(map[string]any)
	if !ok {
		return CapabilityPipeline{}, errors.New("a pipeline document must be an object")
	}
	rawNodes, ok := el[pipelineNodesKey]
	if !ok {
		return CapabilityPipeline{}, errors.New("missing field: nodes")
	}
	items, ok := rawNodes.([]any)
	if !ok {
		return CapabilityPipeline{}, errors.New("field is not an array: nodes")
	}
	nodes := make([]PipelineNode, len(items))
	for i, item := range items {
		o, ok := item.(map[string]any)
		if !ok {
			return CapabilityPipeline{}, errors.New("nodes[" + strconv.Itoa(i) + "] is not an object")
		}
		n, err := decodePipelineNode(o)
		if err != nil {
			return CapabilityPipeline{}, errors.New("nodes[" + strconv.Itoa(i) + "]: " + err.Error())
		}
		nodes[i] = n
	}
	return CapabilityPipeline{Nodes: nodes}, nil
}
