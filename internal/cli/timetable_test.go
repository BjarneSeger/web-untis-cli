package cli

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/dgrieser/web-untis-cli/internal/webuntis"
)

func TestTimetablePDFEngine(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no browser installed
	t.Setenv("WEBUNTIS_BROWSER", "")
	t.Setenv("CHROME_PATH", "")
	mon := time.Date(2026, 9, 21, 0, 0, 0, 0, time.Local)
	tt := &webuntis.Timetable{ResourceType: "CLASS", Resource: webuntis.Resource{ShortName: "6c"}, Start: mon, End: mon.AddDate(0, 0, 4),
		Days: []webuntis.TimetableDay{{Date: mon, Lessons: []webuntis.Lesson{{Start: mon.Add(8 * time.Hour), End: mon.Add(9 * time.Hour), Subjects: []webuntis.Element{{Name: "D"}}}}}}}
	for _, engine := range []string{"auto", "native"} {
		b, err := timetablePDF(context.Background(), tt, "", true, engine)
		if err != nil || !bytes.HasPrefix(b, []byte("%PDF-")) {
			t.Errorf("%s: %v", engine, err)
		}
	}
	if _, err := timetablePDF(context.Background(), tt, "", true, "browser"); err == nil {
		t.Error("browser engine without a browser should fail")
	}
	if _, err := timetablePDF(context.Background(), tt, "", true, "foo"); err == nil {
		t.Error("unknown engine should fail")
	}
}
