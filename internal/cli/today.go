package cli

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	ics "github.com/arran4/golang-ical"
	"github.com/spf13/cobra"

	"github.com/dgrieser/web-untis-cli/internal/dates"
	"github.com/dgrieser/web-untis-cli/internal/render"
	"github.com/dgrieser/web-untis-cli/internal/views"
	"github.com/dgrieser/web-untis-cli/internal/webuntis"
)

func (a *app) todayCmd() *cobra.Command {
	var date string
	var full, agenda, onlyNew, peek, withHTML bool
	cmd := &cobra.Command{
		Use:     "today",
		Aliases: []string{"heute", "news-list"},
		Short:   "News of the day (Heute → Nachrichten) and status",
		Long: `Shows the "Heute" page: system message, messages of the day incl.
attachments, last login, last timetable import and unread counters.

--agenda additionally shows today's lessons, homework due and upcoming exams.

News items not shown before are marked with 🆕 (tracked in
<profile>/news-seen.json); --new shows only those, --peek does not mark
anything as seen.`,
		Example: `  webuntis today
  webuntis today --new
  webuntis today --full
  webuntis today --agenda
  webuntis today --date 2026-10-07 -o json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, err := a.api()
			if err != nil {
				return err
			}
			day, err := dates.Parse(date)
			if err != nil {
				return err
			}
			news, err := c.News(ctx, day)
			if err != nil {
				return err
			}
			if news.MessagesOfDay, err = a.applySeen(c, news.MessagesOfDay, onlyNew, peek, withHTML); err != nil {
				return err
			}
			t := views.TodayData{Date: day, News: news, Agenda: agenda, OnlyNew: onlyNew}
			if ad, err := c.AppData(ctx); err == nil {
				t.School = firstNonEmpty(c.Profile.SchoolDisplayName, strings.Join(strings.Fields(ad.Tenant.DisplayName), " "))
				t.User = ad.User.Name
				t.LastLogin = ad.User.LastLogin
			}
			if it, err := c.LatestImportTime(ctx); err == nil && !it.IsZero() {
				t.LastImport = &it
			}
			t.Unread, _ = c.UnreadCounts(ctx)
			t.Cards, _ = c.DashboardCards(ctx)
			if agenda {
				if st, err := c.ResolveStudent(ctx, a.student); err == nil {
					t.Student = &st
					if tt, err := c.Timetable(ctx, webuntis.TimetableQuery{ResourceType: "STUDENT", ResourceID: st.ID, TimetableType: "MY_TIMETABLE", Start: day, End: day}); err == nil {
						for _, d := range tt.Days {
							t.Lessons = append(t.Lessons, d.Lessons...)
						}
					}
					if hw, err := c.Homework(ctx, st.ID, day.AddDate(0, 0, -21), day.AddDate(0, 0, 1)); err == nil {
						for _, h := range hw {
							if !h.DueDate.Before(day) && !h.DueDate.After(day.AddDate(0, 0, 1)) {
								t.HomeworkDue = append(t.HomeworkDue, h)
							}
						}
					}
					if ex, err := c.Exams(ctx, st.ID, day, day.AddDate(0, 0, 14)); err == nil {
						t.UpcomingExams = ex
					}
				}
			}
			return a.emit(t, func() string { return views.Today(t, full) }, nil)
		},
	}
	cmd.Flags().StringVarP(&date, "date", "d", "", "day to show (default today)")
	cmd.Flags().BoolVar(&full, "full", false, "show full text of all messages")
	cmd.Flags().BoolVarP(&agenda, "agenda", "a", false, "also show today's lessons, homework due and upcoming exams")
	cmd.Flags().BoolVar(&onlyNew, "new", false, "only news not shown before")
	cmd.Flags().BoolVar(&peek, "peek", false, "do not mark shown news as seen")
	cmd.Flags().BoolVar(&withHTML, "html", false, "include the original HTML body in json/yaml output")
	return cmd
}

func (a *app) newsCmd() *cobra.Command {
	var date string
	var onlyNew, peek, withHTML bool
	cmd := &cobra.Command{
		Use:     "news [NUMBER|ID]",
		Aliases: []string{"nachrichten"},
		Short:   "Messages of the day in full; `news forward` sends new ones via SMTP",
		Long: `Shows the messages of the day ("Heute → Nachrichten") in full.

Items not shown before are marked with 🆕 (tracked in <profile>/news-seen.json);
--new shows only those, --peek does not mark anything as seen.`,
		Example: `  webuntis news        # all messages of the day, full text
  webuntis news --new  # only news you have not seen yet
  webuntis news 1      # first message
  webuntis news 371    # by id
  webuntis news forward`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.api()
			if err != nil {
				return err
			}
			day, err := dates.Parse(date)
			if err != nil {
				return err
			}
			news, err := c.News(cmd.Context(), day)
			if err != nil {
				return err
			}
			items := news.MessagesOfDay
			if len(args) == 1 {
				n, err := strconv.Atoi(args[0])
				if err != nil {
					return fmt.Errorf("invalid number %q", args[0])
				}
				var sel []webuntis.MessageOfDay
				for i, m := range items {
					if m.ID == n || i+1 == n {
						sel = append(sel, m)
						break
					}
				}
				if len(sel) == 0 {
					return fmt.Errorf("no message %d (there are %d messages of the day)", n, len(items))
				}
				items = sel
			}
			if items, err = a.applySeen(c, items, onlyNew, peek, withHTML); err != nil {
				return err
			}
			return a.emit(items, func() string {
				var parts []string
				for _, m := range items {
					parts = append(parts, views.NewsItem(m))
				}
				if len(parts) == 0 {
					if onlyNew {
						return "_Keine neuen Nachrichten._\n"
					}
					return "_Keine Nachrichten._\n"
				}
				return strings.Join(parts, "\n---\n\n")
			}, nil)
		},
	}
	cmd.Flags().StringVarP(&date, "date", "d", "", "day to show (default today)")
	cmd.Flags().BoolVar(&onlyNew, "new", false, "only news not shown before")
	cmd.Flags().BoolVar(&peek, "peek", false, "do not mark shown news as seen")
	cmd.Flags().BoolVar(&withHTML, "html", false, "include the original HTML body in json/yaml output")
	cmd.AddCommand(a.newsForwardCmd())
	return cmd
}

// ---------------------------------------------------------------- timetable

const ownClass = "@own"

func (a *app) timetableCmd() *cobra.Command {
	var date, class, resType, resName, outFile, pdfEngine string
	var day, grid, list, next bool
	var days int
	cmd := &cobra.Command{
		Use:     "timetable [DATE]",
		Aliases: []string{"tt", "stundenplan"},
		Short:   "Timetable of the student (default) or the class",
		Long: `Shows a timetable. Default: the student's timetable (Mein Stundenplan) for
the week of DATE (weekends jump to the next week).

DATE accepts 2026-09-21, 21.09., today, tomorrow, monday, +1w, next-week…

Pretty output is a colored week grid; --list shows one table per day.
Use -o ics to export as calendar (e.g. for subscriptions via cron).

-o html writes a standalone page for the browser or printing (a week fits
on one A4 landscape page). -o pdf writes the same layout as PDF to --file,
to stdout when piped, or else to stundenplan-<date>.pdf. --pdf-engine:
  auto     headless Chrome/Chromium/Edge if installed, else native (default)
  browser  print the HTML page with the browser ($WEBUNTIS_BROWSER selects one)
  native   built-in renderer, needs no browser (servers, cron)`,
		Example: `  webuntis timetable
  webuntis tt next-week
  webuntis tt --day tomorrow
  webuntis tt --class              # timetable of the student's class
  webuntis tt --class 6c --list
  webuntis tt --days 28 -o ics > stundenplan.ics
  webuntis tt -o html > stundenplan.html
  webuntis tt next-week -o pdf --file stundenplan.pdf`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if len(args) == 1 {
				date = args[0]
			}
			base, err := dates.Parse(date)
			if err != nil {
				return err
			}
			var start, end time.Time
			switch {
			case day:
				start, end = base, base
			case days > 0:
				start, end = base, base.AddDate(0, 0, days-1)
			default:
				if date == "" && (base.Weekday() == time.Saturday || base.Weekday() == time.Sunday) || next {
					base = base.AddDate(0, 0, 7)
				}
				start = dates.Monday(base)
				end = start.AddDate(0, 0, 4)
			}
			c, err := a.api()
			if err != nil {
				return err
			}
			q := webuntis.TimetableQuery{Start: start, End: end}
			name := ""
			switch {
			case cmd.Flags().Changed("class"):
				sel := class
				if sel == ownClass {
					sel = ""
				}
				r, err := c.ResolveResource(ctx, "CLASS", sel, start, end)
				if err != nil {
					return err
				}
				q.ResourceType, q.ResourceID, q.TimetableType = "CLASS", r.ID, "STANDARD"
			case resType != "":
				rt := strings.ToUpper(resType)
				r, err := c.ResolveResource(ctx, rt, resName, start, end)
				if err != nil {
					return err
				}
				q.ResourceType, q.ResourceID, q.TimetableType = rt, r.ID, "STANDARD"
			default:
				st, err := c.ResolveStudent(ctx, a.student)
				if err != nil {
					return err
				}
				q.ResourceType, q.ResourceID, q.TimetableType = "STUDENT", st.ID, "MY_TIMETABLE"
				name = st.Name
			}
			tt, err := c.Timetable(ctx, q)
			if err != nil {
				return err
			}
			if q.ResourceType == "STUDENT" {
				// The API only returns the surname; use the full student name.
				tt.Resource.LongName = firstNonEmpty(name, tt.Resource.LongName, tt.Resource.DisplayName, tt.Resource.ShortName)
				name = ""
			}
			useGrid := !list && !day && (grid || end.Sub(start) <= 7*24*time.Hour)
			cal := func() *ics.Calendar { return views.TimetableICS(tt, c.Profile.School) }
			if a.format == render.HTMLPage || a.format == render.PDF {
				return a.timetableDocument(ctx, tt, name, useGrid, start, outFile, pdfEngine)
			}
			if a.format == render.Pretty && useGrid {
				r := a.renderer()
				return r.Raw(views.TimetableGridPretty(tt, name, r.Color(), r.Width))
			}
			return a.emit(tt, func() string {
				if useGrid {
					return views.TimetableGridMD(tt, name)
				}
				return views.TimetableList(tt, name)
			}, cal)
		},
	}
	f := cmd.Flags()
	f.StringVarP(&date, "date", "d", "", "date within the week to show (default today)")
	f.BoolVar(&day, "day", false, "show a single day")
	f.IntVar(&days, "days", 0, "show N days starting at DATE")
	f.BoolVarP(&next, "next", "n", false, "show the following week")
	f.BoolVarP(&grid, "grid", "g", false, "force grid view")
	f.BoolVarP(&list, "list", "l", false, "list view (one table per day)")
	f.StringVarP(&class, "class", "c", "", "show a class timetable (default: the student's class)")
	f.Lookup("class").NoOptDefVal = ownClass
	f.StringVar(&resType, "resource-type", "", "other timetable type: TEACHER, ROOM, SUBJECT, STUDENT (if permitted)")
	f.StringVar(&resName, "resource", "", "name or id for --resource-type")
	f.StringVarP(&outFile, "file", "O", "", "write html/pdf output to this file")
	f.StringVar(&pdfEngine, "pdf-engine", envOr("WEBUNTIS_PDF_ENGINE", "auto"), "pdf renderer: auto, browser or native (no browser needed)")
	_ = cmd.RegisterFlagCompletionFunc("pdf-engine", staticCompletion("auto", "browser", "native"))
	return cmd
}

// timetableDocument writes the timetable as HTML or PDF.
func (a *app) timetableDocument(ctx context.Context, tt *webuntis.Timetable, name string, grid bool, start time.Time, outFile, engine string) error {
	if a.format == render.HTMLPage {
		page, err := views.TimetableHTML(tt, name, grid)
		if err != nil {
			return err
		}
		if outFile == "" || outFile == "-" {
			return a.renderer().Raw(page)
		}
		return os.WriteFile(outFile, []byte(page), 0o644)
	}
	pdf, err := timetablePDF(ctx, tt, name, grid, engine)
	if err != nil {
		return err
	}
	if outFile == "" {
		outFile = "-"
		if render.IsTTY() {
			outFile = "stundenplan-" + dates.ISO(start) + ".pdf"
		}
	}
	if outFile == "-" {
		_, err = os.Stdout.Write(pdf)
		return err
	}
	if err := os.WriteFile(outFile, pdf, 0o644); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "PDF gespeichert:", outFile)
	return nil
}

// timetablePDF renders the PDF with a headless browser (best fidelity) or
// the built-in renderer, which works without any browser.
func timetablePDF(ctx context.Context, tt *webuntis.Timetable, name string, grid bool, engine string) ([]byte, error) {
	switch strings.ToLower(engine) {
	case "native":
		return views.TimetablePDF(tt, name, grid)
	case "auto", "":
		if _, err := render.FindBrowser(); err != nil {
			return views.TimetablePDF(tt, name, grid)
		}
	case "browser", "chrome", "chromium":
	default:
		return nil, fmt.Errorf("unknown --pdf-engine %q (auto, browser, native)", engine)
	}
	page, err := views.TimetableHTML(tt, name, grid)
	if err != nil {
		return nil, err
	}
	return render.HTMLToPDF(ctx, page)
}
