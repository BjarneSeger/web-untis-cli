package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dgrieser/web-untis-cli/internal/webuntis"
)

func TestTimetablePDFEngine(t *testing.T) {
	// A configured but missing browser: FindBrowser fails even on machines
	// with Chrome installed (e.g. CI runners).
	t.Setenv("WEBUNTIS_BROWSER", filepath.Join(t.TempDir(), "no-such-browser"))
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
