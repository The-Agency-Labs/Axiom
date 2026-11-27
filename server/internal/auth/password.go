// Package auth implements sessions, API tokens, CSRF and password hashing.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"runtime/debug"
	"strings"

	"golang.org/x/crypto/argon2"
)

// argon2id parameters: the memory-constrained recommendation of RFC 9106
// (64 MiB, t=3, p=4). The other one needs 2 GiB, far more than the container has.
const (
	argonMemory  = 64 * 1024
	argonTime    = 3
	argonThreads = 4
	argonKeyLen  = 32
	argonSaltLen = 16
)

// hashSlots allows one hash at a time: two 64 MiB working sets would not fit in
// the container, so a burst of logins queues instead.
var hashSlots = make(chan struct{}, 1)

func acquire() func() {
	hashSlots <- struct{}{}
	return func() { <-hashSlots }
}

// argonHash runs argon2id and then hands its working set back to the OS before the
// next hash can start. Logins are rare enough that the forced GC is irrelevant.
func argonHash(password, salt []byte, t, m uint32, p uint8, n uint32) []byte {
	release := acquire()
	defer release()
	key := argon2.IDKey(password, salt, t, m, p, n)
	debug.FreeOSMemory()
	return key
}

// HashPassword returns a PHC-format argon2id hash.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argonHash([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword checks password against an argon2id hash. needsRehash is true
// when the hash uses other parameters and should be replaced with HashPassword's
// output.
func VerifyPassword(password, encoded string) (ok, needsRehash bool) {
	if !strings.HasPrefix(encoded, "$argon2id$") {
		return false, false
	}
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
	got := argonHash([]byte(password), salt, t, m, p, uint32(len(want)))
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return false, false
	}
	return true, m != argonMemory || t != argonTime || p != argonThreads
}
