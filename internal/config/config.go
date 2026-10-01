// Package config manages the on-disk state of webuntis-cli: profiles
// (school + credentials), persisted sessions, HTTP caches and small state
// files such as the list of already forwarded messages.
//
// Layout (default base directory: $XDG_CACHE_HOME/webuntis-cli, usually
// ~/.cache/webuntis-cli, override with $WEBUNTIS_CLI_HOME):
//
//	<base>/current                    name of the active profile
//	<base>/<profile>/config.json      school, server, credentials, SMTP settings
//	<base>/<profile>/session.json     cookies + JWT of the last session
//	<base>/<profile>/cache/           HTTP response cache
//	<base>/<profile>/forwarded.json   messages already forwarded via SMTP
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const DefaultProfile = "default"

// SMTPConfig holds the settings used by "messages forward".
type SMTPConfig struct {
	Host          string   `json:"host,omitempty"`
	Port          int      `json:"port,omitempty"`
	Username      string   `json:"username,omitempty"`
	Password      string   `json:"password,omitempty"` // file storage only, see Profile.CredentialStore
	From          string   `json:"from,omitempty"`
	To            []string `json:"to,omitempty"`
	Security      string   `json:"security,omitempty"` // starttls (default), tls, none
	SubjectPrefix string   `json:"subjectPrefix,omitempty"`
}

// Profile is one configured WebUntis login.
type Profile struct {
	Name              string `json:"-"`
	Server            string `json:"server"` // e.g. ge-huellhorst.webuntis.com
	School            string `json:"school"` // login name, e.g. ge-huellhorst
	TenantID          string `json:"tenantId,omitempty"`
	SchoolDisplayName string `json:"schoolDisplayName,omitempty"`
	Username          string `json:"username"`
	AuthMethod        string `json:"authMethod,omitempty"` // AuthPassword (default when empty) or AuthSecret
	// Password and Secret (base32 Untis Mobile key, shown next to the QR code)
	// are only set when credentials are stored in this file
	// (CredentialStore "file", i.e. --no-keyring).
	// Use package secrets to read or store them.
	Password string `json:"password,omitempty"`
	Secret   string `json:"secret,omitempty"`
	// CredentialStore is "keyring" (default) or "file" (--no-keyring).
	CredentialStore string     `json:"credentialStore,omitempty"`
	Student         string     `json:"student,omitempty"`  // default student (name or id) for parent accounts
	Timezone        string     `json:"timezone,omitempty"` // IANA name used for ics output, default: local
	SMTP            SMTPConfig `json:"smtp,omitzero"`
}

// Authentication methods stored in Profile.AuthMethod.
const (
	AuthPassword = "password" // JSON-RPC authenticate with username + password (default)
	AuthSecret   = "secret"   // Untis Mobile key (TOTP), works for Microsoft/SSO accounts
)

// Method returns the effective auth method (AuthPassword or AuthSecret).
// Legacy profiles without AuthMethod use the password unless a secret is stored.
func (p *Profile) Method() string {
	if p.AuthMethod == AuthSecret || (p.AuthMethod == "" && p.Secret != "") {
		return AuthSecret
	}
	return AuthPassword
}

// Cookie is a minimal persisted cookie.
type Cookie struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Path  string `json:"path,omitempty"`
}

// Session is the persisted authentication state.
type Session struct {
	// Cookies per host (e.g. "ge-huellhorst.webuntis.com", "klassengeld.app").
	Cookies  map[string][]Cookie `json:"cookies,omitempty"`
	Token    string              `json:"token,omitempty"`
	TokenExp time.Time           `json:"tokenExp,omitzero"`
	LoginAt  time.Time           `json:"loginAt,omitzero"`
}

// BaseDir returns the root directory for all webuntis-cli files.
func BaseDir() (string, error) {
	if d := os.Getenv("WEBUNTIS_CLI_HOME"); d != "" {
		return d, nil
	}
	d, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "webuntis-cli"), nil
}

var profileNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// ValidateProfileName makes sure a profile name is safe to use as directory.
func ValidateProfileName(name string) error {
	if !profileNameRe.MatchString(name) || name == "." || name == ".." {
		return fmt.Errorf("invalid profile name %q (allowed: letters, digits, . _ -)", name)
	}
	return nil
}

// CurrentProfileName returns the active profile (from $WEBUNTIS_PROFILE,
// the "current" file, or "default").
func CurrentProfileName() string {
	if p := os.Getenv("WEBUNTIS_PROFILE"); p != "" {
		return p
	}
	base, err := BaseDir()
	if err != nil {
		return DefaultProfile
	}
	b, err := os.ReadFile(filepath.Join(base, "current"))
	if err != nil {
		return DefaultProfile
	}
	if n := strings.TrimSpace(string(b)); n != "" {
		return n
	}
	return DefaultProfile
}

// SetCurrentProfile marks name as the active profile.
func SetCurrentProfile(name string) error {
	if err := ValidateProfileName(name); err != nil {
		return err
	}
	base, err := BaseDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(base, "current"), []byte(name+"\n"), 0o600)
}

// ListProfiles returns all profile names that have a config.json.
func ListProfiles() ([]string, error) {
	base, err := BaseDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(base)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(base, e.Name(), "config.json")); err == nil {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

// Dir returns the directory of a profile.
func Dir(profile string) (string, error) {
	if err := ValidateProfileName(profile); err != nil {
		return "", err
	}
	base, err := BaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, profile), nil
}

// ErrNotLoggedIn is returned when a profile has no configuration yet.
var ErrNotLoggedIn = errors.New("not logged in: run `webuntis login` first")

// Load reads a profile's config.json.
func Load(profile string) (*Profile, error) {
	dir, err := Dir(profile)
	if err != nil {
		return nil, err
	}
	var p Profile
	if err := readJSON(filepath.Join(dir, "config.json"), &p); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotLoggedIn
		}
		return nil, err
	}
	p.Name = profile
	return &p, nil
}

// Save writes a profile's config.json (mode 0600).
func (p *Profile) Save() error {
	dir, err := Dir(p.Name)
	if err != nil {
		return err
	}
	return WriteJSON(filepath.Join(dir, "config.json"), p)
}

// Path returns a file path inside the profile directory.
func (p *Profile) Path(elem ...string) string {
	dir, _ := Dir(p.Name)
	return filepath.Join(append([]string{dir}, elem...)...)
}

// LoadSession reads the persisted session (empty session if none).
func (p *Profile) LoadSession() *Session {
	var s Session
	_ = readJSON(p.Path("session.json"), &s)
	if s.Cookies == nil {
		s.Cookies = map[string][]Cookie{}
	}
	return &s
}

// SaveSession persists the session.
func (p *Profile) SaveSession(s *Session) error {
	return WriteJSON(p.Path("session.json"), s)
}

// ClearSession removes the persisted session.
func (p *Profile) ClearSession() error {
	err := os.Remove(p.Path("session.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Location returns the configured time zone (default: local).
func (p *Profile) Location() *time.Location {
	if p.Timezone != "" {
		if loc, err := time.LoadLocation(p.Timezone); err == nil {
			return loc
		}
	}
	return time.Local
}

// BaseURL returns https://<server>.
func (p *Profile) BaseURL() string {
	return "https://" + p.Server
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// WriteJSON atomically writes v as indented JSON with mode 0600.
func WriteJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // no-op after a successful rename
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// ReadJSON reads a JSON file into v.
func ReadJSON(path string, v any) error { return readJSON(path, v) }
