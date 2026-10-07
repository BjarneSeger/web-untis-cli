package views

import (
	_ "embed"
	"fmt"
	"hash/fnv"
	"html/template"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dgrieser/web-untis-cli/internal/dates"
	"github.com/dgrieser/web-untis-cli/internal/webuntis"
)

//go:embed timetable.html.tmpl
var timetableTmplSrc string

var timetableTmpl = template.Must(template.New("timetable").
	Funcs(template.FuncMap{"add": func(a, b int) int { return a + b }}).
	Parse(timetableTmplSrc))

type htmlPage struct {
	Title     string
	Subtitle  string
	Generated string
	Grid      *htmlGrid
	Days      []htmlDay
	Empty     bool
}

type htmlGrid struct {
	Cols   []htmlCol
	Slots  []htmlSlot
	Cells  []htmlCell
	Blocks []htmlBlock
	// Columns / Rows are the CSS grid track definitions.
	Columns template.CSS
	Rows    template.CSS
}

type htmlCol struct {
	Weekday string
	Date    string
	Today   bool
	Status  string
	AllDay  []htmlLesson
}

type htmlSlot struct {
	Label, Start, End string
	Row               int
}

type htmlCell struct {
	Row, Col int
	Today    bool
}

// htmlBlock is a group of overlapping lessons of one day. It spans the
// union of their periods and places them side by side in lanes.
type htmlBlock struct {
	Col, RowStart, RowEnd int
	Lanes, Rows           int
	Lessons               []htmlLesson
}

type htmlLesson struct {
	Time        string
	Subject     string
	SubjectLong string
	Teacher     template.HTML
	Room        template.HTML
	Class       string
	Info        string
	Badge       string
	Kind        string // css modifier: cancelled, changed, exam, event
	Style       template.CSS
	Row, RowEnd int
	Lane        int
}

type htmlDay struct {
	Title   string
	Today   bool
	Note    string
	Lessons []htmlLesson
}

// TimetableHTML renders the timetable as a standalone, printable HTML page:
// a week grid (one A4 landscape page) or, with grid=false, one table per day.
func TimetableHTML(tt *webuntis.Timetable, name string, grid bool) (string, error) {
	p := htmlPage{
		Title:     strings.TrimPrefix(ttTitle(tt), "📅 "),
		Subtitle:  rangeLabel(tt),
		Generated: "Stand: " + dates.WeekdayLong(time.Now()) + ", " + time.Now().Format("02.01.2006 15:04"),
	}
	if name != "" {
		p.Title += " · " + name
	}
	if grid {
		p.Grid = buildHTMLGrid(tt)
		p.Empty = p.Grid == nil
	} else {
		p.Days = buildHTMLDays(tt)
		p.Empty = len(p.Days) == 0
	}
	var b strings.Builder
	if err := timetableTmpl.Execute(&b, p); err != nil {
		return "", err
	}
	return b.String(), nil
}

func buildHTMLGrid(tt *webuntis.Timetable) *htmlGrid {
	days := visibleDays(tt)
	slots := usedSlots(tt, days)
	if len(slots) == 0 {
		return nil
	}
	g := &htmlGrid{
		Columns: template.CSS(fmt.Sprintf("4.6em repeat(%d, minmax(0, 1fr))", len(days))),
		Rows:    template.CSS(fmt.Sprintf("auto repeat(%d, minmax(var(--row-min), 1fr))", len(slots))),
	}
	for i, s := range slots {
		g.Slots = append(g.Slots, htmlSlot{Label: strings.TrimSpace(s.label), Start: s.start, End: s.end, Row: i + 2})
	}
	for ci, day := range days {
		col := ci + 2
		today := dates.Day(day.Date).Equal(dates.Today())
		hc := htmlCol{Weekday: dates.WeekdayLong(day.Date), Date: day.Date.Format("02.01."), Today: today}
		if day.Status != "" && day.Status != "REGULAR" {
			hc.Status = strings.ToLower(day.Status)
		}
		for i := range slots {
			g.Cells = append(g.Cells, htmlCell{Row: i + 2, Col: col, Today: today})
		}
		type placed struct {
			l        webuntis.Lesson
			from, to int // slot indexes, inclusive
		}
		var ps []placed
		for _, l := range day.Lessons {
			if l.AllDay {
				hc.AllDay = append(hc.AllDay, htmlLessonOf(l))
				continue
			}
			from, to := -1, -1
			st, en := l.Start.Format("15:04"), l.End.Format("15:04")
			for i, s := range slots {
				if st < s.end && en > s.start {
					if from < 0 {
						from = i
					}
					to = i
				}
			}
			if from >= 0 {
				ps = append(ps, placed{l, from, to})
			}
		}
		g.Cols = append(g.Cols, hc)
		sort.SliceStable(ps, func(i, j int) bool {
			if ps[i].from != ps[j].from {
				return ps[i].from < ps[j].from
			}
			return ps[i].to > ps[j].to
		})
		// Split into clusters of overlapping lessons and assign lanes.
		for i := 0; i < len(ps); {
			from, to := ps[i].from, ps[i].to
			j := i + 1
			for j < len(ps) && ps[j].from <= to {
				to = max(to, ps[j].to)
				j++
			}
			var laneEnd []int
			blk := htmlBlock{Col: col, RowStart: from + 2, RowEnd: to + 3, Rows: to - from + 1}
			for _, p := range ps[i:j] {
				lane := -1
				for k, e := range laneEnd {
					if e < p.from {
						lane = k
						break
					}
				}
				if lane < 0 {
					laneEnd = append(laneEnd, 0)
					lane = len(laneEnd) - 1
				}
				laneEnd[lane] = p.to
				hl := htmlLessonOf(p.l)
				hl.Row, hl.RowEnd, hl.Lane = p.from-from+1, p.to-from+2, lane+1
				blk.Lessons = append(blk.Lessons, hl)
			}
			blk.Lanes = len(laneEnd)
			g.Blocks = append(g.Blocks, blk)
			i = j
		}
	}
	return g
}

func buildHTMLDays(tt *webuntis.Timetable) []htmlDay {
	var out []htmlDay
	for _, day := range tt.Days {
		wd := day.Date.Weekday()
		if len(day.Lessons) == 0 && (wd == time.Saturday || wd == time.Sunday) {
			continue
		}
		hd := htmlDay{Title: dates.WeekdayLong(day.Date) + ", " + day.Date.Format("02.01.2006"), Today: dates.Day(day.Date).Equal(dates.Today())}
		if len(day.Lessons) == 0 {
			hd.Note = "Kein Unterricht."
			if day.Status != "" && day.Status != "REGULAR" {
				hd.Note = strings.ToLower(day.Status)
			}
		}
		for _, l := range day.Lessons {
			hd.Lessons = append(hd.Lessons, htmlLessonOf(l))
		}
		out = append(out, hd)
	}
	return out
}

func htmlLessonOf(l webuntis.Lesson) htmlLesson {
	h := htmlLesson{
		Time:    l.Start.Format("15:04") + "–" + l.End.Format("15:04"),
		Subject: l.SubjectLabel(),
		Teacher: htmlElements(l.Teachers),
		Room:    htmlElements(l.Rooms),
		Class:   l.ClassLabel(),
		Info:    l.Info(),
	}
	if l.AllDay {
		h.Time = "ganztägig"
	}
	if long := l.SubjectLong(); long != "" && long != h.Subject {
		h.SubjectLong = titleCase(long)
	}
	switch {
	case l.Cancelled():
		h.Kind, h.Badge = "cancelled", "Entfall"
	case l.StatusDetail == "MOVED" && l.MovedFrom != nil:
		h.Kind, h.Badge = "changed", "verlegt"
		h.Info = strings.TrimPrefix(h.Info+" · verlegt von "+dates.WeekdayShort(*l.MovedFrom)+" "+l.MovedFrom.Format("15:04"), " · ")
	case l.Type == "EXAM" || strings.Contains(l.Type, "EXAM"):
		h.Kind, h.Badge = "exam", "Prüfung"
	case l.Status == "ADDITIONAL":
		h.Kind, h.Badge = "changed", "zusätzlich"
	case l.Type == "EVENT":
		h.Kind, h.Badge = "event", "Veranstaltung"
	case l.Changed():
		h.Kind, h.Badge = "changed", "geändert"
	}
	h.Style = lessonColors(l)
	return h
}

// htmlElements renders teachers / rooms, striking through replaced ones.
func htmlElements(el []webuntis.Element) template.HTML {
	var out []string
	for _, e := range el {
		esc := template.HTMLEscapeString
		switch {
		case e.Name == "" && e.Removed != "":
			out = append(out, "<s>"+esc(e.Removed)+"</s>")
		case e.Name == "":
		case e.Removed != "" && e.Removed != e.Name:
			out = append(out, `<ins>`+esc(e.Name)+"</ins> <s>"+esc(e.Removed)+"</s>")
		default:
			out = append(out, esc(e.Name))
		}
	}
	return template.HTML(strings.Join(out, ", "))
}

var hexColor = regexp.MustCompile(`^#?([0-9a-fA-F]{6})$`)

// lessonColors returns CSS variables for the lesson card: the WebUntis
// subject color if set, otherwise a stable color derived from the subject.
func lessonColors(l webuntis.Lesson) template.CSS {
	var r, g, b float64
	if m := hexColor.FindStringSubmatch(strings.TrimSpace(l.Color)); m != nil {
		v, _ := strconv.ParseUint(m[1], 16, 32)
		r, g, b = float64(v>>16&0xff)/255, float64(v>>8&0xff)/255, float64(v&0xff)/255
	} else {
		h := fnv.New32a()
		_, _ = h.Write([]byte(strings.ToUpper(l.SubjectLabel())))
		r, g, b = hslToRGB(float64(h.Sum32()%360), 0.62, 0.5)
	}
	mix := func(c, with, f float64) float64 { return c*(1-f) + with*f }
	bg := hexOf(mix(r, 1, 0.84), mix(g, 1, 0.84), mix(b, 1, 0.84))
	accent := hexOf(mix(r, 0, 0.25), mix(g, 0, 0.25), mix(b, 0, 0.25))
	return template.CSS("--bg:" + bg + ";--accent:" + accent)
}

func hexOf(r, g, b float64) string {
	c := func(v float64) int { return int(math.Round(math.Max(0, math.Min(1, v)) * 255)) }
	return fmt.Sprintf("#%02x%02x%02x", c(r), c(g), c(b))
}

func hslToRGB(h, s, l float64) (float64, float64, float64) {
	c := (1 - math.Abs(2*l-1)) * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := l - c/2
	var r, g, b float64
	switch {
	case h < 60:
		r, g = c, x
	case h < 120:
		r, g = x, c
	case h < 180:
		g, b = c, x
	case h < 240:
		g, b = x, c
	case h < 300:
		r, b = x, c
	default:
		r, b = c, x
	}
	return r + m, g + m, b + m
}
