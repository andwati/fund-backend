package api

import (
	"net/http"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

var methods = map[string]bool{"get": true, "post": true, "put": true, "patch": true, "delete": true}

// Every registered route must be documented, and every documented route must exist.
func TestSpecMatchesRoutes(t *testing.T) {
	var spec struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(OpenAPISpec, &spec); err != nil {
		t.Fatalf("openapi.yaml does not parse: %v", err)
	}
	documented := map[string]bool{}
	for path, ops := range spec.Paths {
		for m := range ops {
			if methods[m] {
				documented[strings.ToUpper(m)+" "+path] = true
			}
		}
	}
	s := New(Deps{})
	registered := map[string]bool{}
	for _, p := range s.routes {
		registered[p] = true
	}
	var missing, extra []string
	for r := range registered {
		if !documented[r] {
			missing = append(missing, r)
		}
	}
	for d := range documented {
		if !registered[d] {
			extra = append(extra, d)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		t.Errorf("routes missing from openapi.yaml:\n  %s", strings.Join(missing, "\n  "))
	}
	if len(extra) > 0 {
		t.Errorf("openapi.yaml documents routes that do not exist:\n  %s", strings.Join(extra, "\n  "))
	}
	if len(registered) < 50 {
		t.Errorf("only %d routes registered; expected the full API", len(registered))
	}
}

// Every $ref in the spec must resolve.
func TestSpecRefsResolve(t *testing.T) {
	var doc map[string]any
	if err := yaml.Unmarshal(OpenAPISpec, &doc); err != nil {
		t.Fatal(err)
	}
	refRe := regexp.MustCompile(`\$ref:\s*"?#/([^"\s}]+)`)
	for _, m := range refRe.FindAllStringSubmatch(string(OpenAPISpec), -1) {
		var cur any = doc
		for _, part := range strings.Split(m[1], "/") {
			mp, ok := cur.(map[string]any)
			if !ok {
				t.Errorf("unresolvable $ref #/%s", m[1])
				break
			}
			if cur, ok = mp[part]; !ok {
				t.Errorf("unresolvable $ref #/%s", m[1])
				break
			}
		}
	}
}

func TestServesTheSpec(t *testing.T) {
	a := newApp(t, opts{})
	r := a.do(http.MethodGet, "/openapi.yaml", "", nil)
	if r.Status != 200 || !strings.HasPrefix(string(r.Body), "openapi: 3.1.0") {
		t.Fatalf("%d %.40s", r.Status, r.Body)
	}
}
