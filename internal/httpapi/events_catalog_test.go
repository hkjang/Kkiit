package httpapi

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// An event that can be subscribed to but has no template renders no
// notification, and the dispatcher treats that as "nothing to do" rather than
// as an error. BudgetExhausted spent its whole life in the catalogue without
// ever being emitted, and RFQCreated was emitted with nothing to render it;
// neither failure showed up anywhere.
func TestEveryAdvertisedEventCanBeRendered(t *testing.T) {
	files, err := filepath.Glob("../database/migrations/*.sql")
	if err != nil || len(files) == 0 {
		t.Fatalf("마이그레이션을 찾지 못했습니다: %v", err)
	}
	templatePattern := regexp.MustCompile(`\('([A-Za-z_]+)','web','ko-KR'`)
	templates := map[string]bool{}
	for _, file := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		for _, match := range templatePattern.FindAllStringSubmatch(string(body), -1) {
			templates[match[1]] = true
		}
	}
	if len(templates) < 20 {
		t.Fatalf("알림 템플릿을 %d개만 찾았습니다. 추출 방식이 깨졌을 수 있습니다.", len(templates))
	}
	var missing []string
	for _, event := range eventCatalog {
		if !templates[event] {
			missing = append(missing, event)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("구독은 가능한데 알림 템플릿이 없는 이벤트 %d개: %s", len(missing), strings.Join(missing, ", "))
	}
}
