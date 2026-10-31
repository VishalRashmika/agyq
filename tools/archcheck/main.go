// Command archcheck enforces the layering rule of docs/01-ARCHITECTURE.md.
// Run from anywhere inside the repo: go run ./tools/archcheck
// Exit status 1 on any violation (this is a build failure, not a warning).
package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"agyq/internal/archrules"
)

func main() {
	root, module, err := findRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "archcheck:", err)
		os.Exit(2)
	}
	vs, err := archrules.CheckTree(root, module)
	if err != nil {
		fmt.Fprintln(os.Stderr, "archcheck:", err)
		os.Exit(2)
	}
	if len(vs) == 0 {
		fmt.Println("archcheck: OK (no architecture violations)")
		return
	}
	for _, v := range vs {
		fmt.Fprintln(os.Stderr, v)
	}
	fmt.Fprintf(os.Stderr, "archcheck: FAILED with %d violation(s). See docs/01-ARCHITECTURE.md section 2.1.\n", len(vs))
	os.Exit(1)
}

func findRoot() (root, module string, err error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", "", err
	}
	for {
		gm := filepath.Join(dir, "go.mod")
		if f, e := os.Open(gm); e == nil {
			defer f.Close()
			sc := bufio.NewScanner(f)
			for sc.Scan() {
				line := strings.TrimSpace(sc.Text())
				if strings.HasPrefix(line, "module ") {
					return dir, strings.TrimSpace(strings.TrimPrefix(line, "module ")), nil
				}
			}
			return "", "", fmt.Errorf("no module line in %s", gm)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", "", fmt.Errorf("go.mod not found")
		}
		dir = parent
	}
}
