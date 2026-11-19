package authsecurity

import (
	"golang.org/x/crypto/bcrypt"
	"strings"
	"testing"
)

func TestPasswordHashSaltUnicodeAndLegacy(t *testing.T) {
	password := strings.Repeat("界", 64)
	a, err := HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	b, err := HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if a == b || !strings.HasPrefix(a, "$argon2id$") {
		t.Fatal("new credentials need random salts and versioned hashes")
	}
	if ComparePassword(a, password) != nil || ComparePassword(a, password+"x") == nil {
		t.Fatal("password verification or full-length comparison failed")
	}
	legacy, _ := bcrypt.GenerateFromPassword([]byte("old-password"), bcrypt.MinCost)
	if ComparePassword(string(legacy), "old-password") != nil {
		t.Fatal("legacy credentials stopped working")
	}
	normalized, _ := HashPassword(strings.Repeat("é", 16))
	if ComparePassword(normalized, strings.Repeat("e\u0301", 16)) != nil {
		t.Fatal("NFC-equivalent passwords differ")
	}
	for _, raw := range []string{"", "$argon2id$v=19$m=999999999,t=2,p=1$a$b", "$argon2id$v=19$m=19456,t=2,p=1$%%%$%%%"} {
		if ComparePassword(raw, password) == nil {
			t.Fatal("malformed hash accepted")
		}
	}
}

func TestPasswordBlocklistWholeValues(t *testing.T) {
	for _, p := range []string{"password", "Password1!", "correct horse battery staple", strings.Repeat("z", 16), "SecretCompany2026!"} {
		if !BlockedPassword(p, "SecretCompany") {
			t.Fatalf("predictable password accepted: %q", p)
		}
	}
	if BlockedPassword("elephant meadow quartz harbor") {
		t.Fatal("independent passphrase rejected")
	}
	if PasswordLength(strings.Repeat("界", 16)) != 16 || PasswordLength(string([]byte{0xff})) != -1 {
		t.Fatal("password length must count Unicode characters")
	}
}
