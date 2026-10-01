package webuntis

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// totpStep is the RFC 6238 time step used by Untis Mobile.
const totpStep = 30 * time.Second

// normalizeSecret upper-cases a base32 key and strips whitespace (including
// non-breaking and zero-width spaces), dashes and '=' padding, so keys copied
// from the WebUntis UI in any form are accepted.
func normalizeSecret(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		switch {
		case unicode.IsSpace(r), r == '-', r == '=', r == 0x200b, r == 0xfeff:
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// decodeSecret base32-decodes a normalized Untis Mobile key.
func decodeSecret(s string) ([]byte, error) {
	switch len(s) % 8 {
	case 1, 3, 6: // not a valid base32 length; the decoder would silently drop the tail
		return nil, errors.New("not a base32 key: invalid length")
	}
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("not a base32 key: %w", err)
	}
	if len(key) < 10 {
		return nil, errors.New("key too short")
	}
	return key, nil
}

// ParseSecret validates the Untis Mobile key shown next to the QR code in
// WebUntis (Profil → Freigaben → "Zugriff über Untis Mobile") and returns it
// normalized (upper-case base32 without spaces or padding). The error never
// contains the input.
func ParseSecret(s string) (string, error) {
	key := normalizeSecret(s)
	if key == "" {
		return "", errors.New("empty Untis Mobile key")
	}
	if _, err := decodeSecret(key); err != nil {
		return "", fmt.Errorf("invalid Untis Mobile key (letters A-Z and digits 2-7 expected): %w", err)
	}
	return key, nil
}

// totpAt computes the RFC 6238 code (HMAC-SHA1, 6 digits) for a counter.
func totpAt(key []byte, counter uint64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	code := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", code%1000000)
}

// totp computes the code for time t with a 30 s step.
func totp(key []byte, t time.Time) string {
	return totpAt(key, uint64(t.Unix())/uint64(totpStep/time.Second))
}
