// Package archrules mechanically enforces the dependency rule from
// docs/01-ARCHITECTURE.md section 2.1. It is tooling, not an application layer.
// The tables below MUST stay identical to the table in that document.
package archrules

import "strings"

// Layer identifies an architecture layer.
type Layer string

const (
	LayerDomain         Layer = "domain"
	LayerApplication    Layer = "application"
	LayerInfrastructure Layer = "infrastructure"
	LayerPresentation   Layer = "presentation"
	LayerCmd            Layer = "cmd"
)

// Rule IDs reported in violations.
const (
	RuleForbiddenStd   = "forbidden-std-import"
	RuleForbiddenLayer = "forbidden-layer-import"
	RuleThirdParty     = "third-party-import"
	RuleUnknownLayer   = "unknown-layer"
)

// ioStd are standard-library packages that perform I/O, spawn processes,
// handle signals or log. A trailing "/..." forbids the whole subtree.
var ioStd = []string{
	"os", "os/exec", "os/signal", "os/user", "io/fs", "io/ioutil",
	"net/...", "syscall", "log/...", "flag", "path/filepath",
}

// forbiddenStd lists forbidden stdlib imports per layer.
var forbiddenStd = map[Layer][]string{
	LayerDomain:         ioStd,
	LayerApplication:    ioStd,
	LayerInfrastructure: nil,
	LayerPresentation:   {"os/exec", "syscall", "net/..."},
	LayerCmd:            nil,
}

// forbiddenLayers lists project layers a layer must not import.
var forbiddenLayers = map[Layer][]Layer{
	LayerDomain:         {LayerApplication, LayerInfrastructure, LayerPresentation, LayerCmd},
	LayerApplication:    {LayerInfrastructure, LayerPresentation, LayerCmd},
	LayerInfrastructure: {LayerPresentation, LayerCmd},
	LayerPresentation:   {LayerInfrastructure, LayerCmd},
	LayerCmd:            nil,
}

// matchesStd reports whether import path p is covered by rule entry e.
func matchesStd(e, p string) bool {
	if strings.HasSuffix(e, "/...") {
		root := strings.TrimSuffix(e, "/...")
		return p == root || strings.HasPrefix(p, root+"/")
	}
	return p == e
}
