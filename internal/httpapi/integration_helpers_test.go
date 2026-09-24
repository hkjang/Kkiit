package httpapi

import "testing"

// These cover the helpers the integration tests are built from. They need no
// database: a helper that misbehaves takes the whole test binary down with it,
// so it is worth pinning separately from the suite it serves.

// TestUniqueNameSuffixKeepsItsWidth guards the assumption callers make when they
// slice a fixed length off the result, such as the twelve character coupon code
// in TestIntegrationAuditLogAnswersWhoChangedThis. The suffix used to be printed
// without padding, so roughly one run in ten produced a name short enough for
// that slice to panic and abort every test in the package.
func TestUniqueNameSuffixKeepsItsWidth(t *testing.T) {
	const prefix = "AUD"
	for _, nanos := range []int64{0, 1, 999, 99_999_999, 100_000_000, 999_999_999, 1_700_000_000_000_000_000} {
		name := uniqueNameAt(prefix, nanos)
		if len(name) != len(prefix)+9 {
			t.Errorf("nanos=%d 이름=%q 길이=%d, 기대=%d", nanos, name, len(name), len(prefix)+9)
		}
	}
}

// TestUniqueNameSeparatesDistinctInstants keeps the padding from being bought by
// truncating the suffix: names still have to differ when the clock does.
func TestUniqueNameSeparatesDistinctInstants(t *testing.T) {
	seen := map[string]int64{}
	for _, nanos := range []int64{0, 1, 999, 99_999_999, 100_000_000, 999_999_999} {
		name := uniqueNameAt("seller", nanos)
		if before, ok := seen[name]; ok {
			t.Fatalf("nanos=%d 와 nanos=%d 가 같은 이름 %q 를 만듭니다", before, nanos, name)
		}
		seen[name] = nanos
	}
}
