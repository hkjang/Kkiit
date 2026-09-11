package httpapi

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The spec had drifted by nine routes before this test existed, including
// logout and the whole social login flow. Documentation that is only correct
// when someone remembers to update it is documentation that is quietly wrong,
// so the check runs with the tests.
//
// It reads the router source rather than the mux because net/http does not
// expose the patterns it was given, and the source is the same thing the mux
// was built from.
var (
	routePattern = regexp.MustCompile(`mux\.HandleFunc\("[A-Z]+ (/api/v1[^"]*)"`)
	specPattern  = regexp.MustCompile(`(?m)^  (/[^\s:]*):$`)
)

func TestOpenAPIDocumentsEveryRoute(t *testing.T) {
	router, err := os.ReadFile("router.go")
	if err != nil {
		t.Fatalf("라우터를 읽지 못했습니다: %v", err)
	}
	spec, err := os.ReadFile("../../docs/openapi.yaml")
	if err != nil {
		t.Fatalf("OpenAPI 문서를 읽지 못했습니다: %v", err)
	}
	routes := map[string]bool{}
	for _, match := range routePattern.FindAllStringSubmatch(string(router), -1) {
		path := strings.TrimPrefix(match[1], "/api/v1")
		if path == "" {
			path = "/"
		}
		routes[path] = true
	}
	if len(routes) < 100 {
		t.Fatalf("라우터에서 %d개의 경로만 찾았습니다. 추출 방식이 깨졌을 수 있습니다.", len(routes))
	}
	documented := map[string]bool{}
	for _, match := range specPattern.FindAllStringSubmatch(string(spec), -1) {
		documented[match[1]] = true
	}

	var undocumented, stale []string
	for path := range routes {
		if !documented[path] {
			undocumented = append(undocumented, path)
		}
	}
	for path := range documented {
		if !routes[path] {
			stale = append(stale, path)
		}
	}
	sort.Strings(undocumented)
	sort.Strings(stale)
	if len(undocumented) > 0 {
		t.Errorf("docs/openapi.yaml에 없는 경로 %d개: %v", len(undocumented), undocumented)
	}
	// A documented path that no longer exists sends callers at a door that is
	// not there, which is worse than saying nothing.
	if len(stale) > 0 {
		t.Errorf("라우터에 없는데 문서에 남은 경로 %d개: %v", len(stale), stale)
	}

	// The spec is not parsed anywhere in the build, so a value that YAML cannot
	// scan ships without a word from anything. A backtick opening a scalar is
	// the mistake that actually happens here, because the descriptions are
	// written in the same Markdown voice as the README.
	for number, line := range strings.Split(string(spec), "\n") {
		trimmed := strings.TrimSpace(line)
		key, value, found := strings.Cut(trimmed, ": ")
		if !found || strings.HasPrefix(trimmed, "#") || strings.Contains(key, " ") {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(value), "`") {
			t.Errorf("docs/openapi.yaml %d행: 값이 백틱으로 시작해 YAML이 읽지 못합니다. 따옴표로 감싸 주세요.", number+1)
		}
	}
}
