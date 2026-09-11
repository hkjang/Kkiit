package httpapi

import "testing"

func TestPortfolioInputNormalizesOptionalFields(t *testing.T) {
	in := portfolioInput{Title: "  브랜드 리뉴얼  "}
	if message, ok := in.validate(); !ok {
		t.Fatalf("유효한 입력이 거부되었습니다: %s", message)
	}
	if in.Title != "브랜드 리뉴얼" {
		t.Fatalf("제목 공백이 정리되지 않았습니다: %q", in.Title)
	}
	if in.Media == nil || in.Tags == nil {
		t.Fatal("빈 미디어와 태그는 SQL NULL이 아니라 빈 배열이어야 합니다")
	}
}

func TestPortfolioInputRejectsUnusableValues(t *testing.T) {
	long := make([]rune, 4001)
	for i := range long {
		long[i] = '가'
	}
	cases := map[string]portfolioInput{
		"제목 없음":  {},
		"설명 초과":  {Title: "작업", Description: string(long)},
		"미디어 초과": {Title: "작업", Media: make([]map[string]any, 13)},
		"태그 초과":  {Title: "작업", Tags: make([]string, 21)},
	}
	for name, in := range cases {
		input := in
		if _, ok := input.validate(); ok {
			t.Fatalf("%s: 거부되어야 합니다", name)
		}
	}
}
