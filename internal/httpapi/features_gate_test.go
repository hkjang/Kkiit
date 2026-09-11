package httpapi

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// A feature switch that leaves most of its own routes reachable is a switch
// that does not do what its name says. Turning enterprise off used to hide the
// organization list and block creating a new one while leaving member
// management, budget creation and company spending fully open through the API.
//
// The mapping below is the claim each flag makes about which paths it governs.
// A new route under one of those prefixes has to be gated or this fails.
var featureGuardedPrefixes = map[string][]string{
	"enterprise":  {"/organizations", "/me/organizations"},
	"smart_quote": {"/rfqs", "/quotes"},
}

func TestFeatureFlagsGateEveryRouteTheyClaim(t *testing.T) {
	source, err := os.ReadFile("router.go")
	if err != nil {
		t.Fatalf("라우터를 읽지 못했습니다: %v", err)
	}
	line := regexp.MustCompile(`(?m)^\s*mux\.HandleFunc\("[A-Z]+ (/api/v1[^"]*)".*$`)
	matches := line.FindAllStringSubmatch(string(source), -1)
	if len(matches) < 100 {
		t.Fatalf("라우터에서 %d개의 경로만 찾았습니다. 추출 방식이 깨졌을 수 있습니다.", len(matches))
	}
	var ungated []string
	for _, match := range matches {
		path := strings.TrimPrefix(match[1], "/api/v1")
		for flag, prefixes := range featureGuardedPrefixes {
			for _, prefix := range prefixes {
				if path != prefix && !strings.HasPrefix(path, prefix+"/") {
					continue
				}
				if !strings.Contains(match[0], `requireFeature("`+flag+`"`) {
					ungated = append(ungated, flag+" → "+path)
				}
			}
		}
	}
	sort.Strings(ungated)
	if len(ungated) > 0 {
		t.Errorf("기능 플래그가 막지 못하는 경로 %d개: %s", len(ungated), strings.Join(ungated, ", "))
	}
}
