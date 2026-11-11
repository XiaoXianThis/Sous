package sysmap

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

func constants(t *testing.T, dir string, visit func(string, string, bool)) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("..", dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			typed := false
			for _, spec := range gen.Specs {
				v := spec.(*ast.ValueSpec)
				if v.Type != nil {
					id, ok := v.Type.(*ast.Ident)
					typed = ok && id.Name == "Type"
				}
				for i, name := range v.Names {
					if i >= len(v.Values) {
						continue
					}
					lit, ok := v.Values[i].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					value, err := strconv.Unquote(lit.Value)
					if err != nil {
						t.Fatal(err)
					}
					visit(name.Name, value, typed)
				}
			}
		}
	}
}
func TestEveryEventGrouped(t *testing.T) {
	constants(t, "event", func(name, value string, typed bool) {
		if typed && GroupOf(value) == "" {
			t.Errorf("event %s (%s) has no group in EventGroups", name, value)
		}
	})
}
func TestEveryFrameDirected(t *testing.T) {
	known := map[string]bool{}
	for _, f := range FrameDown {
		known[f] = true
	}
	for _, f := range FrameUp {
		known[f] = true
	}
	constants(t, "proto", func(name, value string, _ bool) {
		r := []rune(name)
		if len(r) >= 2 && r[0] == 'T' && unicode.IsUpper(r[1]) && !known[value] {
			t.Errorf("frame %s (%s) is in neither FrameDown nor FrameUp", name, value)
		}
	})
}
