package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dgrieser/web-untis-cli/internal/config"
	"github.com/dgrieser/web-untis-cli/internal/dates"
	"github.com/dgrieser/web-untis-cli/internal/webuntis"
)

type completeFunc = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective)

const noFiles = cobra.ShellCompDirectiveNoFileComp

func completeCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
}

// completeSchools searches the public school directory.
func completeSchools(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 || len([]rune(toComplete)) < 3 || strings.Contains(toComplete, "/") {
		return nil, noFiles
	}
	ctx, cancel := completeCtx()
	defer cancel()
	res, err := webuntis.SearchSchools(ctx, toComplete)
	if err != nil {
		return nil, noFiles
	}
	var out []string
	for _, s := range res {
		desc := s.DisplayName
		if s.Address != "" {
			desc += ", " + s.Address
		}
		// Complete to the server when it is school specific, otherwise to
		// the login name (usable with --school).
		out = append(out, s.LoginName+"\t"+desc+" ("+s.Server+")")
	}
	return out, noFiles
}

func completeProfiles(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	names, _ := config.ListProfiles()
	var out []string
	for _, n := range names {
		desc := ""
		if p, err := config.Load(n); err == nil {
			desc = firstNonEmpty(p.SchoolDisplayName, p.School) + ", " + p.Username
		}
		out = append(out, n+"\t"+desc)
	}
	return out, noFiles
}

func staticCompletion(values ...string) completeFunc {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return values, noFiles
	}
}

// withClient builds a client from flags already parsed for completion.
func (a *app) withClient(fn func(ctx context.Context, c *webuntis.Client) []string) completeFunc {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		c, err := a.api()
		if err != nil {
			return nil, noFiles
		}
		ctx, cancel := completeCtx()
		defer cancel()
		out := fn(ctx, c)
		_ = c.SaveSession()
		return out, noFiles
	}
}

func (a *app) registerCompletions(root *cobra.Command) {
	pf := root.PersistentFlags()
	_ = root.RegisterFlagCompletionFunc("profile", completeProfiles)
	_ = root.RegisterFlagCompletionFunc("output", staticCompletion("pretty", "markdown", "json", "yaml", "ics", "html", "pdf"))
	_ = root.RegisterFlagCompletionFunc("style", staticCompletion("auto", "dark", "light", "notty", "dracula", "tokyo-night", "pink", "ascii"))
	_ = pf
	_ = root.RegisterFlagCompletionFunc("student", a.withClient(func(ctx context.Context, c *webuntis.Client) []string {
		st, err := c.Students(ctx)
		if err != nil {
			return nil
		}
		var out []string
		for _, s := range st {
			first := s.Name
			if f := strings.Fields(s.Name); len(f) > 1 {
				first = f[len(f)-1] // "Grieser Liana" -> "Liana"
			}
			out = append(out, first+"\t"+fmt.Sprintf("%s (%d)", s.Name, s.ID))
		}
		return out
	}))

	walk(root, func(c *cobra.Command) {
		switch c.CommandPath() {
		case "webuntis login":
			c.ValidArgsFunction = completeSchools
			_ = c.RegisterFlagCompletionFunc("school", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
				return completeSchools(cmd, nil, toComplete)
			})
		case "webuntis schools search":
			c.ValidArgsFunction = completeSchools
		case "webuntis profiles use":
			c.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
				if len(args) > 0 {
					return nil, noFiles
				}
				return completeProfiles(cmd, args, toComplete)
			}
		case "webuntis config set":
			c.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
				if len(args) == 0 {
					return []string{"student\tdefault student", "timezone\tIANA time zone for ics output"}, noFiles
				}
				if len(args) == 1 && args[0] == "timezone" {
					return []string{"Europe/Berlin", "Europe/Vienna", "Europe/Zurich", "Europe/Rome", "Europe/Amsterdam"}, noFiles
				}
				return nil, noFiles
			}
		case "webuntis addins open":
			c.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
				if len(args) > 0 {
					return nil, noFiles
				}
				return a.withClient(func(ctx context.Context, cl *webuntis.Client) []string {
					apps, err := cl.PlatformApps(ctx)
					if err != nil {
						return nil
					}
					var out []string
					for _, ap := range apps {
						out = append(out, ap.Name+"\t"+ap.Kind)
					}
					return out
				})(cmd, args, toComplete)
			}
		case "webuntis timetable":
			_ = c.RegisterFlagCompletionFunc("class", a.withClient(func(ctx context.Context, cl *webuntis.Client) []string {
				mon := dates.Monday(dates.Today())
				f, err := cl.TimetableFilter(ctx, "CLASS", "STANDARD", mon, mon.AddDate(0, 0, 4))
				if err != nil {
					return nil
				}
				var out []string
				for _, k := range f.Classes {
					out = append(out, k.Class.ShortName+"\t"+k.Class.LongName)
				}
				return out
			}))
			_ = c.RegisterFlagCompletionFunc("resource-type", staticCompletion("CLASS", "TEACHER", "ROOM", "SUBJECT", "STUDENT"))
		case "webuntis contact-hours":
			_ = c.RegisterFlagCompletionFunc("class", a.withClient(func(ctx context.Context, cl *webuntis.Client) []string {
				cls, err := cl.OfficeHourClasses(ctx)
				if err != nil {
					return nil
				}
				var out []string
				for _, k := range cls {
					out = append(out, k.Label)
				}
				return out
			}))
		case "webuntis config smtp":
			_ = c.RegisterFlagCompletionFunc("security", staticCompletion("starttls", "tls", "opportunistic", "none"))
		}
		if f := c.Flags().Lookup("folder"); f != nil {
			_ = c.RegisterFlagCompletionFunc("folder", staticCompletion("inbox", "sent", "drafts", "all"))
		}
	})
}

func walk(c *cobra.Command, fn func(*cobra.Command)) {
	fn(c)
	for _, sub := range c.Commands() {
		walk(sub, fn)
	}
}
