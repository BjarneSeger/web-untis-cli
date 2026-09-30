package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/term"

	"github.com/dgrieser/web-untis-cli/internal/config"
	"github.com/dgrieser/web-untis-cli/internal/mailer"
	"github.com/dgrieser/web-untis-cli/internal/render"
	"github.com/dgrieser/web-untis-cli/internal/secrets"
	"github.com/dgrieser/web-untis-cli/internal/webuntis"
)

// ---------------------------------------------------------------- login

func prompt(label string) (string, error) {
	fmt.Fprint(os.Stderr, label)
	r := bufio.NewReader(os.Stdin)
	s, err := r.ReadString('\n')
	if err != nil && s == "" {
		return "", err
	}
	return strings.TrimSpace(s), nil
}

// promptHidden prompts on stderr and reads a line from the terminal without
// echo; noTerminal is the error when stdin is not a terminal.
func promptHidden(label, noTerminal string) (string, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", errors.New(noTerminal)
	}
	fmt.Fprint(os.Stderr, label)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	return string(b), err
}

func promptPassword(label string) (string, error) {
	return promptHidden(label, "no terminal for password prompt: use --password-stdin or WEBUNTIS_PASSWORD")
}

func promptKey(label string) (string, error) {
	return promptHidden(label, "no terminal for the Untis Mobile key prompt: use --secret-stdin or WEBUNTIS_SECRET")
}

// readStdinSecret reads a secret line from stdin. On a terminal it prompts
// with label and hides the input.
func readStdinSecret(label string) (string, error) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		return promptHidden(label, "")
	}
	b, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && b == "" {
		return "", err
	}
	return strings.TrimRight(b, "\r\n"), nil
}

// resolveSchool turns "https://x.webuntis.com/...", "x.webuntis.com",
// "x.webuntis.com?school=y" or a school name into server + login name.
func resolveSchool(ctx context.Context, input, schoolFlag string) (server, school, tenant, display string, err error) {
	in := strings.TrimSpace(input)
	if in != "" && !strings.Contains(in, "://") && strings.Contains(in, ".") {
		in = "https://" + in
	}
	if u, perr := url.Parse(in); perr == nil && u.Host != "" {
		server = u.Host
		school = firstNonEmpty(schoolFlag, u.Query().Get("school"))
		if school == "" {
			// New-style hosts: <school>.webuntis.com
			school = strings.Split(server, ".")[0]
		}
	} else {
		school = firstNonEmpty(schoolFlag, in)
	}
	if school == "" {
		return "", "", "", "", errors.New("school required")
	}
	// Verify / enrich via the public school search.
	res, serr := webuntis.SearchSchools(ctx, school)
	if serr == nil {
		for _, s := range res {
			if strings.EqualFold(s.LoginName, school) && (server == "" || strings.EqualFold(s.Server, server)) {
				return s.Server, s.LoginName, s.TenantID, s.DisplayName, nil
			}
		}
		if server == "" && len(res) == 1 {
			s := res[0]
			return s.Server, s.LoginName, s.TenantID, s.DisplayName, nil
		}
		if server == "" && len(res) > 1 {
			var names []string
			for _, s := range res {
				names = append(names, fmt.Sprintf("%s (%s, %s)", s.DisplayName, s.LoginName, s.Server))
			}
			return "", "", "", "", fmt.Errorf("several schools match %q, use --school <loginName> or the school URL:\n  %s", school, strings.Join(names, "\n  "))
		}
	}
	if server == "" {
		return "", "", "", "", fmt.Errorf("school %q not found (try `webuntis schools search <name>`)", school)
	}
	return server, school, "", "", nil
}

func firstNonEmpty(s ...string) string {
	for _, x := range s {
		if strings.TrimSpace(x) != "" {
			return strings.TrimSpace(x)
		}
	}
	return ""
}

// loginFlags are the parsed flags of "webuntis login".
type loginFlags struct {
	school, user                          string
	pwStdin, secret, secretStdin, noStore bool
}

func (a *app) loginCmd() *cobra.Command {
	var f loginFlags
	cmd := &cobra.Command{
		Use:   "login [SCHOOL-URL | SERVER | SCHOOL]",
		Short: "Log in and store school + credentials",
		Long: `Log in to WebUntis. School and username are stored in the profile
(~/.cache/webuntis-cli/<profile>/config.json), the password (or the Untis
Mobile key) in the system keyring (Secret Service / macOS Keychain /
Windows Credential Manager) once WebUntis accepted it. With --no-keyring it
goes to config.json (mode 0600).

Without arguments on a terminal, an interactive setup starts (same as
"webuntis setup"): pick/create a profile, search the school, enter
credentials, choose the default student.

The school can be given as URL (https://ge-huellhorst.webuntis.com/today),
host name, or school login name. Use --no-store-password to only keep the
session cookie (you will have to log in again when it expires).

Accounts that can only sign in via Microsoft/Office 365 (typically students)
have no WebUntis password. Use the Untis Mobile key instead: in WebUntis open
Profil → Freigaben → "Zugriff über Untis Mobile" → Anzeigen and copy the key
shown next to the QR code. Enter it with --secret (hidden prompt),
--secret-stdin or $WEBUNTIS_SECRET, or choose "Microsoft / SSO" in
"webuntis setup". "webuntis login SCHOOL" reuses the stored key of such a
profile; --secret enters a new one.

When reading the password or key from stdin, pass the school and -u: there
is no prompt for them.`,
		Example: `  webuntis login https://ge-huellhorst.webuntis.com
  webuntis login ge-huellhorst --user jane@example.com
  echo "$PW" | webuntis login ge-huellhorst -u jane --password-stdin
  webuntis login -p kid2 neilo.webuntis.com --school my-school
  webuntis login -p kid1 ge-huellhorst -u max --secret
  echo "$KEY" | webuntis login -p kid1 ge-huellhorst -u max --secret-stdin`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			arg := ""
			if len(args) > 0 {
				arg = args[0]
			}
			return a.runLogin(cmd.Context(), f, arg)
		},
	}
	cmd.Flags().StringVar(&f.school, "school", "", "school login name (if not part of the URL)")
	cmd.Flags().StringVarP(&f.user, "user", "u", "", "username")
	cmd.Flags().BoolVar(&f.pwStdin, "password-stdin", false, "read the password from stdin")
	cmd.Flags().BoolVar(&f.secret, "secret", false, "log in with the Untis Mobile key (Microsoft/SSO accounts); prompts for it")
	cmd.Flags().BoolVar(&f.secretStdin, "secret-stdin", false, "read the Untis Mobile key from stdin (Microsoft/SSO accounts)")
	cmd.Flags().BoolVar(&f.noStore, "no-store-password", false, "do not store the password / Untis Mobile key (neither keyring nor file; only the session is kept)")
	cmd.MarkFlagsMutuallyExclusive("password-stdin", "secret", "secret-stdin")
	return cmd
}

func (a *app) runLogin(ctx context.Context, f loginFlags, arg string) error {
	name := a.profileName()
	if err := config.ValidateProfileName(name); err != nil {
		return err
	}
	if arg == "" && f.school == "" && f.user == "" && !f.pwStdin && !f.secret && !f.secretStdin && interactive() {
		return a.runSetup(ctx, f.noStore)
	}
	if (f.secret || f.secretStdin) && looksLikeKey(arg) {
		return errors.New("do not pass the Untis Mobile key as argument: --secret prompts for it, --secret-stdin reads it from stdin")
	}
	stdin := f.pwStdin || f.secretStdin // stdin carries the credential, so no prompts
	existing, _ := config.Load(name)
	input, school := arg, f.school
	if input == "" && school == "" && existing != nil {
		input, school = existing.Server, existing.School
	}
	if input == "" && school == "" {
		if stdin {
			return errors.New("school missing: pass it as argument when reading the credential from stdin")
		}
		var err error
		if input, err = prompt("School (URL, server or name): "); err != nil {
			return err
		}
	}
	server, loginName, tenant, display, err := resolveSchool(ctx, input, school)
	if err != nil {
		return err
	}
	user := f.user
	if user == "" && existing != nil && strings.EqualFold(existing.School, loginName) {
		user = existing.Username
	}
	if user == "" {
		if stdin {
			return errors.New("username missing: pass -u when reading the credential from stdin")
		}
		if user, err = prompt("Username: "); err != nil {
			return err
		}
	}
	p := &config.Profile{Name: name, Server: server, School: loginName, TenantID: tenant, SchoolDisplayName: display, Username: user,
		AuthMethod: config.AuthSecret}
	credential, useSecret, err := a.secretInput(f, p, existing)
	if err != nil {
		return err
	}
	if !useSecret {
		p.AuthMethod = config.AuthPassword
		if credential, err = passwordInput(f, user, loginName); err != nil {
			return err
		}
	}
	ad, err := a.performLogin(ctx, p, existing, credential, f.noStore)
	if err != nil {
		return err
	}
	return a.printLoginSummary(p, ad)
}

// sameAccount reports whether p is the same WebUntis account as existing.
func sameAccount(existing, p *config.Profile) bool {
	return existing != nil && strings.EqualFold(existing.School, p.School) && strings.EqualFold(existing.Username, p.Username)
}

// looksLikeKey reports whether s looks like an Untis Mobile key (upper-case
// base32) rather than a school URL, host or login name.
func looksLikeKey(s string) bool {
	if s == "" || s != strings.ToUpper(s) || strings.ContainsAny(s, ".:/") {
		return false
	}
	_, err := webuntis.ParseSecret(s)
	return err == nil
}

// secretInput resolves the Untis Mobile key for p. Explicit flags win
// (--secret-stdin; --secret uses $WEBUNTIS_SECRET or prompts). Otherwise an
// existing profile of the same account keeps its login method: password
// profiles use the password path, key profiles $WEBUNTIS_SECRET or the stored
// key. New accounts use $WEBUNTIS_SECRET if set. ok is false when the
// password path should be used instead.
func (a *app) secretInput(f loginFlags, p, existing *config.Profile) (key string, ok bool, err error) {
	label := fmt.Sprintf("Untis Mobile key for %s (school %s): ", p.Username, p.School)
	envSecret, envPassword := os.Getenv("WEBUNTIS_SECRET"), os.Getenv("WEBUNTIS_PASSWORD")
	known := sameAccount(existing, p)
	var raw string
	switch {
	case f.secretStdin:
		raw, err = readStdinSecret(label)
	case f.secret:
		if raw = envSecret; raw == "" {
			raw, err = promptKey(label)
		}
	case f.pwStdin, known && existing.Method() != config.AuthSecret:
		return "", false, nil
	case !known && envSecret != "" && envPassword != "":
		return "", false, errors.New("both WEBUNTIS_SECRET and WEBUNTIS_PASSWORD are set: unset one of them, or use --secret / --password-stdin")
	case envSecret != "":
		raw = envSecret
	case known && envPassword != "":
		return "", false, nil // switch a key profile to the password
	case known:
		raw, err = a.storedKey(existing, p, label)
	default:
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if key, err = webuntis.ParseSecret(raw); err != nil {
		return "", false, err
	}
	return key, true, nil
}

// storedKey returns the stored Untis Mobile key of existing. It prompts for
// the key when none is stored, the keyring fails, or the server changed (the
// stored key is not sent to another host unasked).
func (a *app) storedKey(existing, p *config.Profile, label string) (string, error) {
	if strings.EqualFold(existing.Server, p.Server) {
		key, _, err := secrets.Get(existing, secrets.MobileSecret, a.noKeyring)
		if err == nil {
			return key, nil
		}
		if !errors.Is(err, secrets.ErrNotFound) {
			fmt.Fprintln(os.Stderr, "Warning:", err)
		}
	}
	return promptKey(label)
}

// passwordInput reads the password from stdin, $WEBUNTIS_PASSWORD or a prompt.
func passwordInput(f loginFlags, user, school string) (string, error) {
	switch {
	case f.pwStdin:
		return readStdinSecret(fmt.Sprintf("Password for %s (school %s): ", user, school))
	case os.Getenv("WEBUNTIS_PASSWORD") != "":
		return os.Getenv("WEBUNTIS_PASSWORD"), nil
	default:
		return promptPassword(fmt.Sprintf("Password for %s (school %s): ", user, school))
	}
}

// performLogin logs in with p and credential (the password or the Untis
// Mobile secret, see p.AuthMethod), stores the credential (system keyring, or
// config.json with --no-keyring) only after the server accepted it, saves the
// profile and makes it current (unless --profile was given).
func (a *app) performLogin(ctx context.Context, p, existing *config.Profile, credential string, noStore bool) (*webuntis.AppData, error) {
	if existing != nil {
		p.SMTP, p.Student, p.Timezone, p.CredentialStore = existing.SMTP, existing.Student, existing.Timezone, existing.CredentialStore
		if sameAccount(existing, p) {
			p.Password, p.Secret = existing.Password, existing.Secret // keep file-stored credentials until replaced below
		}
	}
	_ = p.ClearSession()
	c, err := webuntis.New(p, webuntis.Options{Debug: a.debug, NoCache: true,
		Password: func() (string, error) { return credential, nil },
		Secret:   func() (string, error) { return credential, nil }})
	if err != nil {
		return nil, err
	}
	if err := c.Login(ctx); err != nil {
		return nil, err
	}
	ad, err := c.AppData(ctx)
	if err != nil {
		return nil, err
	}
	if p.Student != "" {
		if _, err := c.ResolveStudent(ctx, ""); err != nil { // e.g. switched to another account
			fmt.Fprintf(os.Stderr, "Hinweis: Standard-Schüler %q passt nicht zu diesem Konto und wurde entfernt.\n", p.Student)
			p.Student = ""
		}
	}
	kind, other := secrets.WebUntis, secrets.MobileSecret
	if p.AuthMethod == config.AuthSecret {
		kind, other = other, kind
	}
	if secrets.Where(p, other, a.noKeyring) != secrets.SourceNone { // left over from the other login method
		if err := secrets.Delete(p, other, a.noKeyring); err != nil {
			fmt.Fprintln(os.Stderr, "Warning:", err)
		}
	}
	switch {
	case noStore:
		if err := secrets.Delete(p, kind, a.noKeyring); err != nil {
			fmt.Fprintln(os.Stderr, "Warning:", err)
		}
	default:
		if err := secrets.Set(p, kind, credential, a.noKeyring); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: credentials not stored: %v\n", err)
		}
	}
	if err := p.Save(); err != nil {
		return nil, err
	}
	a.client = c
	if a.profile == "" || a.profile == p.Name {
		_ = config.SetCurrentProfile(p.Name)
	}
	return ad, nil
}

// authLabel describes the login method of a profile.
func authLabel(p *config.Profile) string {
	if p.Method() == config.AuthSecret {
		return "Untis Mobile Schlüssel (Microsoft/SSO)"
	}
	return "Benutzername & Passwort"
}

func (a *app) printLoginSummary(p *config.Profile, ad *webuntis.AppData) error {
	var d render.Doc
	d.H(2, "✅ Angemeldet")
	var kids []string
	for _, s := range ad.User.Students {
		kids = append(kids, render.Esc(s.DisplayName))
	}
	person := ""
	if ad.User.Person != nil {
		person = ad.User.Person.DisplayName
	}
	d.KV("Schule", render.Esc(firstNonEmpty(p.SchoolDisplayName, ad.Tenant.DisplayName, p.School)),
		"Server", p.Server, "Benutzer", "`"+ad.User.Name+"`", "Anmeldeart", authLabel(p), "Person", render.Esc(person),
		"Rollen", strings.Join(ad.User.Roles, ", "), "Schüler", strings.Join(kids, ", "),
		"Standard-Schüler", render.Esc(p.Student),
		"Schuljahr", ad.CurrentSchoolYear.Name, "Profil", p.Name, "Gespeichert in", p.Path())
	return a.renderer().Markdown(d.String())
}

func (a *app) logoutCmd() *cobra.Command {
	var forget bool
	cmd := &cobra.Command{
		Use:   "logout",
		Short: "End the session (and optionally forget the profile)",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.api()
			if err != nil {
				return err
			}
			if err := c.Logout(cmd.Context()); err != nil {
				return err
			}
			a.client = nil
			if forget {
				if err := secrets.DeleteAll(c.Profile, a.noKeyring); err != nil {
					fmt.Fprintln(os.Stderr, "Warning:", err)
				}
				if err := os.RemoveAll(c.Profile.Path()); err != nil {
					return err
				}
				fmt.Fprintf(os.Stderr, "Profile %q removed.\n", c.Profile.Name)
				return nil
			}
			fmt.Fprintln(os.Stderr, "Logged out (credentials kept; use --forget to remove them).")
			return nil
		},
	}
	cmd.Flags().BoolVar(&forget, "forget", false, "also delete stored credentials (keyring), settings and cache of the profile")
	return cmd
}

type statusInfo struct {
	Profile    string             `json:"profile" yaml:"profile"`
	Server     string             `json:"server" yaml:"server"`
	School     string             `json:"school" yaml:"school"`
	Display    string             `json:"schoolDisplayName" yaml:"schoolDisplayName"`
	Username   string             `json:"username" yaml:"username"`
	Roles      []string           `json:"roles" yaml:"roles"`
	Students   []webuntis.Student `json:"students" yaml:"students"`
	Year       string             `json:"schoolYear" yaml:"schoolYear"`
	AuthMethod string             `json:"authMethod" yaml:"authMethod"`
	Password   string             `json:"passwordStorage" yaml:"passwordStorage"` // keyring, file or empty
	Secret     string             `json:"secretStorage,omitempty" yaml:"secretStorage,omitempty"`
	SMTPPass   string             `json:"smtpPasswordStorage,omitempty" yaml:"smtpPasswordStorage,omitempty"`
	SMTP       bool               `json:"smtpConfigured" yaml:"smtpConfigured"`
	ConfigDir  string             `json:"configDir" yaml:"configDir"`
}

func (a *app) statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "status",
		Aliases: []string{"whoami"},
		Short:   "Show login status, school and students",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.api()
			if err != nil {
				return err
			}
			ad, err := c.AppData(cmd.Context())
			if err != nil {
				return err
			}
			students, _ := c.Students(cmd.Context())
			p := c.Profile
			info := statusInfo{Profile: p.Name, Server: p.Server, School: p.School, Display: firstNonEmpty(p.SchoolDisplayName, ad.Tenant.DisplayName),
				Username: ad.User.Name, Roles: ad.User.Roles, Students: students, Year: ad.CurrentSchoolYear.Name,
				AuthMethod: p.Method(), Password: string(secrets.Where(p, secrets.WebUntis, a.noKeyring)),
				SMTP: mailer.Validate(p.SMTP) == nil, ConfigDir: p.Path()}
			if info.AuthMethod == config.AuthSecret {
				info.Secret = string(secrets.Where(p, secrets.MobileSecret, a.noKeyring))
			}
			if info.SMTP && p.SMTP.Username != "" {
				info.SMTPPass = string(secrets.Where(p, secrets.SMTP, a.noKeyring))
			}
			return a.emit(info, func() string {
				var d render.Doc
				d.H(2, "%s", render.Esc(info.Display))
				var s []string
				for _, st := range students {
					s = append(s, fmt.Sprintf("%s (%d)", render.Esc(st.Name), st.ID))
				}
				d.KV("Profil", info.Profile, "Server", info.Server, "Schule", info.School, "Benutzer", "`"+info.Username+"`",
					"Rollen", strings.Join(info.Roles, ", "), "Schüler", strings.Join(s, ", "), "Schuljahr", info.Year,
					"Anmeldeart", authLabel(p),
					"Zugangsdaten gespeichert", storageLabel(map[bool]string{true: info.Secret, false: info.Password}[info.AuthMethod == config.AuthSecret]),
					"SMTP-Passwort", map[bool]string{true: storageLabel(info.SMTPPass), false: ""}[info.SMTPPass != "" || (info.SMTP && p.SMTP.Username != "")],
					"SMTP konfiguriert", render.Check(info.SMTP)+map[bool]string{true: "", false: "nein"}[info.SMTP],
					"Verzeichnis", info.ConfigDir)
				return d.String()
			}, nil)
		},
	}
}

func (a *app) studentsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "students",
		Short: "List the students (children) visible to this account",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.api()
			if err != nil {
				return err
			}
			st, err := c.Students(cmd.Context())
			if err != nil {
				return err
			}
			return a.emit(st, func() string {
				var rows [][]string
				for _, s := range st {
					def := ""
					if c.Profile.Student != "" && (fmt.Sprint(s.ID) == c.Profile.Student || strings.Contains(strings.ToLower(s.Name), strings.ToLower(c.Profile.Student))) {
						def = "✓"
					}
					rows = append(rows, []string{fmt.Sprint(s.ID), render.Esc(s.Name), def})
				}
				var d render.Doc
				d.H(2, "Schüler")
				d.Table([]string{"ID", "Name", "Standard"}, rows)
				d.P("_Auswahl mit `--student <name|id>` oder `webuntis config set student <name|id>`_")
				return d.String()
			}, nil)
		},
	}
}

func (a *app) schoolsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "schools", Short: "Search the public WebUntis school directory"}
	cmd.AddCommand(&cobra.Command{
		Use:     "search QUERY",
		Short:   "Search schools by name, city or login name",
		Example: "  webuntis schools search hüllhorst",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := webuntis.SearchSchools(cmd.Context(), strings.Join(args, " "))
			if err != nil {
				return err
			}
			return a.emit(res, func() string {
				var d render.Doc
				d.H(2, "Schulen (%d)", len(res))
				var rows [][]string
				for _, s := range res {
					rows = append(rows, []string{render.Esc(s.DisplayName), render.Esc(s.Address), s.LoginName, s.Server})
				}
				d.Table([]string{"Name", "Adresse", "Login-Name", "Server"}, rows)
				d.P("_Anmelden: `webuntis login <server> --school <login-name>`_")
				return d.String()
			}, nil)
		},
	})
	return cmd
}

// ---------------------------------------------------------------- config

func (a *app) configCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Show or change profile settings (SMTP, default student, time zone)"}
	cmd.AddCommand(&cobra.Command{
		Use:   "show",
		Short: "Show the profile configuration (secrets masked)",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := a.loadProfile()
			if err != nil {
				return err
			}
			masked := *p
			if masked.Password != "" {
				masked.Password = "********"
			}
			if masked.Secret != "" {
				masked.Secret = "********"
			}
			if masked.SMTP.Password != "" {
				masked.SMTP.Password = "********"
			}
			out := struct {
				config.Profile
				PasswordStorage     string `json:"passwordStorage"`
				SecretStorage       string `json:"secretStorage,omitempty"`
				SMTPPasswordStorage string `json:"smtpPasswordStorage,omitempty"`
			}{masked, string(secrets.Where(p, secrets.WebUntis, a.noKeyring)), string(secrets.Where(p, secrets.MobileSecret, a.noKeyring)), ""}
			if p.SMTP.Username != "" {
				out.SMTPPasswordStorage = string(secrets.Where(p, secrets.SMTP, a.noKeyring))
			}
			if a.format == render.Pretty || a.format == render.Markdown {
				b, _ := json.MarshalIndent(out, "", "  ")
				return a.renderer().Markdown("## Profil `" + p.Name + "`\n\n```json\n" + string(b) + "\n```\n")
			}
			return a.renderer().Data(out)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "set KEY VALUE",
		Short: "Set a profile value: student, timezone",
		Example: `  webuntis config set student Liana
  webuntis config set timezone Europe/Berlin`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := a.loadProfile()
			if err != nil {
				return err
			}
			switch args[0] {
			case "student":
				p.Student = args[1]
			case "timezone", "tz":
				if _, err := time.LoadLocation(args[1]); err != nil {
					return err
				}
				p.Timezone = args[1]
			default:
				return fmt.Errorf("unknown key %q (student, timezone)", args[0])
			}
			return p.Save()
		},
	})
	cmd.AddCommand(a.smtpConfigCmd())
	return cmd
}

func (a *app) smtpConfigCmd() *cobra.Command {
	var host, user, from, security, prefix string
	var port int
	var to []string
	var pwStdin, pwPrompt, clear bool
	cmd := &cobra.Command{
		Use:   "smtp",
		Short: "Configure the SMTP server used by `messages forward` / `news forward`",
		Long: `Configure the SMTP server used by "webuntis messages forward" and
"webuntis news forward".

Without flags on a terminal, an interactive wizard starts (provider presets,
test mail). "webuntis config smtp test" sends a test mail.

Security modes: starttls (default, port 587), tls (implicit TLS, port 465),
opportunistic (STARTTLS if offered), none (plain, port 25).
The SMTP password can also be provided via $WEBUNTIS_SMTP_PASSWORD.`,
		Example: `  webuntis config smtp                 # interactive
  webuntis config smtp test            # send a test mail
  webuntis config smtp --host smtp.example.com --user me@example.com --password-prompt \
      --from me@example.com --to me@example.com
  webuntis config smtp --security tls --port 465`,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := a.loadProfile()
			if err != nil {
				return err
			}
			if clear {
				if err := secrets.Delete(p, secrets.SMTP, a.noKeyring); err != nil {
					return err
				}
				p.SMTP = config.SMTPConfig{}
				return p.Save()
			}
			f := cmd.Flags()
			if !anyChanged(f, "host", "port", "user", "from", "to", "security", "subject-prefix", "password-stdin", "password-prompt") && interactive() {
				return a.runSMTPSetup(cmd.Context(), p)
			}
			if f.Changed("host") {
				p.SMTP.Host = host
			}
			if f.Changed("port") {
				p.SMTP.Port = port
			}
			if f.Changed("user") {
				p.SMTP.Username = user
			}
			if f.Changed("from") {
				p.SMTP.From = from
			}
			if f.Changed("to") {
				p.SMTP.To = to
			}
			if f.Changed("security") {
				p.SMTP.Security = security
			}
			if f.Changed("subject-prefix") {
				p.SMTP.SubjectPrefix = prefix
			}
			var smtpPw string
			switch {
			case pwStdin:
				if smtpPw, err = readStdinSecret("SMTP password: "); err != nil {
					return err
				}
			case pwPrompt:
				if smtpPw, err = promptPassword("SMTP password: "); err != nil {
					return err
				}
			}
			if smtpPw != "" {
				if err := secrets.Set(p, secrets.SMTP, smtpPw, a.noKeyring); err != nil {
					return err
				}
			}
			if err := p.Save(); err != nil {
				return err
			}
			if err := mailer.Validate(p.SMTP); err != nil {
				fmt.Fprintln(os.Stderr, "Saved, but:", err)
				return nil
			}
			fmt.Fprintf(os.Stderr, "SMTP saved: %s → %s via %s\n", p.SMTP.From, strings.Join(p.SMTP.To, ", "), p.SMTP.Host)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&host, "host", "", "SMTP host")
	f.IntVar(&port, "port", 0, "SMTP port (default by security mode)")
	f.StringVar(&user, "user", "", "SMTP username (empty: no auth)")
	f.BoolVar(&pwStdin, "password-stdin", false, "read SMTP password from stdin")
	f.BoolVar(&pwPrompt, "password-prompt", false, "prompt for SMTP password")
	f.StringVar(&from, "from", "", "sender address")
	f.StringSliceVar(&to, "to", nil, "recipient address(es), comma separated")
	f.StringVar(&security, "security", "", "starttls, tls, opportunistic, none")
	f.StringVar(&prefix, "subject-prefix", "", `subject prefix (default "[WebUntis]")`)
	f.BoolVar(&clear, "clear", false, "remove SMTP configuration")
	cmd.AddCommand(&cobra.Command{
		Use:   "test",
		Short: "Send a test mail with the configured SMTP settings",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := a.loadProfile()
			if err != nil {
				return err
			}
			return a.sendTestMail(cmd.Context(), p)
		},
	})
	return cmd
}

func (a *app) profilesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profiles",
		Short: "List profiles (several schools / accounts)",
		RunE: func(cmd *cobra.Command, args []string) error {
			names, err := config.ListProfiles()
			if err != nil {
				return err
			}
			cur := config.CurrentProfileName()
			type row struct {
				Name    string `json:"name" yaml:"name"`
				Current bool   `json:"current" yaml:"current"`
				Server  string `json:"server" yaml:"server"`
				School  string `json:"school" yaml:"school"`
				User    string `json:"username" yaml:"username"`
				Auth    string `json:"authMethod" yaml:"authMethod"`
			}
			var list []row
			for _, n := range names {
				p, err := config.Load(n)
				if err != nil {
					continue
				}
				list = append(list, row{n, n == cur, p.Server, p.School, p.Username, p.Method()})
			}
			return a.emit(list, func() string {
				var rows [][]string
				for _, r := range list {
					auth := "Passwort"
					if r.Auth == config.AuthSecret {
						auth = "Untis Mobile"
					}
					rows = append(rows, []string{r.Name, render.Check(r.Current), r.School, r.Server, render.Esc(r.User), auth})
				}
				var d render.Doc
				d.H(2, "Profile")
				if len(rows) == 0 {
					return d.Empty("Keine Profile. `webuntis login` legt eines an.").String()
				}
				d.Table([]string{"Name", "Aktiv", "Schule", "Server", "Benutzer", "Anmeldung"}, rows)
				d.P("_Wechseln: `webuntis profiles use <name>` · einmalig: `--profile <name>`_")
				return d.String()
			}, nil)
		},
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "use NAME",
		Short: "Switch the active profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := config.Load(args[0]); err != nil {
				return fmt.Errorf("profile %q: %w", args[0], err)
			}
			return config.SetCurrentProfile(args[0])
		},
	})
	return cmd
}

func (a *app) cacheCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "cache", Short: "Manage the local response cache"}
	cmd.AddCommand(&cobra.Command{
		Use:   "clear",
		Short: "Delete all cached responses of the profile",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.api()
			if err != nil {
				return err
			}
			return c.Cache.Clear()
		},
	})
	return cmd
}

// ---------------------------------------------------------------- raw api

func (a *app) apiCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "api",
		Short: "Raw read-only access to the WebUntis APIs (for exploring)",
	}
	var params []string
	get := &cobra.Command{
		Use:   "get PATH",
		Short: "GET an API path with the current session and print the JSON",
		Example: `  webuntis api get /WebUntis/api/rest/view/v1/app/data
  webuntis api get /WebUntis/api/homeworks/lessons -q startDate=20260901 -q endDate=20260930
  webuntis api get rest/view/v1/messages`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.api()
			if err != nil {
				return err
			}
			path := args[0]
			if !strings.HasPrefix(path, "/") && !strings.HasPrefix(path, "http") {
				path = "/WebUntis/api/" + strings.TrimPrefix(path, "api/")
			}
			if strings.HasPrefix(path, "http") && !strings.HasPrefix(path, c.Profile.BaseURL()+"/") {
				return errors.New("only URLs of the configured WebUntis server are allowed")
			}
			q := url.Values{}
			for _, kv := range params {
				k, v, _ := strings.Cut(kv, "=")
				q.Add(k, v)
			}
			b, err := c.Get(cmd.Context(), path, q, 0)
			if err != nil {
				return err
			}
			return printRawJSON(a, b)
		},
	}
	get.Flags().StringArrayVarP(&params, "query", "q", nil, "query parameter key=value (repeatable)")
	cmd.AddCommand(get)
	cmd.AddCommand(&cobra.Command{
		Use:   "rpc METHOD [PARAMS-JSON]",
		Short: "Call a read-only JSON-RPC method (get*) of the public WebUntis API",
		Example: `  webuntis api rpc getSubjects
  webuntis api rpc getTimetable '{"options":{"element":{"id":8685,"type":5},"startDate":20260921,"endDate":20260925}}'`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !strings.HasPrefix(args[0], "get") {
				return errors.New("only read-only get* methods are allowed")
			}
			c, err := a.api()
			if err != nil {
				return err
			}
			var params any = map[string]any{}
			if len(args) == 2 {
				if err := json.Unmarshal([]byte(args[1]), &params); err != nil {
					return fmt.Errorf("params: %w", err)
				}
			}
			var out json.RawMessage
			if err := c.RPC(cmd.Context(), args[0], params, &out); err != nil {
				return err
			}
			return printRawJSON(a, out)
		},
	})
	return cmd
}

func printRawJSON(a *app, b []byte) error {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		_, err := os.Stdout.Write(b)
		return err
	}
	r := a.renderer()
	if a.format == render.YAML {
		return r.Data(v)
	}
	r.Format = render.JSON
	return r.Data(v)
}

func anyChanged(f *pflag.FlagSet, names ...string) bool {
	return slices.ContainsFunc(names, f.Changed)
}

func storageLabel(src string) string {
	switch secrets.Source(src) {
	case secrets.SourceKeyring:
		return "✓ Schlüsselbund"
	case secrets.SourceFile:
		return "✓ config.json"
	}
	return "nein"
}
