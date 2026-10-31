package archrules

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Violation is one broken architecture rule.
type Violation struct {
	File    string // slash-separated path relative to the repo root
	Line    int
	Import  string
	Layer   Layer
	Rule    string
	Message string
}

func (v Violation) String() string {
	return fmt.Sprintf("%s:%d: [%s] %s", v.File, v.Line, v.Rule, v.Message)
}

// LayerOf maps a slash-separated, repo-relative file path to its layer.
// ok=false means the file is exempt (tooling, scripts, docs, archrules itself).
// A Go file directly under internal/ in an unknown directory yields
// layer "" with ok=true and is reported as RuleUnknownLayer by CheckSource.
func LayerOf(rel string) (layer Layer, ok bool) {
	parts := strings.Split(rel, "/")
	switch {
	case len(parts) >= 2 && parts[0] == "cmd":
		return LayerCmd, true
	case len(parts) >= 3 && parts[0] == "internal":
		switch parts[1] {
		case "domain":
			return LayerDomain, true
		case "application":
			return LayerApplication, true
		case "infrastructure":
			return LayerInfrastructure, true
		case "presentation":
			return LayerPresentation, true
		case "archrules":
			return "", false
		default:
			return "", true
		}
	}
	return "", false
}

func layerOfImport(module, imp string) (Layer, bool) {
	switch {
	case imp == module+"/internal/domain" || strings.HasPrefix(imp, module+"/internal/domain/"):
		return LayerDomain, true
	case imp == module+"/internal/application" || strings.HasPrefix(imp, module+"/internal/application/"):
		return LayerApplication, true
	case imp == module+"/internal/infrastructure" || strings.HasPrefix(imp, module+"/internal/infrastructure/"):
		return LayerInfrastructure, true
	case imp == module+"/internal/presentation" || strings.HasPrefix(imp, module+"/internal/presentation/"):
		return LayerPresentation, true
	case imp == module+"/cmd" || strings.HasPrefix(imp, module+"/cmd/"):
		return LayerCmd, true
	}
	return "", false
}

func isProject(module, imp string) bool {
	return imp == module || strings.HasPrefix(imp, module+"/")
}

// isStdlib: stdlib import paths have no dot in their first element.
func isStdlib(imp string) bool {
	first := imp
	if i := strings.Index(imp, "/"); i >= 0 {
		first = imp[:i]
	}
	return !strings.Contains(first, ".")
}

// CheckSource checks one Go source file. rel is its repo-relative slash path.
func CheckSource(module, rel string, src []byte) ([]Violation, error) {
	layer, ok := LayerOf(rel)
	if !ok {
		return nil, nil
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, rel, src, parser.ImportsOnly)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", rel, err)
	}
	var out []Violation
	if layer == "" {
		out = append(out, Violation{File: rel, Line: 1, Rule: RuleUnknownLayer,
			Message: "Go file in an unknown internal/ directory; new packages must live in domain, application, infrastructure or presentation (docs/01 section 4)"})
		return out, nil
	}
	for _, spec := range f.Imports {
		imp, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return nil, fmt.Errorf("%s: bad import literal %s", rel, spec.Path.Value)
		}
		line := fset.Position(spec.Pos()).Line
		add := func(rule, msg string) {
			out = append(out, Violation{File: rel, Line: line, Import: imp, Layer: layer, Rule: rule, Message: msg})
		}
		switch {
		case imp == "C":
			add(RuleForbiddenStd, fmt.Sprintf("%s layer must not use cgo", layer))
		case isProject(module, imp):
			if l, ok := layerOfImport(module, imp); ok {
				for _, bad := range forbiddenLayers[layer] {
					if l == bad {
						add(RuleForbiddenLayer, fmt.Sprintf("%s layer must not import the %s layer (%q); dependencies point inward only", layer, l, imp))
					}
				}
			}
		case isStdlib(imp):
			for _, e := range forbiddenStd[layer] {
				if matchesStd(e, imp) {
					add(RuleForbiddenStd, fmt.Sprintf("%s layer must not import %q (I/O, process, signal or logging package); reach it through an application port", layer, imp))
					break
				}
			}
		default:
			add(RuleThirdParty, fmt.Sprintf("third-party import %q is not allowed: standard library only (docs/00 P-6); dependencies need owner approval (escalation E-5)", imp))
		}
	}
	return out, nil
}

// CheckTree walks root and checks every .go file (skipping .git, testdata, vendor).
func CheckTree(root, module string) ([]Violation, error) {
	var all []Violation
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "testdata", "vendor", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		vs, err := CheckSource(module, rel, src)
		if err != nil {
			return err
		}
		all = append(all, vs...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].File != all[j].File {
			return all[i].File < all[j].File
		}
		return all[i].Line < all[j].Line
	})
	return all, nil
}
