package cli

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/dgrieser/web-untis-cli/internal/config"
	"github.com/dgrieser/web-untis-cli/internal/webuntis"
)

const testKey = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

// withStdin replaces os.Stdin with a pipe that yields content (never a terminal).
func withStdin(t *testing.T, content string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.WriteString(content)
	_ = w.Close()
	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = old; _ = r.Close() })
}

func TestSecretInput(t *testing.T) {
	p := &config.Profile{Name: "kid", Server: "x.webuntis.com", School: "x", Username: "max"}
	secretProfile := func(server, user, key string) *config.Profile {
		return &config.Profile{Name: "kid", Server: server, School: "X", Username: user, AuthMethod: config.AuthSecret, Secret: key}
	}
	passwordProfile := &config.Profile{Name: "kid", Server: "x.webuntis.com", School: "x", Username: "Max"}
	tests := []struct {
		name          string
		flags         loginFlags
		envSecret     string
		envPassword   string
		stdin         string
		existing      *config.Profile
		wantKey       string
		wantOK        bool
		wantErrSubstr string
	}{
		{name: "stdin", flags: loginFlags{secretStdin: true}, stdin: "gezd gnbv gy3t qojq gezd gnbv gy3t qojq\n", wantKey: testKey, wantOK: true},
		{name: "stdin invalid", flags: loginFlags{secretStdin: true}, stdin: "untis://setschool?key=" + testKey + "\n", wantErrSubstr: "invalid Untis Mobile key"},
		{name: "prompt without terminal", flags: loginFlags{secret: true}, wantErrSubstr: "no terminal"},
		{name: "--secret uses env", flags: loginFlags{secret: true}, envSecret: testKey, envPassword: "pw", wantKey: testKey, wantOK: true},
		{name: "password-stdin wins over env", flags: loginFlags{pwStdin: true}, envSecret: testKey},
		{name: "env", envSecret: strings.ToLower(testKey), wantKey: testKey, wantOK: true},
		{name: "both env, new account", envSecret: testKey, envPassword: "pw", wantErrSubstr: "both WEBUNTIS_SECRET and WEBUNTIS_PASSWORD"},
		{name: "both env, key profile", envSecret: testKey, envPassword: "pw", existing: secretProfile("x.webuntis.com", "max", ""), wantKey: testKey, wantOK: true},
		{name: "stored key, same account", existing: secretProfile("x.webuntis.com", "max", testKey), wantKey: testKey, wantOK: true},
		{name: "stored key, other server", existing: secretProfile("evil.example", "max", testKey), wantErrSubstr: "no terminal"},
		{name: "stored key, other user", existing: secretProfile("x.webuntis.com", "jane", testKey)},
		{name: "stored key, password env", existing: secretProfile("x.webuntis.com", "max", testKey), envPassword: "pw"},
		{name: "no stored key, no terminal", existing: secretProfile("x.webuntis.com", "max", ""), wantErrSubstr: "no terminal"},
		{name: "password profile ignores env key", existing: passwordProfile, envSecret: testKey},
		{name: "password profile", existing: passwordProfile},
		{name: "new profile"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("WEBUNTIS_SECRET", tc.envSecret)
			t.Setenv("WEBUNTIS_PASSWORD", tc.envPassword)
			withStdin(t, tc.stdin)
			a := &app{noKeyring: true}
			key, ok, err := a.secretInput(tc.flags, p, tc.existing)
			if tc.wantErrSubstr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErrSubstr) {
					t.Fatalf("expected error %q, got %v", tc.wantErrSubstr, err)
				}
				if strings.Contains(err.Error(), testKey[:8]) {
					t.Fatalf("error echoes the key: %v", err)
				}
				return
			}
			if err != nil || key != tc.wantKey || ok != tc.wantOK {
				t.Fatalf("got %q %v %v, want %q %v", key, ok, err, tc.wantKey, tc.wantOK)
			}
		})
	}
}

func TestRunLoginInputChecks(t *testing.T) {
	t.Setenv("WEBUNTIS_CLI_HOME", t.TempDir())
	t.Setenv("WEBUNTIS_SECRET", "")
	t.Setenv("WEBUNTIS_PASSWORD", "")
	withStdin(t, "")
	a := &app{profile: "kid", noKeyring: true}
	ctx := context.Background()

	err := a.runLogin(ctx, loginFlags{secret: true}, testKey)
	if err == nil || !strings.Contains(err.Error(), "do not pass the Untis Mobile key as argument") || strings.Contains(err.Error(), testKey) {
		t.Fatalf("expected key-as-argument error, got %v", err)
	}
	if err := a.runLogin(ctx, loginFlags{secretStdin: true}, ""); err == nil || !strings.Contains(err.Error(), "school missing") {
		t.Fatalf("expected school missing error, got %v", err)
	}
}

func TestLooksLikeKey(t *testing.T) {
	for s, want := range map[string]bool{testKey: true, "GEZDGNBVGY3TQOJQ": true, "": false, "ge-huellhorst": false,
		"x.webuntis.com": false, "https://x.webuntis.com": false, "GEZD": false, strings.ToLower(testKey): false} {
		if got := looksLikeKey(s); got != want {
			t.Errorf("looksLikeKey(%q) = %v", s, got)
		}
	}
}

func TestProfileFromWizard(t *testing.T) {
	school := webuntis.School{Server: "x.webuntis.com", LoginName: "x", TenantID: "1", DisplayName: "X-Schule"}
	p, cred := profileFromWizard("kid", school, config.AuthSecret, " max ", "ignored", "gezd-gnbv-gy3t-qojq")
	if cred != "GEZDGNBVGY3TQOJQ" || p.AuthMethod != config.AuthSecret || p.Username != "max" || p.Server != school.Server || p.School != "x" {
		t.Fatalf("secret: %q %+v", cred, p)
	}
	p, cred = profileFromWizard("kid", school, config.AuthPassword, "max", "pw", "")
	if cred != "pw" || p.AuthMethod != config.AuthPassword {
		t.Fatalf("password: %q %+v", cred, p)
	}
}
