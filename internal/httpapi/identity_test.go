package httpapi

import "testing"

// Matching on an email claim alone was an account takeover route: anyone who
// could get a provider to assert a victim's address would be signed straight
// into that account.
func TestDecideAccountLinkRefusesUnverifiedEmails(t *testing.T) {
	account := existingAccount{Found: true}
	decision, message := decideAccountLink(account, false, true)
	if decision != linkRefused || message == "" {
		t.Fatalf("확인되지 않은 이메일 결정=%v 안내=%q", decision, message)
	}
}

func TestDecideAccountLinkProtectsPasswordAndAdminAccounts(t *testing.T) {
	withPassword := existingAccount{Found: true, HasPassword: true}
	if decision, _ := decideAccountLink(withPassword, true, true); decision != linkRefused {
		t.Fatalf("비밀번호가 있는 계정 결정=%v", decision)
	}
	admin := existingAccount{Found: true, IsAdmin: true}
	if decision, _ := decideAccountLink(admin, true, true); decision != linkRefused {
		t.Fatalf("관리자 계정 결정=%v", decision)
	}
}

// An account created by another provider has no password of its own, so a
// verified address may join the two rather than fragmenting the person.
func TestDecideAccountLinkJoinsVerifiedPasswordlessAccounts(t *testing.T) {
	if decision, _ := decideAccountLink(existingAccount{Found: true}, true, true); decision != linkToExistingAccount {
		t.Fatalf("확인된 이메일의 소셜 전용 계정 결정=%v", decision)
	}
}

func TestDecideAccountLinkCreatesWhenNothingMatches(t *testing.T) {
	if decision, _ := decideAccountLink(existingAccount{}, true, true); decision != linkCreateNewAccount {
		t.Fatalf("일치하는 계정이 없을 때 결정=%v", decision)
	}
}

// Operators who want no email based joining at all can switch it off.
func TestDecideAccountLinkHonoursTheSetting(t *testing.T) {
	if decision, _ := decideAccountLink(existingAccount{Found: true}, true, false); decision != linkRefused {
		t.Fatalf("설정을 끈 상태의 결정=%v", decision)
	}
	if decision, _ := decideAccountLink(existingAccount{}, false, false); decision != linkCreateNewAccount {
		t.Fatal("일치하는 계정이 없으면 설정과 무관하게 새 계정을 만들어야 합니다")
	}
}
