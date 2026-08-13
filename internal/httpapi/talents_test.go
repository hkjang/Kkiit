package httpapi

import "testing"

func TestValidateTalentNormalizesOptionalJSONFields(t *testing.T) {
	in := talentInput{
		Title:        "Go API review",
		BasePrice:    100_000,
		DeliveryDays: 2,
		Packages: []packageInput{{
			Type:         "BASIC",
			Name:         "기본",
			Price:        100_000,
			DeliveryDays: 2,
		}},
		Requirements: []requirementInput{{
			Label:     "검토할 저장소",
			FieldType: "text",
			Required:  true,
		}},
	}
	if !validateTalent(&in) {
		t.Fatal("expected valid talent")
	}
	if in.Tags == nil || in.Packages[0].Features == nil || in.Requirements[0].Options == nil {
		t.Fatal("optional JSON fields must be normalized instead of being stored as SQL NULL")
	}
}

func TestValidateTalentRejectsInvalidStructuredFields(t *testing.T) {
	in := talentInput{
		Title:        "Go API review",
		BasePrice:    100_000,
		DeliveryDays: 2,
		Requirements: []requirementInput{{Label: "bad", FieldType: "script"}},
	}
	if validateTalent(&in) {
		t.Fatal("unsupported Smart Order Form field type must be rejected")
	}
}
