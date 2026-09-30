// Package secrets resolves and stores the passwords of a profile. By default
// they live in the system keyring (service "webuntis-cli" for the WebUntis
// password, "webuntis-cli-secret" for the Untis Mobile key,
// "webuntis-cli-smtp" for the SMTP password, account = profile name). With
// --no-keyring they are kept in the profile's config.json (mode 0600) instead.
package secrets

import (
	"errors"
	"fmt"
	"os"

	"github.com/dgrieser/web-untis-cli/internal/config"
	"github.com/dgrieser/web-untis-cli/internal/keyring"
)

// Kind identifies a credential.
type Kind string

const (
	WebUntis     Kind = "webuntis"
	MobileSecret Kind = "secret" // Untis Mobile key (TOTP secret shown next to the QR code)
	SMTP         Kind = "smtp"
)

// Storage locations.
const (
	StoreKeyring = "keyring"
	StoreFile    = "file"
)

// Source tells where a secret came from.
type Source string

const (
	SourceNone    Source = ""
	SourceEnv     Source = "env"
	SourceFile    Source = "file"
	SourceKeyring Source = "keyring"
)

// Stores are the keyring stores per kind; tests replace them.
var Stores = map[Kind]keyring.Store{
	WebUntis:     keyring.System{},
	MobileSecret: keyring.System{ServiceName: keyring.Service + "-secret"},
	SMTP:         keyring.System{ServiceName: keyring.Service + "-smtp"},
}

// ErrNotFound reports that no secret is stored.
var ErrNotFound = errors.New("no password stored")

func envVar(k Kind) string {
	switch k {
	case SMTP:
		return "WEBUNTIS_SMTP_PASSWORD"
	case MobileSecret:
		return "WEBUNTIS_SECRET"
	}
	return "WEBUNTIS_PASSWORD"
}

func fileField(p *config.Profile, k Kind) *string {
	switch k {
	case SMTP:
		return &p.SMTP.Password
	case MobileSecret:
		return &p.Secret
	}
	return &p.Password
}

// Get resolves a secret: environment variable, then config file, then the
// system keyring (unless noKeyring or the profile uses file storage).
func Get(p *config.Profile, k Kind, noKeyring bool) (string, Source, error) {
	if v := os.Getenv(envVar(k)); v != "" {
		return v, SourceEnv, nil
	}
	if v := *fileField(p, k); v != "" {
		return v, SourceFile, nil
	}
	if noKeyring {
		return "", SourceNone, ErrNotFound
	}
	v, err := Stores[k].Get(p.Name)
	switch {
	case errors.Is(err, keyring.ErrNotFound):
		return "", SourceNone, ErrNotFound
	case err != nil && p.CredentialStore == StoreFile:
		// file-storage profile (e.g. headless box without Secret Service)
		return "", SourceNone, ErrNotFound
	case err != nil:
		return "", SourceNone, err
	}
	return v, SourceKeyring, nil
}

// Where reports where a secret is stored without returning it ("" if none).
func Where(p *config.Profile, k Kind, noKeyring bool) Source {
	if *fileField(p, k) != "" {
		return SourceFile
	}
	if !noKeyring {
		if _, err := Stores[k].Get(p.Name); err == nil {
			return SourceKeyring
		}
	}
	return SourceNone
}

// Set stores a secret in the keyring (or the config file with noKeyring) and
// updates p accordingly; the caller saves the profile.
func Set(p *config.Profile, k Kind, value string, noKeyring bool) error {
	if value == "" {
		return errors.New("empty password")
	}
	if noKeyring {
		p.CredentialStore = StoreFile
		*fileField(p, k) = value
		return nil
	}
	if err := Stores[k].Set(p.Name, value); err != nil {
		return fmt.Errorf("%w (use --no-keyring to store it in %s instead)", err, p.Path("config.json"))
	}
	p.CredentialStore = StoreKeyring
	*fileField(p, k) = ""
	return nil
}

// Delete removes a secret from the config file and the keyring.
func Delete(p *config.Profile, k Kind, noKeyring bool) error {
	*fileField(p, k) = ""
	if noKeyring {
		return nil
	}
	if err := Stores[k].Delete(p.Name); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return err
	}
	return nil
}

// DeleteAll removes all secrets of the profile.
func DeleteAll(p *config.Profile, noKeyring bool) error {
	var errs []error
	for _, k := range []Kind{WebUntis, MobileSecret, SMTP} {
		errs = append(errs, Delete(p, k, noKeyring))
	}
	return errors.Join(errs...)
}
