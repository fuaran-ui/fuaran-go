//go:build js && wasm

package main

import "syscall/js"

// main registers the JS-callable encoder and keeps the Go runtime alive so the
// exported function stays reachable. Under GOOS=js GOARCH=wasm the thin JS
// loader (public/rosetta/wasm_exec.js + the page glue) instantiates this module
// and calls `fuaranGoRosettaEncode(holesJSON)`; the codec itself is untouched.
func main() {
	js.Global().Set("fuaranGoRosettaEncode", js.FuncOf(rosettaEncode))
	// Block forever — the module exposes a callable surface, not a batch job.
	select {}
}

// rosettaEncode is the JS bridge: args[0] is the six-scalar holes JSON string;
// the return is the canonical wire bytes, or an empty string on a malformed
// envelope (the page treats an empty result as "diverges", never a crash).
func rosettaEncode(_ js.Value, args []js.Value) any {
	if len(args) < 1 {
		return ""
	}
	wire, err := encodeFromHoles(args[0].String())
	if err != nil {
		return ""
	}
	return wire
}
