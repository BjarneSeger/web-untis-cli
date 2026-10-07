package views

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"

	"github.com/dgrieser/web-untis-cli/internal/dates"
	md "github.com/dgrieser/web-untis-cli/internal/render"
	"github.com/dgrieser/web-untis-cli/internal/webuntis"
)

// LessonHeaders are the columns of the lesson list.
var LessonHeaders = []string{"Zeit", "Fach", "Lehrkraft", "Raum", "Status", "Info"}

func statusLabel(l webuntis.Lesson) string {
	switch {
	case l.Cancelled():
		return "❌ Entfall"
	case l.StatusDetail == "MOVED" && l.MovedFrom != nil:
		return "↪ verlegt (von " + dates.WeekdayShort(*l.MovedFrom) + " " + l.MovedFrom.Format("15:04") + ")"
	case l.Status == "ADDITIONAL":
		return "➕ zusätzlich"
	case l.Type == "EXAM" || strings.Contains(l.Type, "EXAM"):
		return "📝 Prüfung"
	case l.Status == "CHANGED" || l.Status == "SUBSTITUTION":
		return "🔄 geändert"
	case l.Type == "EVENT":
		return "🎉 Veranstaltung"
	case l.Status != "" && l.Status != "REGULAR":
		return strings.ToLower(l.Status)
	}
	return ""
}

func lessonRows(lessons []webuntis.Lesson) [][]string {
	var rows [][]string
	for _, l := range lessons {
		tm := l.Start.Format("15:04") + "–" + l.End.Format("15:04")
		if l.AllDay {
			tm = "ganztägig"
		}
		subj := md.Esc(l.SubjectLabel())
		if long := l.SubjectLong(); long != "" && long != l.SubjectLabel() {
			subj += " _(" + md.Esc(long) + ")_"
		}
		switch {
		case l.Cancelled():
			subj = "~~" + subj + "~~"
		case l.Changed():
			subj = "**" + subj + "**"
		}
		rows = append(rows, []string{tm, subj, md.Esc(l.TeacherLabel()), md.Esc(l.RoomLabel()), statusLabel(l), md.Esc(l.Info())})
	}
	return rows
}

func titleCase(s string) string {
	if strings.ToUpper(s) != s {
		return s
	}
	r := []rune(strings.ToLower(s))
	for i := range r {
		// capitalize words, also after "." / "-" / "/" ("Sek.I", "Kath.-Religion")
		if i == 0 || strings.ContainsRune(" .-/(", r[i-1]) {
			r[i] = unicode.ToUpper(r[i])
		}
	}
	return strings.Join(strings.Fields(string(r)), " ")
}

func ttTitle(tt *webuntis.Timetable) string {
	name := tt.Resource.Label()
	if tt.Resource.LongName != "" && tt.Resource.LongName != name && tt.ResourceType != "STUDENT" {
		name += " (" + titleCase(tt.Resource.LongName) + ")"
	}
	if tt.ResourceType == "STUDENT" && tt.Resource.LongName != "" {
		name = tt.Resource.LongName
	}
	kind := map[string]string{"STUDENT": "Mein Stundenplan", "CLASS": "Klasse", "TEACHER": "Lehrkraft", "ROOM": "Raum", "SUBJECT": "Fach"}[tt.ResourceType]
	return fmt.Sprintf("📅 %s · %s", kind, name)
}

func rangeLabel(tt *webuntis.Timetable) string {
	if dates.Day(tt.Start).Equal(dates.Day(tt.End)) {
		return dates.WeekdayLong(tt.Start) + ", " + tt.Start.Format("02.01.2006")
	}
	_, w := tt.Start.ISOWeek()
	return fmt.Sprintf("KW %d · %s – %s", w, dates.Human(tt.Start), dates.Human(tt.End))
}

// TimetableList renders the timetable as one table per day.
func TimetableList(tt *webuntis.Timetable, name string) string {
	var d md.Doc
	title := ttTitle(tt)
	if name != "" {
		title += " · " + md.Esc(name)
	}
	d.H(1, "%s", title)
	d.Pf("_%s_", rangeLabel(tt))
	any := false
	for _, day := range tt.Days {
		if len(day.Lessons) == 0 && day.Date.Weekday() == time.Saturday || len(day.Lessons) == 0 && day.Date.Weekday() == time.Sunday {
			continue
		}
		any = true
		h := dates.WeekdayLong(day.Date) + ", " + day.Date.Format("02.01.")
		if dates.Day(day.Date).Equal(dates.Today()) {
			h += " · heute"
		}
		d.H(2, "%s", h)
		if len(day.Lessons) == 0 {
			if day.Status != "" && day.Status != "REGULAR" {
				d.Empty(strings.ToLower(day.Status))
			} else {
				d.Empty("Kein Unterricht.")
			}
			continue
		}
		d.Table(LessonHeaders, lessonRows(day.Lessons))
	}
	if !any {
		d.Empty("Keine Einträge.")
	}
	return d.String()
}

type slot struct {
	start, end string
	label      string
}

// gridSlots derives the rows of a weekly grid: the school's time grid if
// available, otherwise the distinct start/end times of all lessons.
func gridSlots(tt *webuntis.Timetable) []slot {
	var slots []slot
	for _, u := range tt.TimeGrid {
		slots = append(slots, slot{dates.HM(u.StartTime), dates.HM(u.EndTime), fmt.Sprintf("%d.", u.UnitOfDay)})
	}
	seen := map[string]bool{}
	for _, s := range slots {
		seen[s.start] = true
	}
	for _, day := range tt.Days {
		for _, l := range day.Lessons {
			if l.AllDay {
				continue
			}
			st := l.Start.Format("15:04")
			if !covered(slots, st) && !seen[st] {
				seen[st] = true
				slots = append(slots, slot{st, l.End.Format("15:04"), ""})
			}
		}
	}
	sort.Slice(slots, func(i, j int) bool { return slots[i].start < slots[j].start })
	return slots
}

func covered(slots []slot, t string) bool {
	for _, s := range slots {
		if t >= s.start && t < s.end {
			return true
		}
	}
	return false
}

// lessonsIn returns lessons overlapping slot s.
func lessonsIn(day webuntis.TimetableDay, s slot) []webuntis.Lesson {
	var out []webuntis.Lesson
	for _, l := range day.Lessons {
		if l.AllDay {
			continue
		}
		st, en := l.Start.Format("15:04"), l.End.Format("15:04")
		if st < s.end && en > s.start {
			out = append(out, l)
		}
	}
	return out
}

func visibleDays(tt *webuntis.Timetable) []webuntis.TimetableDay {
	var out []webuntis.TimetableDay
	for _, d := range tt.Days {
		wd := d.Date.Weekday()
		if (wd == time.Saturday || wd == time.Sunday) && len(d.Lessons) == 0 {
			continue
		}
		out = append(out, d)
	}
	return out
}

func usedSlots(tt *webuntis.Timetable, days []webuntis.TimetableDay) []slot {
	all := gridSlots(tt)
	last := -1
	first := len(all)
	for i, s := range all {
		for _, d := range days {
			if len(lessonsIn(d, s)) > 0 {
				if i > last {
					last = i
				}
				if i < first {
					first = i
				}
			}
		}
	}
	if last < 0 {
		return nil
	}
	return all[first : last+1]
}

// TimetableGridMD renders a week as a Markdown grid (periods x days).
func TimetableGridMD(tt *webuntis.Timetable, name string) string {
	var d md.Doc
	title := ttTitle(tt)
	if name != "" {
		title += " · " + md.Esc(name)
	}
	d.H(1, "%s", title)
	d.Pf("_%s_", rangeLabel(tt))
	days := visibleDays(tt)
	headers := []string{"Std."}
	for _, day := range days {
		headers = append(headers, dates.WeekdayShort(day.Date)+" "+day.Date.Format("02.01."))
	}
	var rows [][]string
	for _, s := range usedSlots(tt, days) {
		row := []string{strings.TrimSpace(s.label + " " + s.start)}
		for _, day := range days {
			var cells []string
			for _, l := range lessonsIn(day, s) {
				c := md.Esc(l.SubjectLabel())
				if r := l.RoomLabel(); r != "" {
					c += " " + md.Esc(r)
				}
				if t := l.TeacherLabel(); t != "" {
					c += " " + md.Esc(t)
				}
				switch {
				case l.Cancelled():
					c = "~~" + c + "~~"
				case l.Changed():
					c = "**" + c + "**"
				}
				cells = append(cells, c)
			}
			row = append(row, strings.Join(cells, " / "))
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return d.Empty("Keine Einträge.").String()
	}
	d.Table(headers, rows)
	d.Raw(allDayNotes(days))
	d.P("_**fett** = Änderung · ~~durchgestrichen~~ = Entfall_")
	return d.String()
}

func allDayNotes(days []webuntis.TimetableDay) string {
	var b strings.Builder
	for _, day := range days {
		for _, l := range day.Lessons {
			if l.AllDay {
				fmt.Fprintf(&b, "- **%s:** %s %s\n", dates.Human(day.Date), md.Esc(l.SubjectLabel()), md.Esc(l.Info()))
			}
		}
	}
	if b.Len() > 0 {
		b.WriteString("\n")
	}
	return b.String()
}

// TimetableGridPretty renders a week as a colored terminal grid.
func TimetableGridPretty(tt *webuntis.Timetable, name string, color bool, width int) string {
	days := visibleDays(tt)
	slots := usedSlots(tt, days)
	title := strings.TrimPrefix(ttTitle(tt), "📅 ")
	if name != "" {
		title += " · " + name
	}
	head := lipgloss.NewStyle().Bold(true)
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("7")) // no Faint: unreadable on many dark themes
	cancelled := lipgloss.NewStyle().Strikethrough(true).Foreground(lipgloss.Color("9"))
	changed := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("11"))
	subjStyle := lipgloss.NewStyle().Bold(true)
	todayStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	if !color {
		plain := lipgloss.NewStyle()
		head, dim, subjStyle, todayStyle = plain, plain, plain, plain
		cancelled = plain.Strikethrough(false)
		changed = plain
	}

	var out strings.Builder
	out.WriteString("\n  " + head.Render(title) + "\n  " + dim.Render(rangeLabel(tt)) + "\n\n")
	if len(slots) == 0 {
		out.WriteString("  Keine Einträge.\n")
		return out.String()
	}

	headers := []string{""}
	for _, day := range days {
		h := dates.WeekdayShort(day.Date) + " " + day.Date.Format("02.01.")
		if dates.Day(day.Date).Equal(dates.Today()) {
			h = todayStyle.Render(h + " •")
		}
		headers = append(headers, h)
	}
	colW := max((width-12)/max(1, len(days)), 10)
	var rows [][]string
	for _, s := range slots {
		row := []string{strings.TrimSpace(s.label) + "\n" + dim.Render(s.start)}
		for _, day := range days {
			var parts []string
			for _, l := range lessonsIn(day, s) {
				subj := l.SubjectLabel()
				meta := strings.TrimSpace(l.RoomLabel() + " " + l.TeacherLabel())
				var cell string
				switch {
				case l.Cancelled():
					cell = cancelled.Render(subj)
					if !color {
						cell = "✗" + subj
					}
					if meta != "" {
						cell += "\n" + dim.Render(meta)
					}
				case l.Changed():
					cell = changed.Render(subj)
					if !color {
						cell = subj + "*"
					}
					if meta != "" {
						cell += "\n" + changed.Render(meta)
					}
				default:
					cell = subjStyle.Render(subj)
					if meta != "" {
						cell += "\n" + dim.Render(meta)
					}
				}
				parts = append(parts, cell)
			}
			row = append(row, strings.Join(parts, "\n"))
		}
		rows = append(rows, row)
	}
	border := lipgloss.RoundedBorder()
	t := table.New().
		Border(border).
		BorderStyle(dim).
		BorderRow(true).
		Headers(headers...).
		Rows(rows...).
		StyleFunc(func(row, col int) lipgloss.Style {
			s := lipgloss.NewStyle().Padding(0, 1)
			if col > 0 {
				s = s.Width(colW)
			}
			if row == table.HeaderRow {
				s = s.Bold(color)
			}
			return s
		})
	out.WriteString(t.Render())
	out.WriteString("\n")
	notes := allDayNotes(days)
	if notes != "" {
		out.WriteString("\n" + strings.ReplaceAll(strings.ReplaceAll(notes, "**", ""), "\\", ""))
	}
	legend := "  " + changed.Render("geändert") + " · " + cancelled.Render("Entfall")
	if !color {
		legend = "  * = geändert · ✗ = Entfall"
	}
	out.WriteString(legend + "\n\n")
	return out.String()
}
