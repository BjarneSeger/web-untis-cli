package webuntis

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dgrieser/web-untis-cli/internal/dates"
)

// Resource is a timetable resource (class, teacher, room, subject, student).
type Resource struct {
	ID          int    `json:"id" yaml:"id"`
	ShortName   string `json:"shortName" yaml:"shortName"`
	LongName    string `json:"longName" yaml:"longName"`
	DisplayName string `json:"displayName" yaml:"displayName"`
}

// Label returns the best display label.
func (r Resource) Label() string {
	if r.DisplayName != "" {
		return r.DisplayName
	}
	if r.ShortName != "" {
		return r.ShortName
	}
	return r.LongName
}

type ttElement struct {
	Type        string `json:"type"`
	Status      string `json:"status"`
	ShortName   string `json:"shortName"`
	LongName    string `json:"longName"`
	DisplayName string `json:"displayName"`
}

type ttPosition struct {
	Current *ttElement `json:"current"`
	Removed *ttElement `json:"removed"`
}

type ttText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type ttEntry struct {
	IDs              []int        `json:"ids"`
	Duration         DateRange    `json:"duration"`
	Type             string       `json:"type"`
	Status           string       `json:"status"`
	StatusDetail     string       `json:"statusDetail"`
	Name             string       `json:"name"`
	LayoutGroup      int          `json:"layoutGroup"`
	Color            string       `json:"color"`
	NotesAll         string       `json:"notesAll"`
	Icons            []string     `json:"icons"`
	Position1        []ttPosition `json:"position1"`
	Position2        []ttPosition `json:"position2"`
	Position3        []ttPosition `json:"position3"`
	Position4        []ttPosition `json:"position4"`
	Position5        []ttPosition `json:"position5"`
	Position6        []ttPosition `json:"position6"`
	Position7        []ttPosition `json:"position7"`
	Texts            []ttText     `json:"texts"`
	LessonText       string       `json:"lessonText"`
	LessonInfo       string       `json:"lessonInfo"`
	SubstitutionText string       `json:"substitutionText"`
	UserName         string       `json:"userName"`
	Moved            *DateRange   `json:"moved"`
	Link             any          `json:"link"`
}

type ttDay struct {
	Date         string    `json:"date"`
	ResourceType string    `json:"resourceType"`
	Resource     Resource  `json:"resource"`
	Status       string    `json:"status"`
	DayEntries   []ttEntry `json:"dayEntries"`
	GridEntries  []ttEntry `json:"gridEntries"`
	BackEntries  []ttEntry `json:"backEntries"`
}

// Element is a normalized timetable element with change information.
type Element struct {
	Name     string `json:"name" yaml:"name"`
	LongName string `json:"longName,omitempty" yaml:"longName,omitempty"`
	Status   string `json:"status,omitempty" yaml:"status,omitempty"` // REGULAR, ADDED, REMOVED, CHANGED...
	Removed  string `json:"removed,omitempty" yaml:"removed,omitempty"`
}

// Lesson is a normalized timetable entry.
type Lesson struct {
	IDs              []int      `json:"ids" yaml:"ids"`
	Start            time.Time  `json:"start" yaml:"start"`
	End              time.Time  `json:"end" yaml:"end"`
	Type             string     `json:"type" yaml:"type"`     // NORMAL_TEACHING_PERIOD, ADDITIONAL_PERIOD, EXAM, EVENT, ...
	Status           string     `json:"status" yaml:"status"` // REGULAR, CHANGED, CANCELLED, ADDITIONAL, ...
	StatusDetail     string     `json:"statusDetail,omitempty" yaml:"statusDetail,omitempty"`
	Name             string     `json:"name,omitempty" yaml:"name,omitempty"`
	Subjects         []Element  `json:"subjects,omitempty" yaml:"subjects,omitempty"`
	Teachers         []Element  `json:"teachers,omitempty" yaml:"teachers,omitempty"`
	Rooms            []Element  `json:"rooms,omitempty" yaml:"rooms,omitempty"`
	Classes          []Element  `json:"classes,omitempty" yaml:"classes,omitempty"`
	Others           []Element  `json:"others,omitempty" yaml:"others,omitempty"`
	Texts            []string   `json:"texts,omitempty" yaml:"texts,omitempty"`
	LessonText       string     `json:"lessonText,omitempty" yaml:"lessonText,omitempty"`
	LessonInfo       string     `json:"lessonInfo,omitempty" yaml:"lessonInfo,omitempty"`
	SubstitutionText string     `json:"substitutionText,omitempty" yaml:"substitutionText,omitempty"`
	Notes            string     `json:"notes,omitempty" yaml:"notes,omitempty"`
	MovedFrom        *time.Time `json:"movedFrom,omitempty" yaml:"movedFrom,omitempty"`
	Color            string     `json:"color,omitempty" yaml:"color,omitempty"`
	AllDay           bool       `json:"allDay,omitempty" yaml:"allDay,omitempty"`
	Icons            []string   `json:"icons,omitempty" yaml:"icons,omitempty"`
}

// Cancelled reports whether the lesson does not take place.
func (l Lesson) Cancelled() bool {
	s := strings.ToUpper(l.Status + " " + l.StatusDetail)
	return strings.Contains(s, "CANCEL") || strings.Contains(s, "REMOVED")
}

// Changed reports substitutions, room changes, additional or moved lessons.
func (l Lesson) Changed() bool {
	if l.Cancelled() {
		return false
	}
	return l.Status != "" && l.Status != "REGULAR"
}

// SubjectLabel returns "D" etc.
func (l Lesson) SubjectLabel() string {
	if n := names(l.Subjects, false); n != "" {
		return n
	}
	if l.Name != "" {
		return l.Name
	}
	return strings.ReplaceAll(strings.ToLower(l.Type), "_", " ")
}

// SubjectLong returns the long subject name.
func (l Lesson) SubjectLong() string {
	var out []string
	for _, s := range l.Subjects {
		if s.LongName != "" {
			out = append(out, s.LongName)
		} else {
			out = append(out, s.Name)
		}
	}
	return strings.Join(out, ", ")
}

func names(el []Element, withRemoved bool) string {
	var out []string
	for _, e := range el {
		if e.Name == "" && withRemoved && e.Removed != "" {
			out = append(out, "~"+e.Removed+"~")
			continue
		}
		if e.Name == "" {
			continue
		}
		s := e.Name
		if withRemoved && e.Removed != "" && e.Removed != e.Name {
			s += " (statt " + e.Removed + ")"
		}
		out = append(out, s)
	}
	return strings.Join(out, ", ")
}

// TeacherLabel e.g. "LER (statt POD)".
func (l Lesson) TeacherLabel() string { return names(l.Teachers, true) }

// RoomLabel e.g. "D104".
func (l Lesson) RoomLabel() string { return names(l.Rooms, true) }

// ClassLabel e.g. "6c".
func (l Lesson) ClassLabel() string { return names(l.Classes, false) }

// Info joins all informational texts.
func (l Lesson) Info() string {
	var parts []string
	seen := map[string]bool{}
	for _, s := range append([]string{l.SubstitutionText, l.LessonText, l.LessonInfo, l.Notes}, l.Texts...) {
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " · ")
}

// TimetableDay is one day of a timetable.
type TimetableDay struct {
	Date     time.Time `json:"date" yaml:"date"`
	Status   string    `json:"status" yaml:"status"`
	Resource Resource  `json:"resource" yaml:"resource"`
	Lessons  []Lesson  `json:"lessons" yaml:"lessons"`
}

// Timetable is the normalized result.
type Timetable struct {
	ResourceType string         `json:"resourceType" yaml:"resourceType"`
	Resource     Resource       `json:"resource" yaml:"resource"`
	Start        time.Time      `json:"start" yaml:"start"`
	End          time.Time      `json:"end" yaml:"end"`
	Days         []TimetableDay `json:"days" yaml:"days"`
	TimeGrid     []TimeUnit     `json:"timeGrid,omitempty" yaml:"timeGrid,omitempty"`
	// Regular is set by WithoutChanges(): changes have been removed.
	Regular bool `json:"regular,omitempty" yaml:"regular,omitempty"`
}

// WithoutChanges returns the regular timetable (Regelstundenplan) without changes:
// cancelled lessons take place, substituted teachers / rooms / subjects
// are replaced by the original ones, and additional or moved-in lessons,
// exams, events and all-day entries are dropped. It is derived from the
// change information WebUntis sends along with each entry.
func (tt *Timetable) WithoutChanges() *Timetable {
	out := *tt
	out.Regular = true
	out.Days = make([]TimetableDay, len(tt.Days))
	for i, d := range tt.Days {
		nd := d
		nd.Lessons = nil
		seen := map[string]bool{}
		var exams []Lesson
		for _, l := range d.Lessons {
			if l.exam() {
				exams = append(exams, l)
				continue
			}
			if l.extra() {
				continue
			}
			r := Lesson{
				IDs: l.IDs, Start: l.Start, End: l.End, Type: l.Type, Status: "REGULAR", Name: l.Name,
				Subjects: regularElements(l.Subjects), Teachers: regularElements(l.Teachers),
				Rooms: regularElements(l.Rooms), Classes: regularElements(l.Classes), Others: regularElements(l.Others),
				LessonInfo: l.LessonInfo, Color: l.Color,
			}
			key := r.Start.String() + r.End.String() + r.SubjectLabel() + r.TeacherLabel() + r.RoomLabel()
			if seen[key] {
				continue
			}
			seen[key] = true
			nd.Lessons = append(nd.Lessons, r)
		}
		// An exam replaces the lesson it is written in: keep it as that
		// lesson unless the regular lesson is there anyway.
		for _, e := range exams {
			free := len(e.Subjects) > 0
			for _, l := range nd.Lessons {
				if l.Start.Before(e.End) && e.Start.Before(l.End) {
					free = false
				}
			}
			if free {
				nd.Lessons = append(nd.Lessons, Lesson{IDs: e.IDs, Start: e.Start, End: e.End, Type: "NORMAL_TEACHING_PERIOD", Status: "REGULAR",
					Subjects: regularElements(e.Subjects), Teachers: regularElements(e.Teachers), Rooms: regularElements(e.Rooms),
					Classes: regularElements(e.Classes), Color: e.Color})
			}
		}
		sort.SliceStable(nd.Lessons, func(i, j int) bool { return nd.Lessons[i].Start.Before(nd.Lessons[j].Start) })
		out.Days[i] = nd
	}
	return &out
}

// extra reports entries that are not part of the regular timetable.
func (l Lesson) extra() bool {
	t := strings.ToUpper(l.Type)
	return l.AllDay || l.Status == "ADDITIONAL" || t == "ADDITIONAL_PERIOD" || t == "EVENT"
}

func (l Lesson) exam() bool { return !l.AllDay && strings.Contains(strings.ToUpper(l.Type), "EXAM") }

// regularElements restores the original elements of a changed lesson.
func regularElements(el []Element) []Element {
	var out []Element
	for _, e := range el {
		switch {
		case e.Removed != "" && e.Removed == e.Name:
			out = append(out, Element{Name: e.Name, LongName: e.LongName, Status: "REGULAR"})
		case e.Removed != "":
			out = append(out, Element{Name: e.Removed, Status: "REGULAR"})
		case e.Status == "ADDED":
		default:
			e.Status = "REGULAR"
			out = append(out, e)
		}
	}
	return out
}

// TimetableQuery selects what to fetch.
type TimetableQuery struct {
	ResourceType  string // STUDENT, CLASS, TEACHER, ROOM, SUBJECT
	ResourceID    int
	TimetableType string // MY_TIMETABLE or STANDARD
	Start, End    time.Time
}

// TimetableFilter is the response of /timetable/filter (available resources).
type TimetableFilter struct {
	ResourceType string    `json:"resourceType" yaml:"resourceType"`
	PreSelected  *Resource `json:"preSelected" yaml:"preSelected"`
	Classes      []struct {
		Class         Resource  `json:"class" yaml:"class"`
		ClassTeacher1 *Resource `json:"classTeacher1" yaml:"classTeacher1"`
		ClassTeacher2 *Resource `json:"classTeacher2" yaml:"classTeacher2"`
	} `json:"classes" yaml:"classes"`
	Students []Resource `json:"students" yaml:"students"`
	Teachers []Resource `json:"teachers" yaml:"teachers"`
	Rooms    []Resource `json:"rooms" yaml:"rooms"`
	Subjects []Resource `json:"subjects" yaml:"subjects"`
}

// All returns every resource of the filter as a flat list.
func (f *TimetableFilter) All() []Resource {
	var out []Resource
	for _, c := range f.Classes {
		out = append(out, c.Class)
	}
	out = append(out, f.Students...)
	out = append(out, f.Teachers...)
	out = append(out, f.Rooms...)
	out = append(out, f.Subjects...)
	return out
}

// TimetableFilter lists the resources available for a resource type.
func (c *Client) TimetableFilter(ctx context.Context, resourceType, timetableType string, start, end time.Time) (*TimetableFilter, error) {
	q := url.Values{
		"resourceType":  {resourceType},
		"timetableType": {timetableType},
		"start":         {dates.ISO(start)},
		"end":           {dates.ISO(end)},
	}
	var f TimetableFilter
	if err := c.GetJSON(ctx, "/WebUntis/api/rest/view/v1/timetable/filter", q, 6*time.Hour, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

// ResolveResource finds a resource of the given type by id/name; empty
// selector returns the preselected resource (e.g. the student's class).
func (c *Client) ResolveResource(ctx context.Context, resourceType, selector string, start, end time.Time) (Resource, error) {
	f, err := c.TimetableFilter(ctx, resourceType, "STANDARD", start, end)
	if err != nil {
		return Resource{}, err
	}
	all := f.All()
	if selector == "" {
		if f.PreSelected != nil {
			return *f.PreSelected, nil
		}
		if len(all) > 0 {
			return all[0], nil
		}
		return Resource{}, fmt.Errorf("no %s timetable available for this account", strings.ToLower(resourceType))
	}
	id, idErr := strconv.Atoi(selector)
	sel := strings.ToLower(selector)
	for _, r := range all {
		if (idErr == nil && r.ID == id) || strings.EqualFold(r.ShortName, selector) || strings.EqualFold(r.DisplayName, selector) {
			return r, nil
		}
	}
	var partial []Resource
	for _, r := range all {
		if strings.Contains(strings.ToLower(r.ShortName+" "+r.LongName+" "+r.DisplayName), sel) {
			partial = append(partial, r)
		}
	}
	if len(partial) == 1 {
		return partial[0], nil
	}
	var avail []string
	for _, r := range all {
		avail = append(avail, r.Label())
	}
	if len(partial) > 1 {
		return Resource{}, fmt.Errorf("%q is ambiguous", selector)
	}
	return Resource{}, fmt.Errorf("no %s matches %q (available: %s)", strings.ToLower(resourceType), selector, strings.Join(avail, ", "))
}

// Timetable fetches and normalizes timetable entries.
func (c *Client) Timetable(ctx context.Context, tq TimetableQuery) (*Timetable, error) {
	if tq.TimetableType == "" {
		tq.TimetableType = "STANDARD"
	}
	q := url.Values{
		"start":         {dates.ISO(tq.Start)},
		"end":           {dates.ISO(tq.End)},
		"format":        {"1"},
		"resourceType":  {tq.ResourceType},
		"resources":     {strconv.Itoa(tq.ResourceID)},
		"periodTypes":   {""},
		"timetableType": {tq.TimetableType},
		"layout":        {"START_TIME"},
	}
	var r struct {
		Format int     `json:"format"`
		Days   []ttDay `json:"days"`
		Errors []any   `json:"errors"`
	}
	ttl := 5 * time.Minute
	if tq.End.Before(dates.Today()) {
		ttl = 12 * time.Hour
	}
	if err := c.GetJSON(ctx, "/WebUntis/api/rest/view/v1/timetable/entries", q, ttl, &r); err != nil {
		return nil, err
	}
	tt := &Timetable{ResourceType: tq.ResourceType, Start: tq.Start, End: tq.End}
	if ad, err := c.AppData(ctx); err == nil && ad.CurrentSchoolYear.TimeGrid != nil {
		tt.TimeGrid = ad.CurrentSchoolYear.TimeGrid.Units
	}
	for _, d := range r.Days {
		day, _ := dates.ParseLocalDateTime(d.Date)
		td := TimetableDay{Date: day, Status: d.Status, Resource: d.Resource}
		if tt.Resource.ID == 0 {
			tt.Resource = d.Resource
		}
		for _, e := range d.DayEntries {
			l := normalizeEntry(e)
			l.AllDay = true
			td.Lessons = append(td.Lessons, l)
		}
		for _, e := range d.GridEntries {
			td.Lessons = append(td.Lessons, normalizeEntry(e))
		}
		for _, e := range d.BackEntries {
			td.Lessons = append(td.Lessons, normalizeEntry(e))
		}
		sort.SliceStable(td.Lessons, func(i, j int) bool {
			if td.Lessons[i].AllDay != td.Lessons[j].AllDay {
				return td.Lessons[i].AllDay
			}
			return td.Lessons[i].Start.Before(td.Lessons[j].Start)
		})
		tt.Days = append(tt.Days, td)
	}
	return tt, nil
}

func normalizeEntry(e ttEntry) Lesson {
	l := Lesson{
		IDs:              e.IDs,
		Start:            e.Duration.StartTime(),
		End:              e.Duration.EndTime(),
		Type:             e.Type,
		Status:           e.Status,
		StatusDetail:     e.StatusDetail,
		Name:             e.Name,
		LessonText:       e.LessonText,
		LessonInfo:       e.LessonInfo,
		SubstitutionText: e.SubstitutionText,
		Notes:            e.NotesAll,
		Color:            e.Color,
		Icons:            e.Icons,
	}
	if e.Moved != nil {
		t := e.Moved.StartTime()
		if !t.IsZero() {
			l.MovedFrom = &t
		}
	}
	for _, t := range e.Texts {
		if strings.TrimSpace(t.Text) != "" {
			l.Texts = append(l.Texts, t.Text)
		}
	}
	for _, pos := range [][]ttPosition{e.Position1, e.Position2, e.Position3, e.Position4, e.Position5, e.Position6, e.Position7} {
		for _, p := range pos {
			typ := ""
			el := Element{}
			if p.Current != nil {
				typ = p.Current.Type
				el.Name = firstNonEmpty(p.Current.DisplayName, p.Current.ShortName, p.Current.LongName)
				el.LongName = p.Current.LongName
				el.Status = p.Current.Status
			}
			if p.Removed != nil {
				if typ == "" {
					typ = p.Removed.Type
					el.Status = "REMOVED"
				}
				el.Removed = firstNonEmpty(p.Removed.DisplayName, p.Removed.ShortName, p.Removed.LongName)
			}
			switch typ {
			case "SUBJECT":
				l.Subjects = append(l.Subjects, el)
			case "TEACHER":
				l.Teachers = append(l.Teachers, el)
			case "ROOM":
				l.Rooms = append(l.Rooms, el)
			case "CLASS":
				l.Classes = append(l.Classes, el)
			case "":
			default:
				l.Others = append(l.Others, el)
			}
		}
	}
	return l
}

func firstNonEmpty(s ...string) string {
	for _, x := range s {
		if strings.TrimSpace(x) != "" {
			return x
		}
	}
	return ""
}
