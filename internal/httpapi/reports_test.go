package httpapi

import "testing"

func TestReportCatalogsAreConsistent(t *testing.T) {
	for _, reason := range []string{"fraud", "inappropriate", "spam", "copyright", "impersonation", "other"} {
		if reportReasons[reason] == "" {
			t.Fatalf("신고 사유 %s에 표시 이름이 없습니다", reason)
		}
	}
	for _, action := range []string{"dismiss", "warn", "hide_talent", "suspend_user"} {
		if reportActions[action] == "" {
			t.Fatalf("조치 %s에 표시 이름이 없습니다", action)
		}
	}
	// Every reportable resource must be one the enforcement path understands.
	for resource := range reportResources {
		switch resource {
		case "talent", "user", "order", "message":
		default:
			t.Fatalf("처리 경로가 없는 신고 대상: %s", resource)
		}
	}
}

func TestReportActionsAreInTheEventCatalog(t *testing.T) {
	seen := map[string]bool{}
	for _, event := range eventCatalog {
		seen[event] = true
	}
	for _, event := range []string{"ReportResolved", "TalentPaused"} {
		if !seen[event] {
			t.Fatalf("%s 이벤트가 카탈로그에 없습니다", event)
		}
	}
}
