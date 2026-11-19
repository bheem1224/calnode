package authsecurity

import (
	_ "embed"
	"golang.org/x/text/unicode/norm"
	"strings"
	"unicode/utf8"
)

//go:embed passwords/common-passwords.txt
var commonPasswords string

var blockedPasswords = func() map[string]bool {
	out := map[string]bool{}
	for _, p := range strings.Fields(commonPasswords) {
		out[strings.ToLower(p)] = true
	}
	for _, p := range []string{"password1!", "password123!", "correct horse battery staple", "correcthorsebatterystaple", "passwordpassword", "passwordpassword1!", "thisisapassword", "thisismypassword", "letmeinletmein", "administrator123", "changemechangeme", "qwertyuiopasdfgh", "qwertyuiopasdfghjkl", "1234567890123456"} {
		out[p] = true
	}
	return out
}()

// BlockedPassword compares whole values, not words within a passphrase. Context
// entries are user/service names and common suffix variants, never sent off-site.
func BlockedPassword(password string, context ...string) bool {
	p := strings.ToLower(norm.NFC.String(password))
	if blockedPasswords[p] {
		return true
	}
	runes := []rune(p)
	if len(runes) > 0 {
		same := true
		for _, r := range runes {
			same = same && r == runes[0]
		}
		if same {
			return true
		}
	}
	for _, value := range append(context, "calnode") {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		for _, suffix := range []string{"", "1", "123", "123!", "!", "2026", "2026!", "2027", "2027!"} {
			if p == value+suffix {
				return true
			}
		}
	}
	return false
}

func PasswordLength(password string) int {
	if !utf8.ValidString(password) {
		return -1
	}
	return utf8.RuneCountInString(norm.NFC.String(password))
}
