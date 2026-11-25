// Package auth implements sessions, API tokens, CSRF and password hashing.
package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

// argon2id parameters (OWASP minimum profile: 19 MiB, t=2, p=1). Kept small
// because the server runs in a ~128 MB container.
const (
	argonMemory  = 19 * 1024
	argonTime    = 2
	argonThreads = 1
	argonKeyLen  = 32
	argonSaltLen = 16
)

// hashSlots bounds concurrent hashing so a burst of logins can't spike memory.
var hashSlots = make(chan struct{}, 2)

func acquire() func() {
	hashSlots <- struct{}{}
	return func() { <-hashSlots }
}

// HashPassword returns a PHC-format argon2id hash.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	release := acquire()
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	release()
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword checks password against an argon2id or Django pbkdf2_sha256
// hash. needsRehash is true when the hash is legacy (or uses old parameters)
// and should be replaced with HashPassword's output.
func VerifyPassword(password, encoded string) (ok, needsRehash bool) {
	switch {
	case strings.HasPrefix(encoded, "$argon2id$"):
		return verifyArgon2(password, encoded)
	case strings.HasPrefix(encoded, "pbkdf2_sha256$"):
		return verifyDjangoPBKDF2(password, encoded), true
	}
	return false, false
}

func verifyArgon2(password, encoded string) (bool, bool) {
	parts := strings.Split(encoded, "$") // "", argon2id, v=19, m=..,t=..,p=.., salt, hash
	if len(parts) != 6 {
		return false, false
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil || m > 256*1024 || t > 10 || p == 0 {
		return false, false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[4])
	want, err2 := base64.RawStdEncoding.DecodeString(parts[5])
	if err1 != nil || err2 != nil || len(want) == 0 {
		return false, false
	}
	release := acquire()
	got := argon2.IDKey([]byte(password), salt, t, m, p, uint32(len(want)))
	release()
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return false, false
	}
	return true, m != argonMemory || t != argonTime || p != argonThreads
}

// verifyDjangoPBKDF2 checks Django's "pbkdf2_sha256$<iterations>$<salt>$<b64 hash>".
func verifyDjangoPBKDF2(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 {
		return false
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations < 1 || iterations > 10_000_000 {
		return false
	}
	want, err := base64.StdEncoding.DecodeString(parts[3])
	if err != nil || len(want) == 0 {
		return false
	}
	release := acquire()
	got, err := pbkdf2.Key(sha256.New, password, []byte(parts[2]), iterations, len(want))
	release()
	return err == nil && subtle.ConstantTimeCompare(got, want) == 1
}
