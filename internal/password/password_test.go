package password

import "testing"

func TestHashAndVerify(t *testing.T) {
	hash, err := Hash("this is a secure password")
	if err != nil {
		t.Fatal(err)
	}
	if !Verify(hash, "this is a secure password") {
		t.Fatal("valid password rejected")
	}
	if Verify(hash, "this is another password") {
		t.Fatal("invalid password accepted")
	}
}

func TestRejectsShortPassword(t *testing.T) {
	if _, err := Hash("too-short"); err == nil {
		t.Fatal("expected short password error")
	}
}
