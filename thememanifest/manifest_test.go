package thememanifest

import "testing"

func sp(s string) *string { return &s }

func sampleManifest() ThemeManifest {
	return ThemeManifest{
		Meta: ManifestMeta{Name: "test", Version: "1.0"},
		Tokens: []ManifestToken{
			{Name: "color.brand.base", Type: "color", Value: "#3b5bdb"},
			{Name: "color.surface", Type: "color", Value: "#ffffff"},
			{Name: "space.md", Type: "dimension", Value: "16px"},
		},
		Roles: []RoleBinding{
			{Role: ToneRole{Tone: "Brand"}, TokenName: "color.brand.base"},
			{Role: NamedRole{Name: "body-text"}, TokenName: "color.surface"},
		},
		Invariants: []Invariant{NewInvariant(ContrastFloor{Role: "Brand", MinRatio: 7.0})},
	}
}

func TestHelpers(t *testing.T) {
	m := sampleManifest()
	if tok := TryGetToken("color.surface", m); tok == nil || tok.Value != "#ffffff" {
		t.Fatalf("try_get_token color.surface: %v", tok)
	}
	if TryGetToken("missing", m) != nil {
		t.Fatal("expected nil for missing token")
	}
	if brand := ResolveRole("Brand", m); brand == nil || brand.Name != "color.brand.base" {
		t.Fatalf("resolve_role Brand: %v", brand)
	}
	if ResolveRole("Critical", m) != nil {
		t.Fatal("expected nil for unbound Critical")
	}
	if body := ResolveNamedRole("body-text", m); body == nil || body.Value != "#ffffff" {
		t.Fatalf("resolve_named_role body-text: %v", body)
	}
	pal := PaletteColours(m)
	if len(pal) != 2 || !pal["#3b5bdb"] || !pal["#ffffff"] {
		t.Fatalf("palette_colours: %v", pal)
	}
}

func TestDecodeWrapper(t *testing.T) {
	payload := `{
		"meta": {"name": "acme", "version": "2.1", "description": "x"},
		"tokens": {"color": {"brand": {"base": {"$type":"color","$value":"#3b5bdb","$description":"brand"}},
		                     "surface": {"$type":"color","$value":"#ffffff"}}},
		"roles": [{"role": {"tone": "Brand"}, "token": "color.brand.base"}],
		"invariants": [{"kind":"ContrastFloor","role":"Brand","minRatio":7,"weight":2}]
	}`
	m, err := Decode(payload)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if m.Meta.Name != "acme" || m.Meta.Version != "2.1" || m.Meta.Description == nil || *m.Meta.Description != "x" {
		t.Fatalf("meta: %+v", m.Meta)
	}
	tok := TryGetToken("color.brand.base", m)
	if tok == nil || tok.Type != "color" || tok.Value != "#3b5bdb" || tok.Description == nil || *tok.Description != "brand" {
		t.Fatalf("brand token: %+v", tok)
	}
	if brand := ResolveRole("Brand", m); brand == nil || brand.Name != "color.brand.base" {
		t.Fatalf("resolve_role Brand: %v", brand)
	}
	if len(m.Invariants) != 1 {
		t.Fatalf("invariants: %v", m.Invariants)
	}
	cf, ok := m.Invariants[0].Kind.(ContrastFloor)
	if !ok || cf.Role != "Brand" || cf.MinRatio != 7.0 || m.Invariants[0].Weight != 2.0 {
		t.Fatalf("invariant: %+v", m.Invariants[0])
	}
}

func TestDecodeVanillaDTCG(t *testing.T) {
	m, err := Decode(`{"color": {"accent": {"$type":"color","$value":"#ff8800"}}}`)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(m.Tokens) != 1 || m.Tokens[0].Name != "color.accent" || m.Tokens[0].Value != "#ff8800" {
		t.Fatalf("tokens: %+v", m.Tokens)
	}
	if len(m.Roles) != 0 {
		t.Fatalf("roles should be empty: %v", m.Roles)
	}
}

func TestDecodeRoleExtension(t *testing.T) {
	m, err := Decode(`{"color": {"brand": {"$type":"color","$value":"#3b5bdb","$extensions":{"fuaran":{"role":"accent"}}}}}`)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(m.Tokens) != 1 || m.Tokens[0].Role == nil || *m.Tokens[0].Role != "accent" {
		t.Fatalf("token role: %+v", m.Tokens)
	}
}

func TestProjectors(t *testing.T) {
	m := ProjectFromFuaranToneVars(":root { --fuaran-tone-brand-bg: #3b5bdb; --fuaran-tone-brand-fg: #fff; }")
	if bg := TryGetToken("tone.brand.bg", m); bg == nil || bg.Value != "#3b5bdb" {
		t.Fatalf("tone.brand.bg: %v", bg)
	}
	if brand := ResolveRole("Brand", m); brand == nil || brand.Name != "tone.brand.bg" {
		t.Fatalf("resolve_role Brand: %v", brand)
	}

	css := `:root { --color-x: #111; } [data-theme="dark"] { --color-x: #eee; }`
	g := ProjectFromCssCustomProperties(css)
	if light := TryGetToken("color-x", g); light == nil || light.Value != "#111" {
		t.Fatalf("color-x: %v", light)
	}
	if dark := TryGetToken("color-x@dark", g); dark == nil || dark.Value != "#eee" {
		t.Fatalf("color-x@dark: %v", dark)
	}
	if len(g.Roles) != 0 {
		t.Fatalf("roles should be empty: %v", g.Roles)
	}
}

func TestMergeLastWriteWins(t *testing.T) {
	base := ProjectFromCssCustomProperties(":root { --a: 1px; --b: 2px; }")
	over := ProjectFromCssCustomProperties(":root { --b: 9px; }")
	m := Merge(base, over)
	if b := TryGetToken("b", m); b == nil || b.Value != "9px" {
		t.Fatalf("b: %v", b)
	}
	if a := TryGetToken("a", m); a == nil || a.Value != "1px" {
		t.Fatalf("a: %v", a)
	}
}
