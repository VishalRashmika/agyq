package archrules

import (
	"os"
	"path/filepath"
	"testing"
)

const mod = "agyq"

func TestCheckSourceFixtures(t *testing.T) {
	cases := []struct {
		fixture string
		rel     string // pretend repo-relative location
		want    []string
	}{
		{"clean_domain", "internal/domain/x.go", nil},
		{"domain_os_exec", "internal/domain/x.go", []string{RuleForbiddenStd}},
		{"domain_net_http", "internal/domain/x.go", []string{RuleForbiddenStd}},
		{"domain_imports_infra", "internal/domain/x.go", []string{RuleForbiddenLayer}},
		{"domain_third_party", "internal/domain/x.go", []string{RuleThirdParty}},
		{"application_os", "internal/application/x.go", []string{RuleForbiddenStd}},
		{"application_log", "internal/application/x.go", []string{RuleForbiddenStd}},
		{"application_imports_presentation", "internal/application/x.go", []string{RuleForbiddenLayer}},
		{"application_clean", "internal/application/x.go", nil},
		{"infra_imports_presentation", "internal/infrastructure/x.go", []string{RuleForbiddenLayer}},
		{"infra_clean", "internal/infrastructure/x.go", nil},
		{"presentation_imports_infra", "internal/presentation/x.go", []string{RuleForbiddenLayer}},
		{"presentation_exec", "internal/presentation/x.go", []string{RuleForbiddenStd}},
		{"cmd_clean", "cmd/agyq/main.go", nil},
		// same violating source is fine in an exempt location (tooling)
		{"domain_os_exec", "tools/archcheck/x.go", nil},
		// unknown internal package
		{"clean_domain", "internal/newthing/x.go", []string{RuleUnknownLayer}},
	}
	for _, c := range cases {
		t.Run(c.fixture+"@"+c.rel, func(t *testing.T) {
			src, err := os.ReadFile(filepath.Join("testdata", c.fixture+".go.txt"))
			if err != nil {
				t.Fatal(err)
			}
			vs, err := CheckSource(mod, c.rel, src)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, v := range vs {
				got = append(got, v.Rule)
			}
			if len(got) != len(c.want) {
				t.Fatalf("got rules %v, want %v (violations: %v)", got, c.want, vs)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("got rules %v, want %v", got, c.want)
				}
			}
		})
	}
}

// The real repository must always be clean.
func TestRepositoryIsClean(t *testing.T) {
	vs, err := CheckTree(filepath.Join("..", ".."), mod)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range vs {
		t.Errorf("architecture violation: %s", v)
	}
}
