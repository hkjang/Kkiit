package database

import (
	"strings"
	"testing"
)

// Two branches once landed a 040 each. The runner records one version per
// number, so the file that sorted second was never applied and nobody noticed
// until a setting was missing. A duplicate number is now a failing build.
func TestMigrationVersionsAreUnique(t *testing.T) {
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	seen := map[string]string{}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		version := strings.SplitN(entry.Name(), "_", 2)[0]
		if previous, duplicate := seen[version]; duplicate {
			t.Errorf("마이그레이션 번호 %s 가 겹칩니다: %s, %s — 하나는 적용되지 않습니다", version, previous, entry.Name())
		}
		seen[version] = entry.Name()
	}
	if len(seen) < 40 {
		t.Fatalf("마이그레이션을 %d개만 찾았습니다", len(seen))
	}
}
