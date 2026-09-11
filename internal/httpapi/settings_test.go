package httpapi

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// A setting an operator can edit that no code reads is a screen that lies: they
// change a number, save it, and nothing behaves differently. Seven settings
// were in that state, and two of them duplicated a value that was read from
// somewhere else, so tightening the one that read like the right one did
// nothing at all.
//
// Settings that are genuinely waiting on unbuilt adapters stay, but they have
// to say so in unconnectedSettings, which the console shows to the operator.
func TestEverySettingIsEitherReadOrDeclaredUnconnected(t *testing.T) {
	migrations, err := filepath.Glob("../database/migrations/*.sql")
	if err != nil || len(migrations) == 0 {
		t.Fatalf("마이그레이션을 찾지 못했습니다: %v", err)
	}
	inserted := regexp.MustCompile(`\('([a-z][a-z0-9_.]+)',\s*'\{`)
	keys := map[string]bool{}
	for _, file := range migrations {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		for _, match := range inserted.FindAllStringSubmatch(string(body), -1) {
			if strings.Contains(match[1], ".") {
				keys[match[1]] = true
			}
		}
	}
	if len(keys) < 15 {
		t.Fatalf("설정 키를 %d개만 찾았습니다. 추출 방식이 깨졌을 수 있습니다.", len(keys))
	}

	sources, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("소스를 찾지 못했습니다: %v", err)
	}
	workerSources, _ := filepath.Glob("../worker/*.go")
	var code strings.Builder
	for _, file := range append(sources, workerSources...) {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		body, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		code.Write(body)
	}
	// The declaration of unconnectedSettings names every key it covers, so it
	// would answer this question with itself. It is cut out before searching.
	haystack := code.String()
	if start := strings.Index(haystack, "var unconnectedSettings = map[string]string{"); start >= 0 {
		if end := strings.Index(haystack[start:], "\n}\n"); end >= 0 {
			haystack = haystack[:start] + haystack[start+end:]
		}
	}

	// Settings are read both as Go string literals and as SQL literals inside
	// queries, so both spellings count as a read.
	isRead := func(key string) bool {
		return strings.Contains(haystack, `"`+key+`"`) || strings.Contains(haystack, "'"+key+"'")
	}

	var silent []string
	for key := range keys {
		if isRead(key) {
			continue
		}
		if _, declared := unconnectedSettings[key]; declared {
			continue
		}
		silent = append(silent, key)
	}
	sort.Strings(silent)
	if len(silent) > 0 {
		t.Errorf("읽는 곳도 없고 미연결 선언도 없는 설정 %d개: %s", len(silent), strings.Join(silent, ", "))
	}

	// The other direction: a key declared unconnected that something does read
	// is a note that has gone stale and now understates what the screen does.
	var stale []string
	for key := range unconnectedSettings {
		if isRead(key) {
			stale = append(stale, key)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf("미연결로 표시했지만 코드가 읽고 있는 설정: %s", strings.Join(stale, ", "))
	}
}
