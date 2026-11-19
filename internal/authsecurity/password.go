package authsecurity

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/text/unicode/norm"
)

// Bound expensive password work independently of public request concurrency.
var passwordWork = make(chan struct{}, 4)
var dummyOnce sync.Once
var dummyPassword string

func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	passwordWork <- struct{}{}
	defer func() { <-passwordWork }()
	key := argon2.IDKey([]byte(norm.NFC.String(password)), salt, 2, 19*1024, 1, 32)
	return fmt.Sprintf("$argon2id$v=19$m=19456,t=2,p=1$%s$%s", base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// ComparePassword accepts existing bcrypt hashes without changing old passwords.
// New passwords use Argon2id, including long Unicode passphrases beyond bcrypt's
// 72-byte limit. Algorithm parameters are bounded before doing expensive work.
func ComparePassword(encoded, password string) error {
	if len(password) > 4096 {
		return errors.New("invalid password")
	}
	if strings.HasPrefix(encoded, "$2") {
		passwordWork <- struct{}{}
		defer func() { <-passwordWork }()
		return bcrypt.CompareHashAndPassword([]byte(encoded), []byte(password))
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" || parts[3] != "m=19456,t=2,p=1" {
		return errors.New("invalid password hash")
	}
	salt, e1 := base64.RawStdEncoding.DecodeString(parts[4])
	want, e2 := base64.RawStdEncoding.DecodeString(parts[5])
	if e1 != nil || e2 != nil || len(salt) != 16 || len(want) != 32 {
		return errors.New("invalid password hash")
	}
	passwordWork <- struct{}{}
	defer func() { <-passwordWork }()
	got := argon2.IDKey([]byte(norm.NFC.String(password)), salt, 2, 19*1024, 1, 32)
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return errors.New("invalid password")
	}
	return nil
}

func DummyPasswordHash() string {
	dummyOnce.Do(func() {
		var err error
		dummyPassword, err = HashPassword("dummy-constant-string")
		if err != nil {
			panic(err)
		}
	})
	return dummyPassword
}
