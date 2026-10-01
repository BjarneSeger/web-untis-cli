package secrets

import (
	"errors"
	"testing"

	gokeyring "github.com/zalando/go-keyring"

	"github.com/dgrieser/web-untis-cli/internal/config"
	"github.com/dgrieser/web-untis-cli/internal/keyring"
)

// setup uses the in-memory keyring mock; no test may touch the real keyring.
func setup(t *testing.T) *config.Profile {
	t.Helper()
	gokeyring.MockInit()
	t.Cleanup(gokeyring.MockInit)
	t.Setenv("WEBUNTIS_CLI_HOME", t.TempDir())
	t.Setenv("WEBUNTIS_PASSWORD", "")
	t.Setenv("WEBUNTIS_SECRET", "")
	t.Setenv("WEBUNTIS_SMTP_PASSWORD", "")
	return &config.Profile{Name: "default", Server: "s", School: "x", Username: "u"}
}

func TestKeyringRoundTrip(t *testing.T) {
	p := setup(t)
	if err := Set(p, WebUntis, "pw", false); err != nil {
		t.Fatal(err)
	}
	if p.Password != "" || p.CredentialStore != StoreKeyring {
		t.Fatalf("password must not be kept in the profile: %+v", p)
	}
	if v, err := gokeyring.Get(keyring.Service, "default"); err != nil || v != "pw" {
		t.Fatalf("keyring entry = %q, %v", v, err)
	}
	v, src, err := Get(p, WebUntis, false)
	if err != nil || v != "pw" || src != SourceKeyring {
		t.Fatalf("Get = %q %s %v", v, src, err)
	}
	if Where(p, SMTP, false) != SourceNone {
		t.Fatal("SMTP password must be separate")
	}
	if err := Delete(p, WebUntis, false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Get(p, WebUntis, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
}

func TestFileStorage(t *testing.T) {
	p := setup(t)
	if err := Set(p, SMTP, "smtp-pw", true); err != nil {
		t.Fatal(err)
	}
	if p.SMTP.Password != "smtp-pw" || p.CredentialStore != StoreFile {
		t.Fatalf("file storage: %+v", p)
	}
	if _, err := gokeyring.Get(keyring.Service+"-smtp", "default"); !errors.Is(err, gokeyring.ErrNotFound) {
		t.Fatal("--no-keyring must not write to the keyring")
	}
	v, src, _ := Get(p, SMTP, true)
	if v != "smtp-pw" || src != SourceFile {
		t.Fatalf("Get = %q %s", v, src)
	}
}

func TestEnvOverrides(t *testing.T) {
	p := setup(t)
	_ = Set(p, WebUntis, "stored", false)
	t.Setenv("WEBUNTIS_PASSWORD", "env")
	if v, src, _ := Get(p, WebUntis, false); v != "env" || src != SourceEnv {
		t.Fatalf("Get = %q %s", v, src)
	}
}

func TestNoKeyringSkipsKeyringOnRead(t *testing.T) {
	p := setup(t)
	_ = Set(p, WebUntis, "stored", false)
	if _, _, err := Get(p, WebUntis, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("--no-keyring must not read the keyring: %v", err)
	}
}

func TestMobileSecret(t *testing.T) {
	p := setup(t)
	if err := Set(p, MobileSecret, "KEY", false); err != nil {
		t.Fatal(err)
	}
	if p.Secret != "" {
		t.Fatalf("key must not be kept in the profile: %+v", p)
	}
	if v, err := gokeyring.Get(keyring.Service+"-secret", "default"); err != nil || v != "KEY" {
		t.Fatalf("keyring entry = %q, %v", v, err)
	}
	if Where(p, WebUntis, false) != SourceNone {
		t.Fatal("key must be separate from the password")
	}
	t.Setenv("WEBUNTIS_SECRET", "ENV")
	if v, src, _ := Get(p, MobileSecret, false); v != "ENV" || src != SourceEnv {
		t.Fatalf("Get = %q %s", v, src)
	}
	if err := Set(p, MobileSecret, "FILEKEY", true); err != nil || p.Secret != "FILEKEY" {
		t.Fatalf("file storage: %v %+v", err, p)
	}
}

func TestDeleteAll(t *testing.T) {
	p := setup(t)
	_ = Set(p, WebUntis, "a", false)
	_ = Set(p, MobileSecret, "k", false)
	_ = Set(p, SMTP, "b", false)
	if err := DeleteAll(p, false); err != nil {
		t.Fatal(err)
	}
	if Where(p, WebUntis, false) != SourceNone || Where(p, MobileSecret, false) != SourceNone || Where(p, SMTP, false) != SourceNone {
		t.Fatal("secrets left after DeleteAll")
	}
}
