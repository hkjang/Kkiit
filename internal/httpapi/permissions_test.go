package httpapi

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// A permission on the roles screen that nothing reads is a promise the product
// cannot keep. keys.manage.any was declared, granted to the security admin role
// and listed for anyone editing roles, and no line of code had ever looked at
// it: a security administrator was told they could manage other people's API
// keys and could not.
//
// A permission counts as honoured if a route requires it, an MCP tool declares
// it, or some handler checks it directly.
func TestEveryPermissionIsHonouredSomewhere(t *testing.T) {
	migrations, err := filepath.Glob("../database/migrations/*.sql")
	if err != nil || len(migrations) == 0 {
		t.Fatalf("마이그레이션을 찾지 못했습니다: %v", err)
	}
	block := regexp.MustCompile(`(?s)INSERT INTO permissions.*?;`)
	entry := regexp.MustCompile(`\('([a-z][a-z.]+)'`)
	declared := map[string]bool{}
	for _, file := range migrations {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		for _, statement := range block.FindAllString(string(body), -1) {
			for _, match := range entry.FindAllStringSubmatch(statement, -1) {
				declared[match[1]] = true
			}
		}
	}
	if len(declared) < 10 {
		t.Fatalf("권한을 %d개만 찾았습니다. 추출 방식이 깨졌을 수 있습니다.", len(declared))
	}

	sources, _ := filepath.Glob("*.go")
	webSources, _ := filepath.Glob("../../web/src/**/*.tsx")
	shell, _ := filepath.Glob("../../web/src/components/*.tsx")
	pages, _ := filepath.Glob("../../web/src/*.tsx")
	var code strings.Builder
	for _, file := range append(append(append(sources, webSources...), shell...), pages...) {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		if body, err := os.ReadFile(file); err == nil {
			code.Write(body)
		}
	}
	haystack := code.String()

	var unused []string
	for permission := range declared {
		if !strings.Contains(haystack, `"`+permission+`"`) && !strings.Contains(haystack, "'"+permission+"'") {
			unused = append(unused, permission)
		}
	}
	sort.Strings(unused)
	if len(unused) > 0 {
		t.Errorf("어디에서도 확인하지 않는 권한 %d개: %s", len(unused), strings.Join(unused, ", "))
	}
}
