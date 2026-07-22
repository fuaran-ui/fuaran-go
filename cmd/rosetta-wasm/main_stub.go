//go:build !js || !wasm

package main

// main is an empty entry point for non-wasm targets, so `go build ./...`,
// `go vet ./...`, and `go test ./...` stay green on the host toolchain. The real
// entry point (main_wasm.go) is built only under GOOS=js GOARCH=wasm; the tree
// builder + encoder (rosetta.go) are platform-neutral and unit-tested here.
func main() {}
