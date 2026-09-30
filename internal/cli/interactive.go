package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/dgrieser/web-untis-cli/internal/config"
	"github.com/dgrieser/web-untis-cli/internal/mailer"
	"github.com/dgrieser/web-untis-cli/internal/webuntis"
)

const newProfileChoice = "\x00new"

// interactive reports whether we can show interactive forms.
func interactive() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stderr.Fd()))
}

func (a *app) setupCmd() *cobra.Command {
	var noStore bool
	cmd := &cobra.Command{
		Use:     "setup",
		Aliases: []string{"init"},
		Short:   "Interactive setup: profile, school search, login, default student",
		Long: `Interactive setup wizard:

  1. choose an existing profile or create a new one
  2. search the school (name, city, login name or URL) with live results
  3. choose the login method: username & password, or the Untis Mobile
     key (accounts that sign in via Microsoft/SSO); optionally not stored
  4. choose the default student (parent accounts with several children)

Same as running "webuntis login" without arguments on a terminal.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !interactive() {
				return errors.New("setup needs a terminal; use `webuntis login <school> --user … --password-stdin` (Microsoft/SSO accounts: --secret-stdin) in scripts")
			}
			return a.runSetup(cmd.Context(), noStore)
		},
	}
	cmd.Flags().BoolVar(&noStore, "no-store-password", false, "do not store the password / Untis Mobile key (neither keyring nor file)")
	return cmd
}

// schoolSearcher caches school directory lookups while the user types.
type schoolSearcher struct {
	mu    sync.Mutex
	cache map[string][]huh.Option[webuntis.School]
}

func schoolLabel(s webuntis.School) string {
	label := s.DisplayName
	if s.Address != "" {
		label += " · " + s.Address
	}
	return label + "  (" + s.LoginName + ")"
}

func (ss *schoolSearcher) options(ctx context.Context, query string, current webuntis.School) []huh.Option[webuntis.School] {
	q := strings.TrimSpace(query)
	hint := func(msg string) []huh.Option[webuntis.School] {
		opts := []huh.Option[webuntis.School]{}
		if current.LoginName != "" {
			opts = append(opts, huh.NewOption(schoolLabel(current), current))
		}
		return append(opts, huh.NewOption(msg, webuntis.School{}))
	}
	if len([]rune(q)) < 3 {
		return hint("… mindestens 3 Zeichen eingeben")
	}
	ss.mu.Lock()
	if o, ok := ss.cache[q]; ok {
		ss.mu.Unlock()
		return o
	}
	ss.mu.Unlock()

	var opts []huh.Option[webuntis.School]
	if strings.Contains(q, "://") || strings.Contains(q, ".") {
		server, login, tenant, display, err := resolveSchool(ctx, q, "")
		if err != nil {
			return hint("⚠ " + err.Error())
		}
		s := webuntis.School{Server: server, LoginName: login, TenantID: tenant, DisplayName: firstNonEmpty(display, login)}
		opts = append(opts, huh.NewOption(schoolLabel(s), s))
	} else {
		res, err := webuntis.SearchSchools(ctx, q)
		if err != nil {
			return hint("⚠ " + err.Error())
		}
		if len(res) == 0 {
			return hint("… keine Schule gefunden")
		}
		for _, s := range res {
			opts = append(opts, huh.NewOption(schoolLabel(s), s))
		}
	}
	ss.mu.Lock()
	ss.cache[q] = opts
	ss.mu.Unlock()
	return opts
}

func notEmpty(what string) func(string) error {
	return func(s string) error {
		if strings.TrimSpace(s) == "" {
			return fmt.Errorf("%s fehlt", what)
		}
		return nil
	}
}

func (a *app) runSetup(ctx context.Context, noStore bool) error {
	theme := formTheme()
	profiles, _ := config.ListProfiles()

	// ---- 1. profile
	choice := a.profile
	newName := ""
	if choice == "" {
		choice = config.CurrentProfileName()
		if !slices.Contains(profiles, choice) {
			choice = newProfileChoice
		}
		if len(profiles) == 0 {
			newName = config.DefaultProfile
		}
	}
	if a.profile == "" && len(profiles) > 0 {
		var opts []huh.Option[string]
		for _, n := range profiles {
			label := n
			if p, err := config.Load(n); err == nil {
				label = fmt.Sprintf("%s — %s, %s", n, firstNonEmpty(p.SchoolDisplayName, p.School), p.Username)
				if p.Method() == config.AuthSecret {
					label += " · Untis Mobile"
				}
			}
			opts = append(opts, huh.NewOption(label, n))
		}
		opts = append(opts, huh.NewOption("➕ Neue Anmeldung hinzufügen", newProfileChoice))
		err := huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().Title("Anmeldung wählen").
					Description("Gespeicherte Anmeldung aktualisieren oder eine weitere hinzufügen").
					Options(opts...).Value(&choice),
			),
			huh.NewGroup(
				huh.NewInput().Title("Kurzname für die neue Anmeldung").Description("Später nutzbar mit --profile <name>").Placeholder("z.B. liana, schule2").Value(&newName).
					Validate(func(s string) error {
						if err := config.ValidateProfileName(s); err != nil {
							return err
						}
						if slices.Contains(profiles, s) {
							return fmt.Errorf("profil %q existiert bereits", s)
						}
						return nil
					}),
			).WithHideFunc(func() bool { return choice != newProfileChoice }),
		).WithTheme(theme).RunWithContext(ctx)
		if err != nil {
			return abortErr(err)
		}
	}
	name := choice
	if choice == newProfileChoice {
		name = newName
	}
	existing, _ := config.Load(name)

	// ---- 2. school + credentials
	var school webuntis.School
	query, user, method := "", "", config.AuthPassword
	if existing != nil {
		school = webuntis.School{Server: existing.Server, LoginName: existing.School, TenantID: existing.TenantID,
			DisplayName: firstNonEmpty(existing.SchoolDisplayName, existing.School)}
		query, user, method = existing.School, existing.Username, existing.Method()
	}
	storePw := !noStore
	searcher := &schoolSearcher{cache: map[string][]huh.Option[webuntis.School]{}}
	password, key := "", ""

	for attempt := 1; ; attempt++ {
		groups := []*huh.Group{
			huh.NewGroup(
				huh.NewInput().Title("Schule suchen").
					Description("Name, Ort, Login-Name oder URL (z.B. https://ge-huellhorst.webuntis.com)").
					Placeholder("hüllhorst").Value(&query).Validate(notEmpty("Suchbegriff")),
				huh.NewSelect[webuntis.School]().Title("Schule").
					OptionsFunc(func() []huh.Option[webuntis.School] { return searcher.options(ctx, query, school) }, &query).
					Value(&school).Height(8).
					Validate(func(s webuntis.School) error {
						if s.LoginName == "" {
							return errors.New("bitte eine Schule auswählen")
						}
						return nil
					}),
			),
		}
		groups = append(groups, credentialGroups(&school, &method, &user, &password, &key, &storePw, a.noKeyring)...)
		if attempt > 1 {
			groups = groups[1:] // school already chosen, only retry credentials
		}
		if err := huh.NewForm(groups...).WithTheme(theme).RunWithContext(ctx); err != nil {
			return abortErr(err)
		}
		p, credential := profileFromWizard(name, school, method, user, password, key)
		fmt.Fprintf(os.Stderr, "Anmelden bei %s …\n", firstNonEmpty(school.DisplayName, school.LoginName))
		ad, err := a.performLogin(ctx, p, existing, credential, !storePw)
		if err != nil {
			fmt.Fprintln(os.Stderr, "✗", err)
			retry := true
			if ferr := huh.NewForm(huh.NewGroup(huh.NewConfirm().Title("Anmeldung fehlgeschlagen – nochmal versuchen?").
				Affirmative("Ja").Negative("Abbrechen").Value(&retry))).WithTheme(theme).RunWithContext(ctx); ferr != nil || !retry {
				return err
			}
			password, key = "", ""
			continue
		}

		// ---- 3. default student
		if len(ad.User.Students) > 1 {
			sel := p.Student
			var opts []huh.Option[string]
			for _, s := range ad.User.Students {
				opts = append(opts, huh.NewOption(s.DisplayName, fmt.Sprint(s.ID)))
			}
			if err := huh.NewForm(huh.NewGroup(huh.NewSelect[string]().Title("Standard-Schüler").
				Description("Andere jederzeit mit --student <name>").Options(opts...).Value(&sel))).
				WithTheme(theme).RunWithContext(ctx); err == nil {
				for _, s := range ad.User.Students {
					if fmt.Sprint(s.ID) == sel {
						p.Student = s.DisplayName
					}
				}
				if err := p.Save(); err != nil {
					return err
				}
			}
		}
		if err := a.printLoginSummary(p, ad); err != nil {
			return err
		}

		// ---- 4. optional: e-mail forwarding
		if mailer.Validate(p.SMTP) != nil {
			smtp := false
			if err := huh.NewForm(huh.NewGroup(huh.NewConfirm().Title("E-Mail-Weiterleitung einrichten?").
				Description("Mitteilungen und Nachrichten per SMTP an eine E-Mail-Adresse senden (später: webuntis config smtp)").
				Affirmative("Ja").Negative("Später").Value(&smtp))).WithTheme(theme).RunWithContext(ctx); err == nil && smtp {
				return a.runSMTPSetup(ctx, p)
			}
		}
		return nil
	}
}

// credentialGroups builds the wizard groups for the login method: the
// method select with the username, the password (password method) or the
// Untis Mobile key (secret method), and the "store credentials" confirmation.
func credentialGroups(school *webuntis.School, method, user, password, key *string, store *bool, noKeyring bool) []*huh.Group {
	where := "im Schlüsselbund des Systems"
	if noKeyring {
		where = "in config.json (Dateimodus 0600)"
	}
	return []*huh.Group{
		huh.NewGroup(
			huh.NewSelect[string]().Title("Anmeldeart").
				Description("Konten mit Microsoft/Office-365-Anmeldung (meist Schüler) haben kein WebUntis-Passwort; sie nutzen den Untis Mobile Schlüssel.").
				Options(
					huh.NewOption("Benutzername & Passwort", config.AuthPassword),
					huh.NewOption("Microsoft / SSO (Untis Mobile Schlüssel)", config.AuthSecret),
				).Value(method),
			huh.NewInput().TitleFunc(func() string { return "Benutzername bei " + firstNonEmpty(school.DisplayName, school.LoginName) }, school).
				Value(user).Validate(notEmpty("Benutzername")),
		),
		huh.NewGroup(
			huh.NewInput().Title("Passwort").EchoMode(huh.EchoModePassword).Value(password).Validate(notEmpty("Passwort")),
		).WithHideFunc(func() bool { return *method != config.AuthPassword }),
		huh.NewGroup(
			huh.NewInput().Title("Untis Mobile Schlüssel").
				Description("In WebUntis: Profil → Freigaben → „Zugriff über Untis Mobile“ → Anzeigen. Den neben dem QR-Code angezeigten Schlüssel einfügen.").
				EchoMode(huh.EchoModePassword).Value(key).Validate(validateSecret),
		).WithHideFunc(func() bool { return *method != config.AuthSecret }),
		huh.NewGroup(
			huh.NewConfirm().Title("Zugangsdaten speichern?").
				Description("Erlaubt automatische Neuanmeldung; Passwort bzw. Schlüssel wird " + where + " gespeichert.").
				Affirmative("Ja").Negative("Nein").Value(store),
		),
	}
}

func validateSecret(s string) error {
	if _, err := webuntis.ParseSecret(s); err != nil {
		return errors.New("ungültiger Schlüssel (nur Buchstaben A–Z und Ziffern 2–7)")
	}
	return nil
}

// profileFromWizard builds the profile from the wizard inputs and returns it
// with the credential to log in with (password or normalized Untis Mobile key).
func profileFromWizard(name string, school webuntis.School, method, user, password, key string) (*config.Profile, string) {
	p := &config.Profile{Name: name, Server: school.Server, School: school.LoginName, TenantID: school.TenantID,
		SchoolDisplayName: school.DisplayName, Username: strings.TrimSpace(user), AuthMethod: method}
	if method != config.AuthSecret {
		return p, password
	}
	k, _ := webuntis.ParseSecret(key) // validated by the form
	return p, k
}

// formTheme returns the theme for interactive forms. The default uses only
// the terminal's own 16 ANSI colors (so it follows the user's color scheme)
// and avoids "bright black"/faint text, which is unreadable on many dark
// themes. Override with $WEBUNTIS_FORM_THEME: ansi (default), charm,
// dracula, catppuccin, base16, plain.
func formTheme() *huh.Theme {
	switch strings.ToLower(os.Getenv("WEBUNTIS_FORM_THEME")) {
	case "charm":
		return huh.ThemeCharm()
	case "dracula":
		return huh.ThemeDracula()
	case "catppuccin":
		return huh.ThemeCatppuccin()
	case "base16":
		return huh.ThemeBase16()
	case "plain", "base":
		return huh.ThemeBase()
	}
	var (
		text     = lipgloss.Color("7")  // normal foreground
		bright   = lipgloss.Color("15") // bright white
		accent   = lipgloss.Color("6")  // cyan
		selected = lipgloss.Color("2")  // green
		pointer  = lipgloss.Color("3")  // yellow
		errc     = lipgloss.Color("9")  // bright red
	)
	t := huh.ThemeBase16()
	f := &t.Focused
	f.Base = f.Base.BorderForeground(accent)
	f.Card = f.Base
	f.Title = lipgloss.NewStyle().Foreground(accent).Bold(true)
	f.NoteTitle = f.Title
	f.Description = lipgloss.NewStyle().Foreground(text)
	f.ErrorIndicator = f.ErrorIndicator.Foreground(errc)
	f.ErrorMessage = f.ErrorMessage.Foreground(errc)
	f.SelectSelector = f.SelectSelector.Foreground(pointer).Bold(true)
	f.NextIndicator = f.NextIndicator.Foreground(pointer)
	f.PrevIndicator = f.PrevIndicator.Foreground(pointer)
	f.Option = f.Option.Foreground(text)
	f.UnselectedOption = f.UnselectedOption.Foreground(text)
	f.SelectedOption = f.SelectedOption.Foreground(selected).Bold(true)
	f.SelectedPrefix = f.SelectedPrefix.Foreground(selected)
	f.FocusedButton = lipgloss.NewStyle().Padding(0, 2).MarginRight(1).Foreground(lipgloss.Color("0")).Background(accent).Bold(true)
	f.BlurredButton = lipgloss.NewStyle().Padding(0, 2).MarginRight(1).Foreground(text).Background(lipgloss.Color("0"))
	f.TextInput.Cursor = f.TextInput.Cursor.Foreground(pointer)
	f.TextInput.Placeholder = lipgloss.NewStyle().Foreground(text).Italic(true)
	f.TextInput.Prompt = f.TextInput.Prompt.Foreground(pointer)
	f.TextInput.Text = lipgloss.NewStyle().Foreground(bright)

	t.Blurred = t.Focused
	t.Blurred.Base = t.Blurred.Base.BorderStyle(lipgloss.HiddenBorder())
	t.Blurred.Card = t.Blurred.Base
	t.Blurred.Title = lipgloss.NewStyle().Foreground(text).Bold(true)
	t.Blurred.NoteTitle = t.Blurred.Title
	t.Blurred.TextInput.Prompt = t.Blurred.TextInput.Prompt.Foreground(text)
	t.Blurred.TextInput.Text = lipgloss.NewStyle().Foreground(text)
	t.Blurred.NextIndicator = lipgloss.NewStyle()
	t.Blurred.PrevIndicator = lipgloss.NewStyle()

	t.Group.Title = t.Focused.Title
	t.Group.Description = t.Focused.Description

	t.Help.ShortKey = lipgloss.NewStyle().Foreground(pointer)
	t.Help.ShortDesc = lipgloss.NewStyle().Foreground(text)
	t.Help.ShortSeparator = lipgloss.NewStyle().Foreground(text)
	t.Help.FullKey = t.Help.ShortKey
	t.Help.FullDesc = t.Help.ShortDesc
	t.Help.FullSeparator = t.Help.ShortSeparator
	t.Help.Ellipsis = t.Help.ShortSeparator
	return t
}

func abortErr(err error) error {
	if errors.Is(err, huh.ErrUserAborted) {
		return errors.New("setup aborted")
	}
	return err
}
