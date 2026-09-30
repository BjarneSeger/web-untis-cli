package webuntis

import (
	"strings"
	"testing"
	"time"
)

// rfcKey is the RFC 6238 test secret "12345678901234567890" in base32.
const rfcKey = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

func TestTOTPRFC6238(t *testing.T) {
	key, err := decodeSecret(rfcKey)
	if err != nil {
		t.Fatal(err)
	}
	vectors := map[int64]string{59: "287082", 1111111109: "081804", 1111111111: "050471", 1234567890: "005924", 2000000000: "279037", 20000000000: "353130"}
	for ts, want := range vectors {
		if got := totp(key, time.Unix(ts, 0)); got != want {
			t.Errorf("T=%d: got %s, want %s", ts, got, want)
		}
	}
}

func TestNormalizeSecret(t *testing.T) {
	if got := normalizeSecret(" gezd-gnbv gy3t\tqojq gezd\u00a0gnbv\u200bgy3t qojq== "); got != rfcKey {
		t.Fatalf("normalize: %q", got)
	}
	if _, err := decodeSecret("NOT*BASE32"); err == nil {
		t.Fatal("expected decode error")
	}
	if _, err := decodeSecret("GEZD"); err == nil {
		t.Fatal("expected too-short error")
	}
}

func TestParseSecret(t *testing.T) {
	tests := []struct {
		in, key, wantErr string
	}{
		{in: rfcKey, key: rfcKey},
		{in: "gezd gnbv gy3t qojq gezd gnbv gy3t qojq", key: rfcKey},
		{in: "GEZD-GNBV-GY3T-QOJQ", key: "GEZDGNBVGY3TQOJQ"},
		{in: "  ", wantErr: "empty"},
		{in: "untis://setschool?key=GEZDGNBVGY3TQOJQ", wantErr: "invalid Untis Mobile key"},
		{in: "GEZDGNBVGY3TQOJQX", wantErr: "invalid length"}, // 17 chars: tail would be dropped silently
		{in: "GEZDGNBV", wantErr: "too short"},
	}
	for _, tc := range tests {
		key, err := ParseSecret(tc.in)
		if tc.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("%q: expected error %q, got %v", tc.in, tc.wantErr, err)
			}
			if err != nil && strings.Contains(err.Error(), "GEZDGNBV") {
				t.Errorf("%q: error echoes the key: %v", tc.in, err)
			}
			continue
		}
		if err != nil || key != tc.key {
			t.Errorf("%q: got %q, %v", tc.in, key, err)
		}
	}
}
