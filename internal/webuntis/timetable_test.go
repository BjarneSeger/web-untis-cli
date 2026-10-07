package webuntis

import (
	"testing"
	"time"
)

func TestWithoutChanges(t *testing.T) {
	mon := time.Date(2026, 9, 21, 0, 0, 0, 0, time.Local)
	h := func(hh, mm int) time.Time { return mon.Add(time.Duration(hh)*time.Hour + time.Duration(mm)*time.Minute) }
	moved := h(12, 20)
	tt := &Timetable{Days: []TimetableDay{{Date: mon, Lessons: []Lesson{
		{AllDay: true, Type: "EVENT", Name: "Wandertag"},
		// substitute teacher and room change
		{Start: h(7, 45), End: h(8, 30), Status: "CHANGED", Subjects: []Element{{Name: "D", LongName: "Deutsch"}},
			Teachers: []Element{{Name: "VER", Status: "CHANGED", Removed: "LER"}}, Rooms: []Element{{Name: "A1", Removed: "D104"}}, SubstitutionText: "Vertretung"},
		// cancelled lesson, replaced by a lesson moved in from 12:20
		{Start: h(8, 35), End: h(9, 20), Status: "CANCELLED", Subjects: []Element{{Name: "M"}}, Teachers: []Element{{Name: "", Removed: "KRA"}}},
		{Start: h(8, 35), End: h(9, 20), Status: "ADDITIONAL", StatusDetail: "MOVED", MovedFrom: &moved, Subjects: []Element{{Name: "E"}}},
		// exam written in a regular lesson / exam next to its regular lesson
		{Start: h(9, 40), End: h(10, 25), Type: "EXAM", Status: "REGULAR", Subjects: []Element{{Name: "BI"}}},
		{Start: h(10, 30), End: h(11, 15), Status: "REGULAR", Subjects: []Element{{Name: "GE"}}, Teachers: []Element{{Name: "SAL"}, {Name: "EXT", Status: "ADDED"}}},
		{Start: h(10, 30), End: h(11, 15), Type: "EXAM", Subjects: []Element{{Name: "GE"}}},
	}}}}
	r := tt.WithoutChanges()
	if !r.Regular || tt.Regular {
		t.Fatal("Regular flag")
	}
	got := r.Days[0].Lessons
	want := []string{"D VER→LER D104", "M KRA", "BI", "GE SAL"}
	if len(got) != len(want) {
		t.Fatalf("got %d lessons: %+v", len(got), got)
	}
	for i, l := range got {
		if l.Changed() || l.Cancelled() || l.Info() != "" {
			t.Errorf("%d: still marked as change: %+v", i, l)
		}
	}
	if got[0].TeacherLabel() != "LER" || got[0].RoomLabel() != "D104" || got[0].SubjectLong() != "Deutsch" {
		t.Errorf("substitution not reverted: %+v", got[0])
	}
	if got[1].SubjectLabel() != "M" || got[1].TeacherLabel() != "KRA" {
		t.Errorf("cancelled lesson not restored: %+v", got[1])
	}
	if got[2].SubjectLabel() != "BI" || got[2].exam() {
		t.Errorf("exam in a free slot should become the lesson: %+v", got[2])
	}
	if got[3].TeacherLabel() != "SAL" {
		t.Errorf("added teacher not removed: %+v", got[3])
	}
	if len(tt.Days[0].Lessons) != 7 {
		t.Error("original timetable modified")
	}
}
