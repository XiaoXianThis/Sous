package agents

import (
	"runtime"
	"testing"
)

func TestPick(t *testing.T) {
	m := map[string]Recipe{"unix": {Script: "u"}, "windows": {Script: "w"}}
	r, ok := pick(m)
	want := "u"
	if runtime.GOOS == "windows" {
		want = "w"
	}
	if !ok || r.Script != want {
		t.Fatalf("pick = %+v %v", r, ok)
	}
	if _, ok := pick(map[string]Recipe{"plan9": {}}); ok {
		t.Fatal("picked another system's recipe")
	}
	if r, _ := pick(map[string]Recipe{"*": {Argv: []string{"a", "b c"}, Env: []string{"X=1"}}}); r.String() != `X=1 a "b c"` {
		t.Fatalf("string = %q", r.String())
	}
}

func TestBuiltinRecipes(t *testing.T) {
	for _, s := range Builtin() {
		if _, ok := s.LoginRecipe(); !ok {
			t.Errorf("%s has no login", s.Name)
		}
		if len(s.Configs) == 0 {
			t.Errorf("%s has no config file", s.Name)
		}
	}
}

func TestVersionLess(t *testing.T) {
	if !versionLess("/n/v9.1.0/bin", "/n/v24.0.0/bin") || versionLess("/n/v24.21.0/bin", "/n/v24.3.0/bin") {
		t.Fatal("wrong order")
	}
}
