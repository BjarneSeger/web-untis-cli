package views

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dgrieser/web-untis-cli/internal/dates"
	"github.com/dgrieser/web-untis-cli/internal/render"
	"github.com/dgrieser/web-untis-cli/internal/webuntis"
)

func at(d, hm string) time.Time {
	t, _ := time.ParseInLocation("2006-01-02 15:04", d+" "+hm, time.Local)
	return t
}

func sampleTimetable() *webuntis.Timetable {
	mon := at("2026-09-21", "00:00")
	moved := at("2026-09-21", "12:20")
	return &webuntis.Timetable{
		ResourceType: "STUDENT",
		Resource:     webuntis.Resource{ID: 8685, ShortName: "KidAlp", LongName: "Kid Alpha"},
		Start:        mon, End: mon.AddDate(0, 0, 4),
		TimeGrid: []webuntis.TimeUnit{{UnitOfDay: 1, StartTime: 745, EndTime: 830}, {UnitOfDay: 2, StartTime: 835, EndTime: 920}, {UnitOfDay: 3, StartTime: 940, EndTime: 1025}},
		Days: []webuntis.TimetableDay{
			{Date: mon, Lessons: []webuntis.Lesson{
				{IDs: []int{1}, Start: at("2026-09-21", "07:45"), End: at("2026-09-21", "09:20"), Status: "REGULAR",
					Subjects: []webuntis.Element{{Name: "D", LongName: "DEUTSCH SEK.I"}}, Teachers: []webuntis.Element{{Name: "LER"}}, Rooms: []webuntis.Element{{Name: "D104"}}},
				{IDs: []int{2}, Start: at("2026-09-21", "09:40"), End: at("2026-09-21", "10:25"), Status: "ADDITIONAL", StatusDetail: "MOVED", MovedFrom: &moved,
					Subjects: []webuntis.Element{{Name: "E"}}, Teachers: []webuntis.Element{{Name: "LER", Removed: "POD"}}},
				{IDs: []int{3}, Start: at("2026-09-21", "09:40"), End: at("2026-09-21", "10:25"), Status: "CANCELLED",
					Subjects: []webuntis.Element{{Name: "M"}}},
			}},
			{Date: mon.AddDate(0, 0, 1), Lessons: []webuntis.Lesson{
				{IDs: []int{4}, Start: at("2026-09-22", "08:35"), End: at("2026-09-22", "09:20"), Status: "REGULAR", Subjects: []webuntis.Element{{Name: "SP"}}},
			}},
		},
	}
}

func TestTimetableViews(t *testing.T) {
	tt := sampleTimetable()
	grid := TimetableGridMD(tt, "")
	for _, want := range []string{"| Std. |", "D D104 LER", "**E LER (statt POD)**", "~~M~~", "SP"} {
		if !strings.Contains(grid, want) {
			t.Errorf("grid missing %q:\n%s", want, grid)
		}
	}
	list := TimetableList(tt, "")
	if !strings.Contains(list, "❌ Entfall") || !strings.Contains(list, "↪ verlegt") {
		t.Errorf("list:\n%s", list)
	}
	pretty := TimetableGridPretty(tt, "", false, 100)
	if !strings.Contains(pretty, "✗M") || !strings.Contains(pretty, "E*") {
		t.Errorf("pretty:\n%s", pretty)
	}
	cal := TimetableICS(tt, "school").Serialize()
	if strings.Count(cal, "BEGIN:VEVENT") != 4 || !strings.Contains(cal, "STATUS:CANCELLED") {
		t.Errorf("ics:\n%s", cal)
	}
	if os.Getenv("SHOW") != "" {
		t.Log("\n" + pretty)
		t.Log("\n" + TimetableGridPretty(tt, "", true, 100))
		r := &render.Renderer{Format: render.Markdown, Out: os.Stdout}
		_ = r.Markdown(grid)
		_ = r.Markdown(list)
	}
}

func TestRowsGeneric(t *testing.T) {
	rows := []webuntis.Row{
		{"id": 1.0, "startDate": 20260921.0, "startTime": 745.0, "duty": map[string]any{"label": "Tafeldienst"}, "text": "a|b", "active": true},
	}
	out := Rows("T", "", rows, []string{"duty.label", "startDate", "startTime"}, "none")
	for _, want := range []string{"duty.label", "Tafeldienst", "Mo 21.09.2026", "07:45", `a\|b`, "✓"} {
		if !strings.Contains(out, want) {
			t.Errorf("rows missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "| id |") {
		t.Errorf("id column should be skipped:\n%s", out)
	}
}

func TestTodayAndMessage(t *testing.T) {
	news := &webuntis.News{MessagesOfDay: []webuntis.MessageOfDay{{ID: 1, Subject: "Hallo *Welt*", Text: "<font>Zeile 1<br/>Zeile <b>2</b></font>",
		Attachments: []webuntis.NewsAttachment{{Name: "Bild.png", DownloadURL: "https://x/y"}}}}}
	out := Today(TodayData{Date: at("2026-09-24", "00:00"), News: news, Unread: webuntis.UnreadCounts{Messages: 2}}, true)
	for _, want := range []string{"Donnerstag, 24.09.2026", `Hallo \*Welt\*`, "**2**", "[Bild.png](https://x/y)", "2 ungelesene"} {
		if !strings.Contains(out, want) {
			t.Errorf("today missing %q:\n%s", want, out)
		}
	}
	m := &webuntis.MessageDetail{ID: 5, Subject: "S", Content: "Liebe Eltern,\n\n> zitat\nGruß", SentDateTime: "2026-07-21T17:11:00",
		Sender: &webuntis.MessagePerson{DisplayName: "Teacher, T (TT)"}, StorageAttachments: []webuntis.StorageAttachment{{ID: "x", Name: "a.pdf"}}}
	mo := Message(m)
	if !strings.Contains(mo, "Anhänge (1)") || !strings.Contains(mo, "> zitat") {
		t.Errorf("message:\n%s", mo)
	}
}

// fullWeek is a realistic week with double periods, split groups, changes,
// cancellations and an all-day event.
func fullWeek() *webuntis.Timetable {
	mon := at("2026-09-21", "00:00")
	grid := []webuntis.TimeUnit{
		{UnitOfDay: 1, StartTime: 745, EndTime: 830}, {UnitOfDay: 2, StartTime: 835, EndTime: 920},
		{UnitOfDay: 3, StartTime: 940, EndTime: 1025}, {UnitOfDay: 4, StartTime: 1030, EndTime: 1115},
		{UnitOfDay: 5, StartTime: 1135, EndTime: 1220}, {UnitOfDay: 6, StartTime: 1225, EndTime: 1310},
		{UnitOfDay: 7, StartTime: 1340, EndTime: 1425}, {UnitOfDay: 8, StartTime: 1430, EndTime: 1515},
		{UnitOfDay: 9, StartTime: 1520, EndTime: 1605},
	}
	subj := []struct{ s, long, t, r string }{
		{"D", "DEUTSCH", "LER", "D104"}, {"M", "MATHEMATIK", "KRA", "A201"}, {"E", "ENGLISCH", "POD", "B012"},
		{"BI", "BIOLOGIE", "HUB", "NW1"}, {"GE", "GESCHICHTE", "SAL", "C110"}, {"SP", "SPORT", "BRU", "TH2"},
		{"MU", "MUSIK", "KEL", "MU1"}, {"PH", "PHYSIK", "WEI", "NW2"}, {"KU", "KUNST", "OTT", "KU1"},
	}
	tt := &webuntis.Timetable{
		ResourceType: "STUDENT",
		Resource:     webuntis.Resource{ID: 1, ShortName: "KidAlp", LongName: "Kid Alpha"},
		Start:        mon, End: mon.AddDate(0, 0, 4), TimeGrid: grid,
	}
	id := 0
	for d := range 5 {
		date := mon.AddDate(0, 0, d)
		ds := date.Format("2006-01-02")
		day := webuntis.TimetableDay{Date: date}
		last := 6 + d%3
		for u := 0; u < last; u += 2 {
			id++
			s := subj[(u/2+d*2)%len(subj)]
			end := min(u+1, last-1)
			l := webuntis.Lesson{IDs: []int{id}, Start: at(ds, dates.HM(grid[u].StartTime)), End: at(ds, dates.HM(grid[end].EndTime)), Status: "REGULAR",
				Subjects: []webuntis.Element{{Name: s.s, LongName: s.long}}, Teachers: []webuntis.Element{{Name: s.t}}, Rooms: []webuntis.Element{{Name: s.r}}}
			switch {
			case d == 1 && u == 2:
				l.Status = "CANCELLED"
				l.LessonText = "Lehrkraft erkrankt"
			case d == 2 && u == 0:
				l.Status = "CHANGED"
				l.Teachers = []webuntis.Element{{Name: "VER", Removed: s.t}}
				l.SubstitutionText = "Vertretung, Aufgaben im Moodle"
			case d == 3 && u == 4:
				l.Type = "EXAM"
				l.LessonInfo = "Klassenarbeit Nr. 1"
			}
			day.Lessons = append(day.Lessons, l)
			if d == 4 && u == 2 {
				// split groups: religion / ethics in parallel
				day.Lessons[len(day.Lessons)-1].Subjects = []webuntis.Element{{Name: "KR", LongName: "KATH. RELIGION"}}
				day.Lessons = append(day.Lessons, webuntis.Lesson{IDs: []int{100}, Start: l.Start, End: l.End, Status: "REGULAR",
					Subjects: []webuntis.Element{{Name: "ER"}}, Teachers: []webuntis.Element{{Name: "SCH"}}, Rooms: []webuntis.Element{{Name: "C204"}}})
				day.Lessons = append(day.Lessons, webuntis.Lesson{IDs: []int{101}, Start: l.Start, End: at(ds, dates.HM(grid[u].EndTime)), Status: "REGULAR",
					Subjects: []webuntis.Element{{Name: "PPL"}}, Teachers: []webuntis.Element{{Name: "MÜL"}}, Rooms: []webuntis.Element{{Name: "C206"}}})
			}
		}
		if d == 2 {
			day.Lessons = append([]webuntis.Lesson{{IDs: []int{200}, Start: date, End: date, AllDay: true, Type: "EVENT", Name: "Wandertag", Texts: []string{"Treffpunkt <Schulhof>"}}}, day.Lessons...)
		}
		tt.Days = append(tt.Days, day)
	}
	return tt
}

func TestTimetableHTML(t *testing.T) {
	tt := fullWeek()
	page, err := TimetableHTML(tt, "", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<!doctype html>", "size: A4 landscape", `class="lesson cancelled"`, `class="lesson changed"`, `class="lesson exam"`,
		"<ins>VER</ins> <s>SAL</s>", "grid-column: 3", "Treffpunkt &lt;Schulhof&gt;", "Wandertag", "KW 39", ">Deutsch</div>", ">Kath. Religion</div>"} {
		if !strings.Contains(page, want) {
			t.Errorf("grid html missing %q", want)
		}
	}
	if strings.Contains(page, "<Schulhof>") {
		t.Error("unescaped text in html")
	}
	list, err := TimetableHTML(sampleTimetable(), "", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`<body class="list">`, "<section class=\"day", "Entfall", "verlegt von Mo 12:20", "Deutsch Sek"} {
		if !strings.Contains(list, want) {
			t.Errorf("list html missing %q", want)
		}
	}
	if dir := os.Getenv("HTML_OUT"); dir != "" {
		_ = os.WriteFile(dir+"/grid.html", []byte(page), 0o644)
		_ = os.WriteFile(dir+"/list.html", []byte(list), 0o644)
	}
}
