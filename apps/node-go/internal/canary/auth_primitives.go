package canary

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters follow OWASP's minimum recommendation. At most two
// password operations run concurrently, outside the monitoring state lock.
const passwordMemory = 19 * 1024
const sessionLifetimeMs int64 = 12 * 60 * 60 * 1000

type loginSession struct {
	AccountID   string `json:"accountId"`
	CreatedAt   int64  `json:"createdAt"`
	ExpiresAt   int64  `json:"expiresAt"`
	AuthVersion uint64 `json:"authVersion"`
}
type loginLimit struct {
	Since int64
	Count int
}

func randomCredential(size int) (string, error) {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return "", fail(503, "Cryptographic randomness unavailable")
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func passwordValue(value string) error {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) < 12 || utf8.RuneCountInString(value) > 128 || strings.ContainsAny(value, "\x00\r\n") {
		return fail(400, "Passwords must contain 12 to 128 characters without line breaks")
	}
	return nil
}

func setPassword(a account, password string) (account, error) {
	if err := passwordValue(password); err != nil {
		return a, err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return a, fail(503, "Cryptographic randomness unavailable")
	}
	hash := argon2.IDKey([]byte(password), salt, 2, passwordMemory, 1, 32)
	a.PasswordSalt = base64.RawStdEncoding.EncodeToString(salt)
	a.PasswordHash = base64.RawStdEncoding.EncodeToString(hash)
	a.PasswordVersion = 1
	a.PasswordEnabled = true
	return a, nil
}

func verifyPassword(a account, password string) bool {
	salt, e1 := base64.RawStdEncoding.DecodeString(a.PasswordSalt)
	expected, e2 := base64.RawStdEncoding.DecodeString(a.PasswordHash)
	valid := e1 == nil && e2 == nil && len(salt) == 16 && len(expected) == 32 && a.PasswordVersion == 1
	if !valid {
		salt, expected = make([]byte, 16), make([]byte, 32)
	}
	actual := argon2.IDKey([]byte(password), salt, 2, passwordMemory, 1, 32)
	return subtle.ConstantTimeCompare(actual, expected) == 1 && valid
}

func totpCode(secret string, step int64) string {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil || len(key) != 20 || step < 0 {
		return ""
	}
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(step))
	h := hmac.New(sha1.New, key)
	_, _ = h.Write(counter[:])
	sum := h.Sum(nil)
	offset := sum[len(sum)-1] & 15
	number := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", number%1000000)
}

func verifyTOTP(secret, code string, now, previous int64) (int64, bool) {
	if len(code) != 6 || strings.ContainsAny(code, " \r\n\t") {
		return 0, false
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	for _, delta := range []int64{0, -1, 1} {
		step := now/30000 + delta
		if step <= previous {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(totpCode(secret, step)), []byte(code)) == 1 {
			return step, true
		}
	}
	return 0, false
}

func newTOTP(name string) (string, string, error) {
	key := make([]byte, 20)
	if _, err := rand.Read(key); err != nil {
		return "", "", fail(503, "Cryptographic randomness unavailable")
	}
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(key)
	u := &url.URL{Scheme: "otpauth", Host: "totp", Path: "/Uptime Canary:" + name}
	q := url.Values{"secret": {secret}, "issuer": {"Uptime Canary"}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}
	u.RawQuery = q.Encode()
	return secret, u.String(), nil
}
