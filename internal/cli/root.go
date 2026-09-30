// Package cli implements the webuntis command line interface.
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"time"

	ics "github.com/arran4/golang-ical"
	"github.com/spf13/cobra"

	"github.com/dgrieser/web-untis-cli/internal/config"
	"github.com/dgrieser/web-untis-cli/internal/dates"
	"github.com/dgrieser/web-untis-cli/internal/render"
	"github.com/dgrieser/web-untis-cli/internal/secrets"
	"github.com/dgrieser/web-untis-cli/internal/webuntis"
)

// Version is set at build time via -ldflags.
var Version = "dev"

type app struct {
	output    string
	profile   string
	student   string
	refresh   bool
	debug     bool
	noKeyring bool
	style     string

	format render.Format
	client *webuntis.Client
}

// Execute runs the CLI.
func Execute() int {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	a := &app{}
	root := a.rootCmd()
	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		if errors.Is(err, config.ErrNotLoggedIn) || errors.Is(err, webuntis.ErrAuth) {
			return 2
		}
		return 1
	}
	return 0
}

func (a *app) rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "webuntis",
		Short: "Read-only WebUntis in your terminal",
		Long: `webuntis is a read-only command line client for WebUntis (webuntis.com).

It speaks the same (undocumented) JSON APIs as the WebUntis web UI:
news of the day, messages (inbox/sent/drafts, SMTP forwarding),
timetables, absences, homework, class register entries, class services,
exams, exemptions, contact hours and addins such as Klassengeld.

Get started:
  webuntis login https://ge-huellhorst.webuntis.com
  webuntis today
  webuntis timetable
  webuntis messages`,
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			f, err := render.ParseFormat(a.output)
			if err != nil {
				return err
			}
			// Pretty output is for humans; when piped, emit plain Markdown
			// unless a style was requested explicitly.
			if f == render.Pretty && !render.IsTTY() && a.style == "" && !cmd.Flags().Changed("output") {
				f = render.Markdown
			}
			a.format = f
			return nil
		},
		PersistentPostRun: func(cmd *cobra.Command, args []string) {
			if a.client != nil {
				_ = a.client.SaveSession()
			}
		},
	}
	pf := root.PersistentFlags()
	pf.StringVarP(&a.output, "output", "o", envOr("WEBUNTIS_OUTPUT", "pretty"), "output format: pretty, markdown, json, yaml, ics (where supported)")
	pf.StringVarP(&a.profile, "profile", "p", "", "profile to use (default: the current profile, see: webuntis profiles)")
	pf.StringVarP(&a.student, "student", "s", "", "student name or id (for parent accounts with several children)")
	pf.BoolVarP(&a.refresh, "refresh", "r", false, "bypass the local cache and fetch fresh data")
	pf.BoolVar(&a.debug, "debug", false, "print HTTP requests to stderr")
	pf.BoolVar(&a.noKeyring, "no-keyring", os.Getenv("WEBUNTIS_NO_KEYRING") != "", "do not use the system keyring; store passwords / the Untis Mobile key in the profile's config.json (mode 0600)")
	pf.StringVar(&a.style, "style", os.Getenv("WEBUNTIS_STYLE"), "markdown style: auto, dark, light, notty, dracula, tokyo-night, pink, ascii or a glamour JSON file")

	root.AddGroup(
		&cobra.Group{ID: "main", Title: "WebUntis:"},
		&cobra.Group{ID: "student", Title: "Student data:"},
		&cobra.Group{ID: "setup", Title: "Setup:"},
	)
	add := func(group string, cmds ...*cobra.Command) {
		for _, c := range cmds {
			c.GroupID = group
			root.AddCommand(c)
		}
	}
	add("main", a.todayCmd(), a.newsCmd(), a.messagesCmd(), a.timetableCmd(), a.addinsCmd(), a.klassengeldCmd())
	add("student", a.absencesCmd(), a.absenceTimesCmd(), a.homeworkCmd(), a.classRegCmd(), a.classServicesCmd(),
		a.examsCmd(), a.exemptionsCmd(), a.contactHoursCmd(), a.studentsCmd())
	add("setup", a.setupCmd(), a.loginCmd(), a.logoutCmd(), a.statusCmd(), a.schoolsCmd(), a.configCmd(), a.profilesCmd(), a.cacheCmd(), a.apiCmd())
	root.SetHelpCommandGroupID("setup")
	root.SetCompletionCommandGroupID("setup")
	a.registerCompletions(root)
	return root
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func (a *app) profileName() string {
	if a.profile != "" {
		return a.profile
	}
	return config.CurrentProfileName()
}

// loadProfile loads the active profile.
func (a *app) loadProfile() (*config.Profile, error) {
	return config.Load(a.profileName())
}

// api returns a logged-in client for the active profile.
func (a *app) api() (*webuntis.Client, error) {
	if a.client != nil {
		return a.client, nil
	}
	p, err := a.loadProfile()
	if err != nil {
		return nil, err
	}
	c, err := webuntis.New(p, webuntis.Options{NoCache: a.refresh, Debug: a.debug, Password: a.secretFunc(p, secrets.WebUntis), Secret: a.secretFunc(p, secrets.MobileSecret)})
	if err != nil {
		return nil, err
	}
	a.client = c
	return c, nil
}

func (a *app) renderer() *render.Renderer {
	return render.New(a.format, a.style)
}

// emit writes data in the selected format. md renders Markdown (pretty /
// markdown), cal (optional) produces an iCalendar document.
func (a *app) emit(data any, md func() string, cal func() *ics.Calendar) error {
	r := a.renderer()
	switch a.format {
	case render.JSON, render.YAML:
		return r.Data(data)
	case render.ICS:
		if cal == nil {
			return errors.New("ics output is not supported for this command (supported: timetable, exams, homework, absences)")
		}
		return r.Calendar(cal())
	default:
		return r.Markdown(md())
	}
}

// student resolves the selected student.
func (a *app) resolveStudent(ctx context.Context) (*webuntis.Client, webuntis.Student, error) {
	c, err := a.api()
	if err != nil {
		return nil, webuntis.Student{}, err
	}
	s, err := c.ResolveStudent(ctx, a.student)
	return c, s, err
}

// dateRange handles --from/--to flags with defaults.
type dateRange struct {
	from, to string
}

func (d *dateRange) register(cmd *cobra.Command, fromHelp, toHelp string) {
	cmd.Flags().StringVarP(&d.from, "from", "f", "", fromHelp)
	cmd.Flags().StringVarP(&d.to, "to", "t", "", toHelp)
}

// resolve parses the flags; defaults are used for empty values.
func (d *dateRange) resolve(defFrom, defTo time.Time) (time.Time, time.Time, error) {
	from, to := defFrom, defTo
	var err error
	if d.from != "" {
		if from, err = dates.Parse(d.from); err != nil {
			return from, to, err
		}
	}
	if d.to != "" {
		if to, err = dates.Parse(d.to); err != nil {
			return from, to, err
		}
	}
	if to.Before(from) {
		return from, to, fmt.Errorf("--to (%s) is before --from (%s)", dates.ISO(to), dates.ISO(from))
	}
	return from, to, nil
}

// schoolYearRange returns start/end of the current school year.
func (a *app) schoolYearRange(ctx context.Context, c *webuntis.Client) (time.Time, time.Time) {
	y, err := c.SchoolYearFor(ctx, dates.Today())
	if err != nil || y.DateRange.Start == "" {
		t := dates.Today()
		return t.AddDate(0, -6, 0), t.AddDate(0, 6, 0)
	}
	return dates.Day(y.DateRange.StartTime()), dates.Day(y.DateRange.EndTime())
}

// secretFunc looks up the WebUntis password or Untis Mobile secret lazily
// (only when a login is needed), so normal runs with a valid session never
// touch the keyring.
func (a *app) secretFunc(p *config.Profile, k secrets.Kind) func() (string, error) {
	return func() (string, error) {
		pw, src, err := secrets.Get(p, k, a.noKeyring)
		if errors.Is(err, secrets.ErrNotFound) {
			return "", webuntis.ErrNoPassword
		}
		if err == nil && a.debug {
			fmt.Fprintf(os.Stderr, "[debug] using WebUntis %s from %s\n", map[bool]string{true: "secret", false: "password"}[k == secrets.MobileSecret], src)
		}
		return pw, err
	}
}

// smtpConfig returns the SMTP settings with the password resolved.
func (a *app) smtpConfig(p *config.Profile) (config.SMTPConfig, error) {
	cfg := p.SMTP
	pw, _, err := secrets.Get(p, secrets.SMTP, a.noKeyring)
	switch {
	case errors.Is(err, secrets.ErrNotFound):
		if cfg.Username != "" {
			return cfg, fmt.Errorf("no SMTP password stored for %s (run `webuntis config smtp`)", cfg.Username)
		}
	case err != nil:
		return cfg, err
	}
	cfg.Password = pw
	return cfg, nil
}
