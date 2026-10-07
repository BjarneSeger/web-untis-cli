package views

import (
	"bytes"
	_ "embed"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-pdf/fpdf"

	"github.com/dgrieser/web-untis-cli/internal/webuntis"
)

// Inter (SIL Open Font License, see fonts/OFL.txt) keeps the PDF looking
// like the HTML page without depending on fonts installed on the system.
var (
	//go:embed fonts/Inter-Regular.ttf
	interRegular []byte
	//go:embed fonts/Inter-Bold.ttf
	interBold []byte
)

const (
	ptMM    = 25.4 / 72 // one point in mm
	ascent  = 0.96875   // Inter ascender / descender (em)
	descent = 0.2421875
)

type rgb struct{ r, g, b int }

func hexRGB(s string) rgb {
	v, _ := strconv.ParseUint(strings.TrimPrefix(s, "#"), 16, 32)
	return rgb{int(v >> 16 & 0xff), int(v >> 8 & 0xff), int(v & 0xff)}
}

// Same palette as the CSS variables of the HTML page.
var (
	cInk       = hexRGB("#1c2230")
	cMuted     = hexRGB("#667085")
	cLine      = hexRGB("#e4e7ec")
	cHead      = hexRGB("#f8f9fb")
	cToday     = hexRGB("#eef4ff")
	cTodayInk  = hexRGB("#2f5bd3")
	cChanged   = hexRGB("#d97706")
	cChangedBG = hexRGB("#fff7e6")
	cCancelled = hexRGB("#d92d20")
	cCancelBG  = hexRGB("#f7f7f8")
	cHatch     = hexRGB("#eceef1")
	cExam      = hexRGB("#7a3ee8")
	cEvent     = hexRGB("#0e9384")
	cMeta      = hexRGB("#3d4556")
	cInfo      = hexRGB("#475467")
	cWhite     = rgb{255, 255, 255}
)

type pdfDoc struct {
	*fpdf.Fpdf
	w, h, m float64 // page size and margin
}

func (d *pdfDoc) fill(c rgb)  { d.SetFillColor(c.r, c.g, c.b) }
func (d *pdfDoc) draw(c rgb)  { d.SetDrawColor(c.r, c.g, c.b) }
func (d *pdfDoc) color(c rgb) { d.SetTextColor(c.r, c.g, c.b) }

func (d *pdfDoc) font(bold bool, pt float64) {
	style := ""
	if bold {
		style = "B"
	}
	d.SetFont("inter", style, pt)
}

func (d *pdfDoc) width(s string, bold bool, pt float64) float64 {
	d.font(bold, pt)
	return d.GetStringWidth(s)
}

// lineH is the height of a text line of pt points with CSS line-height lh.
func lineH(pt, lh float64) float64 { return pt * ptMM * lh }

// baseline returns the baseline of a line box starting at top (CSS model).
func baseline(top, pt, lh float64) float64 {
	em := pt * ptMM
	return top + (em*lh-em*(ascent+descent))/2 + em*ascent
}

// text draws s with its line box starting at top and returns the width.
func (d *pdfDoc) text(x, top float64, s string, bold bool, pt, lh float64, c rgb) float64 {
	d.font(bold, pt)
	d.color(c)
	d.Text(x, baseline(top, pt, lh), s)
	return d.GetStringWidth(s)
}

// strike draws a line-through for text drawn with text().
func (d *pdfDoc) strike(x, top, w, pt, lh float64, c rgb, thick float64) {
	y := baseline(top, pt, lh) - pt*ptMM*0.3
	d.draw(c)
	d.SetLineWidth(thick)
	d.Line(x, y, x+w, y)
}

// wrap breaks s into lines of at most width w (the first line may be
// narrower: first). Words longer than a line are split with a hyphen. With
// more than max lines, the last one ends with an ellipsis.
func (d *pdfDoc) wrap(s string, w, first float64, max int, bold bool, pt float64) []string {
	d.font(bold, pt)
	lineW := func(i int) float64 {
		if i == 0 {
			return first
		}
		return w
	}
	var lines []string
	cur := ""
	for word := range strings.FieldsSeq(s) {
		cand := strings.TrimLeft(cur+" "+word, " ")
		if d.GetStringWidth(cand) <= lineW(len(lines)) {
			cur = cand
			continue
		}
		if cur != "" {
			lines = append(lines, cur)
		}
		for d.GetStringWidth(word) > lineW(len(lines)) {
			r := []rune(word)
			n := len(r) - 1
			if len(r) >= 6 {
				n = len(r) - 3 // keep at least 3 letters for the next line
			}
			for n > 1 && d.GetStringWidth(string(r[:n])+"-") > lineW(len(lines)) {
				n--
			}
			if n <= 1 {
				break
			}
			lines = append(lines, string(r[:n])+"-")
			word = string(r[n:])
		}
		cur = word
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	if max > 0 && len(lines) > max {
		lines = lines[:max]
		lines[max-1] = d.ellipsize(lines[max-1]+" …", lineW(max-1), bold, pt)
	}
	return lines
}

// ellipsize shortens s to fit w, ending with "…".
func (d *pdfDoc) ellipsize(s string, w float64, bold bool, pt float64) string {
	d.font(bold, pt)
	if d.GetStringWidth(s) <= w {
		return s
	}
	r := []rune(strings.TrimSuffix(s, " …"))
	for len(r) > 0 && d.GetStringWidth(strings.TrimRight(string(r), " ·,")+"…") > w {
		r = r[:len(r)-1]
	}
	return strings.TrimRight(string(r), " ·,") + "…"
}

// span is styled inline text; an atom is a group of spans that is never
// split across lines (one teacher, one room, a separator).
type span struct {
	text   string
	bold   bool
	strike bool
	c      rgb
}

type atom struct {
	spans []span
	sep   bool // dropped at the start of a line
}

func elementAtoms(el []webuntis.Element, bold bool, c rgb, muted bool) []atom {
	var out []atom
	for _, e := range el {
		var a atom
		switch {
		case e.Name == "" && e.Removed != "":
			a.spans = []span{{text: e.Removed, strike: true, c: cMuted}}
		case e.Name == "":
			continue
		case e.Removed != "" && e.Removed != e.Name:
			nc := cChanged
			if muted {
				nc = cMuted
			}
			a.spans = []span{{text: e.Name, bold: true, c: nc}, {text: " ", c: c}, {text: e.Removed, strike: true, c: cMuted}}
		default:
			a.spans = []span{{text: e.Name, bold: bold, c: c}}
		}
		if len(out) > 0 {
			out = append(out, atom{spans: []span{{text: ", ", c: c}}, sep: true})
		}
		out = append(out, a)
	}
	return out
}

func (d *pdfDoc) atomW(a atom, pt float64) float64 {
	w := 0.0
	for _, s := range a.spans {
		w += d.width(s.text, s.bold, pt)
	}
	return w
}

// layout distributes atoms over lines of width w (at most max lines).
func (d *pdfDoc) layout(atoms []atom, w float64, max int, pt float64) [][]atom {
	var lines [][]atom
	var cur []atom
	curW := 0.0
	for _, a := range atoms {
		aw := d.atomW(a, pt)
		if len(cur) > 0 && curW+aw > w && !a.sep {
			for len(cur) > 0 && cur[len(cur)-1].sep {
				cur = cur[:len(cur)-1]
			}
			lines = append(lines, cur)
			cur, curW = nil, 0
		}
		if len(cur) == 0 && a.sep {
			continue
		}
		cur = append(cur, a)
		curW += aw
	}
	if len(cur) > 0 {
		lines = append(lines, cur)
	}
	if max > 0 && len(lines) > max {
		lines = lines[:max]
		last := lines[max-1]
		lines[max-1] = append(slices.Clip(last), atom{spans: []span{{text: " …", c: cMuted}}})
	}
	return lines
}

// drawAtoms draws one line of atoms, clipping at width w.
func (d *pdfDoc) drawAtoms(x, top, w float64, line []atom, pt, lh float64) {
	end := x + w
	for _, a := range line {
		for _, s := range a.spans {
			t := s.text
			if sw := d.width(t, s.bold, pt); x+sw > end {
				t = d.ellipsize(t, end-x, s.bold, pt)
			}
			sw := d.text(x, top, t, s.bold, pt, lh, s.c)
			if s.strike {
				d.strike(x, top, sw, pt, lh, cMuted, 0.15)
			}
			x += sw
			if x >= end {
				return
			}
		}
	}
}

func badgeColor(kind string) rgb {
	switch kind {
	case "cancelled":
		return cCancelled
	case "exam":
		return cExam
	case "event":
		return cEvent
	}
	return cChanged
}

// badge draws a small uppercase label; (x, y) is its top-right corner when
// right is set, else its top-left corner.
func (d *pdfDoc) badge(x, y float64, label, kind string, pt float64, right bool) {
	w, h := d.badgeSize(label, pt)
	label = strings.ToUpper(label)
	if right {
		x -= w
	}
	d.fill(badgeColor(kind))
	d.RoundedRect(x, y, w, h, 0.8, "1234", "F")
	d.text(x+1.0, y, label, true, pt, 1.6, cWhite)
}

func (d *pdfDoc) badgeSize(label string, pt float64) (float64, float64) {
	return d.width(strings.ToUpper(label), true, pt) + 2*1.0, lineH(pt, 1.6)
}

// header draws title, range and timestamp; it returns the y below it.
func (d *pdfDoc) header(p htmlPage, titlePt, subPt, stampPt float64) float64 {
	x, y, right := d.m, d.m, d.w-d.m
	d.text(x, y, p.Title, true, titlePt, 1.25, cInk)
	y += lineH(titlePt, 1.25) + 0.5
	d.text(x, y, p.Subtitle, false, subPt, 1.35, cMuted)
	sw := d.width(p.Generated, false, stampPt)
	d.text(right-sw, y+lineH(subPt, 1.35)-lineH(stampPt, 1.35), p.Generated, false, stampPt, 1.35, cMuted)
	y += lineH(subPt, 1.35) + 2.5
	d.fill(cInk)
	d.Rect(x, y, right-x, 0.55, "F")
	return y + 0.55 + 3
}

// legend draws the color key at top and returns its height.
func (d *pdfDoc) legend(top float64, pt float64, keys []string) float64 {
	x := d.m
	sw, sh := 4.8, 2.9
	h := lineH(pt, 1.4)
	sy := top + (h-sh)/2
	for _, k := range keys {
		switch k {
		case "Änderung":
			d.fill(cChangedBG)
			d.draw(cChanged)
			d.SetLineWidth(0.35)
			d.RoundedRect(x, sy, sw, sh, 0.7, "1234", "FD")
		case "Entfall":
			d.fill(cCancelBG)
			d.RoundedRect(x, sy, sw, sh, 0.7, "1234", "F")
			d.hatch(x, sy, sw, sh, 0.9)
			d.fill(cCancelled)
			d.Rect(x, sy, 0.8, sh, "F")
		case "Prüfung":
			d.fill(cWhite)
			d.draw(cExam)
			d.SetLineWidth(0.35)
			d.RoundedRect(x, sy, sw, sh, 0.7, "1234", "FD")
		case "Heute":
			d.fill(cToday)
			d.RoundedRect(x, sy, sw, sh, 0.7, "1234", "F")
			d.fill(cTodayInk)
			d.Rect(x+0.3, sy, sw-0.6, 0.5, "F")
		}
		x += sw + 1.6
		x += d.text(x, top, k, false, pt, 1.4, cMuted) + 4.8
	}
	bw := d.width("webuntis-cli", false, pt)
	d.text(d.w-d.m-bw, top, "webuntis-cli", false, pt, 1.4, cMuted)
	return h
}

// hatch draws diagonal stripes into a rectangle (cancelled lessons).
func (d *pdfDoc) hatch(x, y, w, h, step float64) {
	d.ClipRect(x, y, w, h, false)
	d.draw(cHatch)
	d.SetLineWidth(step * 0.5)
	for o := -h; o < w+h; o += step {
		d.Line(x+o, y+h, x+o+h, y)
	}
	d.ClipEnd()
}

// lessonCard draws a lesson of the week grid.
func (d *pdfDoc) lessonCard(x, y, w, h float64, l htmlLesson, base float64, multi bool) {
	const r = 1.2
	bg, accent := hexRGB(l.BG), hexRGB(l.Accent)
	switch l.Kind {
	case "changed":
		bg, accent = cChangedBG, cChanged
	case "cancelled":
		bg, accent = cCancelBG, cCancelled
	}
	d.fill(accent)
	d.RoundedRect(x, y, w, h, r, "1234", "F")
	d.fill(bg)
	d.RoundedRectExt(x+1, y, w-1, h, 0, r, r, 0, "F")
	switch l.Kind {
	case "cancelled":
		d.hatch(x+1, y, w-1, h, 3.2)
	case "changed", "exam":
		d.draw(map[bool]rgb{true: cChanged, false: cExam}[l.Kind == "changed"])
		d.SetLineWidth(0.4)
		d.RoundedRect(x+0.2, y+0.2, w-0.4, h-0.4, r-0.2, "1234", "D")
	}

	padL, padR := 3.0, 1.6
	if multi {
		base *= 0.86
		padL, padR = 2.85, 1.3
	}
	tx, tw := x+padL, w-padL-padR
	top, bottom := y+0.8, y+h-0.8
	cancelled := l.Kind == "cancelled"
	room := func(pt, lh float64) int { return int(math.Floor((bottom - top + 0.01) / lineH(pt, lh))) }

	subjPt := base * 1.12
	ink := cInk
	if cancelled {
		ink = cMuted
	}
	clamp := 2
	if multi {
		clamp = 3
	}
	metaPt := base * 0.9
	reserve := 0.0 // keep room for the room/teacher line
	if len(l.Rooms)+len(l.Teachers) > 0 {
		reserve = lineH(metaPt, 1.35) + metaPt*ptMM*0.15
	}
	subjLines := int(math.Floor((bottom - top - reserve + 0.01) / lineH(subjPt, 1.15)))
	for _, line := range d.wrap(l.Name, tw, tw, max(1, min(clamp, subjLines)), true, subjPt) {
		lw := d.text(tx, top, line, true, subjPt, 1.15, ink)
		if cancelled {
			d.strike(tx, top, lw, subjPt, 1.15, cCancelled, 0.5)
		}
		top += lineH(subjPt, 1.15)
	}

	top += metaPt * ptMM * 0.15
	roomC, teachC := cInk, cMeta
	if cancelled {
		roomC, teachC = cMuted, cMuted
	}
	atoms := elementAtoms(l.Rooms, true, roomC, cancelled)
	if t := elementAtoms(l.Teachers, false, teachC, cancelled); len(t) > 0 {
		if len(atoms) > 0 {
			atoms = append(atoms, atom{spans: []span{{text: " · ", c: teachC}}, sep: true})
		}
		atoms = append(atoms, t...)
	}
	if n := min(2, room(metaPt, 1.35)); n > 0 {
		for _, line := range d.layout(atoms, tw, n, metaPt) {
			d.drawAtoms(tx, top, tw, line, metaPt, 1.35)
			top += lineH(metaPt, 1.35)
		}
	}

	if l.Info != "" {
		infoPt := base * 0.78
		top += infoPt * ptMM * 0.2
		if n := min(2, room(infoPt, 1.35)); n > 0 {
			for _, line := range d.wrap(l.Info, tw, tw, n, false, infoPt) {
				d.text(tx, top, line, false, infoPt, 1.35, cInfo)
				top += lineH(infoPt, 1.35)
			}
		}
	}
	if l.Badge != "" {
		pt := base * 0.68
		d.badge(x+w-1, y+h-1-lineH(pt, 1.6), l.Badge, l.Kind, pt, true)
	}
}

// TimetablePDF renders the timetable as PDF without a browser: the week
// grid on one A4 landscape page, the list view on A4 portrait pages. The
// layout follows the HTML page of TimetableHTML.
func TimetablePDF(tt *webuntis.Timetable, name string, grid bool) ([]byte, error) {
	p := buildPage(tt, name, grid)
	orient, w, h, m := "P", 210.0, 297.0, 12.0
	if grid {
		orient, w, h, m = "L", 297.0, 210.0, 8.0
	}
	f := fpdf.New(orient, "mm", "A4", "")
	f.AddUTF8FontFromBytes("inter", "", interRegular)
	f.AddUTF8FontFromBytes("inter", "B", interBold)
	f.SetTitle(p.Title+" · "+p.Subtitle, true)
	f.SetCreator("webuntis-cli", true)
	f.SetCreationDate(time.Now())
	f.SetMargins(m, m, m)
	f.SetAutoPageBreak(false, m)
	f.AddPage()
	d := &pdfDoc{Fpdf: f, w: w, h: h, m: m}
	if grid {
		d.gridPage(p)
	} else {
		d.listPages(p)
	}
	var b bytes.Buffer
	if err := f.Output(&b); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func (d *pdfDoc) gridPage(p htmlPage) {
	top := d.header(p, 15, 10, 7.5)
	if p.Empty {
		d.text(d.m, top, "Keine Einträge.", false, 10, 1.4, cMuted)
		return
	}
	g := p.Grid
	legendPt := 7.5
	legendTop := d.h - d.m - lineH(legendPt, 1.4)
	d.legend(legendTop, legendPt, []string{"Änderung", "Entfall", "Prüfung", "Heute"})

	base := 13 * printScale(len(g.Slots))
	x0, y0 := d.m, top
	gw, gh := d.w-2*d.m, legendTop-2-top
	timeW := 4.6 * base * ptMM
	colW := (gw - timeW) / float64(len(g.Cols))

	chipPt := base * 0.8
	chipH := lineH(chipPt, 1.35) + 0.5*2
	chips, status := 0, false
	for _, c := range g.Cols {
		chips = max(chips, len(c.AllDay))
		status = status || c.Status != ""
	}
	headH := 1.5*2 + lineH(base, 1.35) + float64(chips)*(chipH+1)
	if status {
		headH += lineH(base*0.8, 1.35)
	}
	rowH := (gh - headH) / float64(len(g.Slots))
	colX := func(col int) float64 { return x0 + timeW + float64(col-2)*colW }
	rowY := func(row int) float64 { return y0 + headH + float64(row-2)*rowH }

	// background, clipped to the rounded frame
	const radius = 2.6
	d.ClipRoundedRect(x0, y0, gw, gh, radius, false)
	d.fill(cWhite)
	d.Rect(x0, y0, gw, gh, "F")
	d.fill(cHead)
	d.Rect(x0, y0, gw, headH, "F")
	d.Rect(x0, y0, timeW, gh, "F")
	for i, c := range g.Cols {
		if c.Today {
			d.fill(cToday)
			d.Rect(colX(i+2), y0, colW, gh, "F")
			d.fill(cTodayInk)
			d.Rect(colX(i+2), y0, colW, 0.8, "F")
		}
	}
	d.draw(cLine)
	d.SetLineWidth(0.25)
	for i := range g.Slots {
		y := rowY(i + 2)
		d.Line(x0, y, x0+gw, y)
	}
	for i := range g.Cols {
		x := colX(i + 2)
		d.Line(x, y0, x, y0+gh)
	}
	d.ClipEnd()
	d.draw(cLine)
	d.SetLineWidth(0.3)
	d.RoundedRect(x0, y0, gw, gh, radius, "1234", "D")

	// day headers
	for i, c := range g.Cols {
		x, y := colX(i+2)+2.5, y0+1.5
		ink, muted := cInk, cMuted
		if c.Today {
			ink, muted = cTodayInk, cTodayInk
		}
		ww := d.text(x, y, c.Weekday, true, base, 1.35, ink)
		d.text(x+ww+1, y+lineH(base, 1.35)-lineH(base*0.88, 1.35)-0.2, c.Date, false, base*0.88, 1.35, muted)
		y += lineH(base, 1.35)
		if c.Status != "" {
			d.text(x, y, c.Status, false, base*0.8, 1.35, cMuted)
			y += lineH(base*0.8, 1.35)
		}
		cw := colW - 5
		for _, a := range c.AllDay {
			y += 1
			bg, accent := hexRGB(a.BG), hexRGB(a.Accent)
			d.fill(accent)
			d.RoundedRect(x, y, cw, chipH, 0.8, "1234", "F")
			d.fill(bg)
			d.RoundedRectExt(x+0.8, y, cw-0.8, chipH, 0, 0.8, 0.8, 0, "F")
			label := a.Name
			if a.Info != "" {
				label += " · " + a.Info
			}
			d.text(x+2.4, y+0.5, d.ellipsize(label, cw-3.4, false, chipPt), false, chipPt, 1.35, cInk)
			y += chipH
		}
	}

	// periods
	for _, s := range g.Slots {
		y := rowY(s.Row)
		label := s.Label
		if label == "" {
			label = "·"
		}
		tPt := base * 0.74
		total := lineH(base*1.05, 1.15) + 2*lineH(tPt, 1.15)
		ty := y + (rowH-total)/2
		cx := x0 + timeW/2
		for i, t := range []string{label, s.Start, s.End} {
			pt, bold, c := tPt, false, cMuted
			if i == 0 {
				pt, bold, c = base*1.05, true, cInk
			}
			tw := d.width(t, bold, pt)
			d.text(cx-tw/2, ty, t, bold, pt, 1.15, c)
			ty += lineH(pt, 1.15)
		}
	}

	// lessons
	const pad, gap = 0.6, 0.6
	for _, b := range g.Blocks {
		bx, by := colX(b.Col)+pad, rowY(b.RowStart)+pad
		bw, bh := colW-2*pad, float64(b.RowEnd-b.RowStart)*rowH-2*pad
		laneW := (bw - gap*float64(b.Lanes-1)) / float64(b.Lanes)
		irh := (bh - gap*float64(b.Rows-1)) / float64(b.Rows)
		for _, l := range b.Lessons {
			n := float64(l.RowEnd - l.Row)
			lx := bx + float64(l.Lane-1)*(laneW+gap)
			ly := by + float64(l.Row-1)*(irh+gap)
			d.lessonCard(lx, ly, laneW, n*irh+(n-1)*gap, l, base, b.Lanes > 1)
		}
	}
}

func (d *pdfDoc) listPages(p htmlPage) {
	y := d.header(p, 15, 10, 7.5)
	if p.Empty {
		d.text(d.m, y, "Keine Einträge.", false, 10, 1.4, cMuted)
		return
	}
	tw := d.w - 2*d.m
	fracs := []float64{0.15, 0.22, 0.18, 0.12, 0.33}
	heads := []string{"ZEIT", "FACH", "LEHRKRAFT", "RAUM", "HINWEIS"}
	const padX, padY = 2.4, 1.6
	const bodyPt, thPt, subjPt, smallPt, infoPt = 9.0, 7.5, 11.0, 8.0, 8.5
	h2Pt := 12.0
	cols := make([]float64, len(fracs))
	for i, f := range fracs {
		cols[i] = tw * f
	}
	colX := func(i int) float64 {
		x := d.m
		for _, c := range cols[:i] {
			x += c
		}
		return x
	}
	inner := func(i int) float64 { return cols[i] - 2*padX }
	thH := lineH(thPt, 1.35) + 2*padY

	type rowLayout struct {
		l             htmlLesson
		subj          []string
		teach, rooms  [][]atom
		badgeW, infoH float64
		info          []string
		h             float64
	}
	measure := func(l htmlLesson) rowLayout {
		r := rowLayout{l: l}
		r.subj = d.wrap(l.Name, inner(1), inner(1), 0, true, subjPt)
		hs := float64(len(r.subj)) * lineH(subjPt, 1.3)
		if l.SubjectLong != "" {
			hs += lineH(smallPt, 1.3)
		}
		r.teach = d.layout(elementAtoms(l.Teachers, false, cInk, false), inner(2), 0, bodyPt)
		r.rooms = d.layout(elementAtoms(l.Rooms, false, cInk, false), inner(3), 0, bodyPt)
		first := inner(4)
		if l.Badge != "" {
			r.badgeW, _ = d.badgeSize(l.Badge, bodyPt*0.68)
			first -= r.badgeW + 1.2
		}
		r.info = d.wrap(l.Info, inner(4), first, 0, false, infoPt)
		r.infoH = max(float64(len(r.info))*lineH(infoPt, 1.35), lineH(bodyPt, 1.35))
		r.h = 2*padY + max(hs, float64(max(1, len(r.teach), len(r.rooms)))*lineH(bodyPt, 1.35), r.infoH)
		return r
	}

	bottom := d.h - d.m
	for _, day := range p.Days {
		var rows []rowLayout
		total := lineH(h2Pt, 1.3) + 2
		if day.Note != "" {
			total += lineH(bodyPt, 1.4)
		} else {
			total += thH
			for _, l := range day.Lessons {
				r := measure(l)
				rows = append(rows, r)
				total += r.h
			}
		}
		if y+total > bottom && total < bottom-d.m {
			d.AddPage()
			y = d.m
		}
		ink := cInk
		if day.Today {
			ink = cTodayInk
		}
		tx := d.m + d.text(d.m, y, day.Title, true, h2Pt, 1.3, ink)
		if day.Today {
			pw := d.width("heute", true, 8) + 4
			d.fill(cToday)
			d.RoundedRect(tx+2, y+0.6, pw, lineH(8, 1.6), lineH(8, 1.6)/2, "1234", "F")
			d.text(tx+4, y+0.6, "heute", true, 8, 1.6, cTodayInk)
		}
		y += lineH(h2Pt, 1.3) + 2
		if day.Note != "" {
			d.text(d.m, y, day.Note, false, bodyPt, 1.4, cMuted)
			y += lineH(bodyPt, 1.4) + 5
			continue
		}

		tableTop := y
		drawFrame := func(from, to float64) {
			d.draw(cLine)
			d.SetLineWidth(0.3)
			d.RoundedRect(d.m, from, tw, to-from, 2.6, "1234", "D")
		}
		d.fill(cHead)
		d.RoundedRectExt(d.m, y, tw, thH, 2.6, 2.6, 0, 0, "F")
		for i, h := range heads {
			d.text(colX(i)+padX, y+padY, h, true, thPt, 1.35, cMuted)
		}
		y += thH
		for ri, r := range rows {
			if y+r.h > bottom {
				drawFrame(tableTop, y)
				d.AddPage()
				y, tableTop = d.m, d.m
			}
			l := r.l
			last := ri == len(rows)-1
			rad := 0.0
			if last {
				rad = 2.6
			}
			switch l.Kind {
			case "changed":
				d.fill(cChangedBG)
				d.RoundedRectExt(d.m, y, tw, r.h, 0, 0, rad, rad, "F")
			}
			// subject cell with accent bar
			bg, accent := hexRGB(l.BG), hexRGB(l.Accent)
			switch l.Kind {
			case "changed":
				bg, accent = cChangedBG, cChanged
			case "cancelled":
				bg, accent = hexRGB("#f4f4f5"), cCancelled
			}
			d.fill(bg)
			d.Rect(colX(1), y, cols[1], r.h, "F")
			d.fill(accent)
			d.Rect(colX(1), y, 1, r.h, "F")
			d.draw(cLine)
			d.SetLineWidth(0.25)
			d.Line(d.m, y, d.m+tw, y)

			muted := l.Kind == "cancelled"
			txt := cInk
			if muted {
				txt = cMuted
			}
			ty := y + padY
			d.text(colX(0)+padX, ty, l.Time, false, bodyPt, 1.35, cMuted)
			sy := ty
			for _, s := range r.subj {
				w := d.text(colX(1)+padX+1, sy, s, true, subjPt, 1.3, txt)
				if muted {
					d.strike(colX(1)+padX+1, sy, w, subjPt, 1.3, cCancelled, 0.4)
				}
				sy += lineH(subjPt, 1.3)
			}
			if l.SubjectLong != "" {
				d.text(colX(1)+padX+1, sy, l.Subject, false, smallPt, 1.3, cMuted)
			}
			for i, line := range r.teach {
				d.drawAtoms(colX(2)+padX, ty+float64(i)*lineH(bodyPt, 1.35), inner(2), recolor(line, txt), bodyPt, 1.35)
			}
			for i, line := range r.rooms {
				d.drawAtoms(colX(3)+padX, ty+float64(i)*lineH(bodyPt, 1.35), inner(3), recolor(line, txt), bodyPt, 1.35)
			}
			ix := colX(4) + padX
			if l.Badge != "" {
				bpt := bodyPt * 0.68
				d.badge(ix, ty+(lineH(infoPt, 1.35)-lineH(bpt, 1.6))/2, l.Badge, l.Kind, bpt, false)
			}
			for i, line := range r.info {
				x := ix
				if i == 0 && l.Badge != "" {
					x += r.badgeW + 1.2
				}
				d.text(x, ty+float64(i)*lineH(infoPt, 1.35), line, false, infoPt, 1.35, cInfo)
			}
			y += r.h
		}
		drawFrame(tableTop, y)
		y += 5
	}
	if y+lineH(7.5, 1.4) <= bottom {
		d.legend(y, 7.5, []string{"Änderung", "Entfall"})
	}
}

// recolor applies the row text color to plain (unchanged) spans.
func recolor(line []atom, c rgb) []atom {
	if c == cInk {
		return line
	}
	out := make([]atom, len(line))
	for i, a := range line {
		spans := make([]span, len(a.spans))
		for j, s := range a.spans {
			if s.c == cInk {
				s.c = c
			}
			spans[j] = s
		}
		out[i] = atom{spans: spans, sep: a.sep}
	}
	return out
}
