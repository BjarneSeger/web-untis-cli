// Package render turns data into terminal output: Markdown rendered with
// glamour (pretty), raw Markdown, JSON, YAML, iCalendar, HTML or PDF.
package render

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	ics "github.com/arran4/golang-ical"
	"github.com/charmbracelet/glamour"
	"github.com/goccy/go-yaml"
	"golang.org/x/term"
)

// Format is an output format.
type Format string

const (
	Pretty   Format = "pretty"
	Markdown Format = "markdown"
	JSON     Format = "json"
	YAML     Format = "yaml"
	ICS      Format = "ics"
	HTMLPage Format = "html"
	PDF      Format = "pdf"
)

// ParseFormat validates a --output value.
func ParseFormat(s string) (Format, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "pretty", "table", "text", "term":
		return Pretty, nil
	case "md", "markdown":
		return Markdown, nil
	case "json":
		return JSON, nil
	case "yaml", "yml":
		return YAML, nil
	case "ics", "ical", "icalendar":
		return ICS, nil
	case "html", "htm":
		return HTMLPage, nil
	case "pdf":
		return PDF, nil
	}
	return "", fmt.Errorf("unknown output format %q (pretty, markdown, json, yaml, ics, html, pdf)", s)
}

// Renderer writes output in the selected format.
type Renderer struct {
	Format Format
	Out    io.Writer
	// Style is a glamour style name (auto, dark, light, notty, dracula,
	// tokyo-night, pink, ascii) or a path to a JSON style.
	Style string
	Width int
}

// New creates a renderer writing to stdout.
func New(f Format, style string) *Renderer {
	r := &Renderer{Format: f, Out: os.Stdout, Style: style}
	r.Width = TermWidth()
	return r
}

// TermWidth returns the terminal width (default 100).
func TermWidth() int {
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 20 {
		return w
	}
	return 100
}

// IsTTY reports whether stdout is a terminal.
func IsTTY() bool { return term.IsTerminal(int(os.Stdout.Fd())) }

// Color reports whether colored output should be used.
func (r *Renderer) Color() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if r.Style == "notty" || r.Style == "ascii" {
		return false
	}
	return IsTTY()
}

// Markdown renders a Markdown document (pretty) or prints it (markdown).
func (r *Renderer) Markdown(md string) error {
	if r.Format == Markdown {
		_, err := io.WriteString(r.Out, strings.TrimRight(md, "\n")+"\n")
		return err
	}
	out, err := r.renderMarkdown(md)
	if err != nil {
		return err
	}
	_, err = io.WriteString(r.Out, out)
	return err
}

func (r *Renderer) renderMarkdown(md string) (string, error) {
	opts := []glamour.TermRendererOption{glamour.WithWordWrap(r.Width - 4), glamour.WithEmoji(), glamour.WithPreservedNewLines()}
	style := r.Style
	if style == "" {
		style = os.Getenv("GLAMOUR_STYLE")
	}
	switch {
	case !r.Color():
		opts = append(opts, glamour.WithStandardStyle("notty"))
	case style == "" || style == "auto":
		opts = append(opts, glamour.WithAutoStyle())
	case strings.HasSuffix(style, ".json"):
		opts = append(opts, glamour.WithStylePath(style))
	default:
		opts = append(opts, glamour.WithStandardStyle(style))
	}
	tr, err := glamour.NewTermRenderer(opts...)
	if err != nil {
		return "", err
	}
	return tr.Render(md)
}

// Raw writes preformatted text as is (used for the lipgloss timetable grid).
func (r *Renderer) Raw(s string) error {
	_, err := io.WriteString(r.Out, s)
	return err
}

// Data writes v as JSON or YAML.
func (r *Renderer) Data(v any) error {
	switch r.Format {
	case YAML:
		// Round-trip through JSON so that json tags / omitempty and custom
		// marshalers are respected consistently.
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.UseNumber()
		var generic any
		if err := dec.Decode(&generic); err != nil {
			return err
		}
		out, err := yaml.MarshalWithOptions(numbers(generic), yaml.Indent(2), yaml.IndentSequence(true), yaml.UseLiteralStyleIfMultiline(true))
		if err != nil {
			return err
		}
		_, err = r.Out.Write(out)
		return err
	default:
		enc := json.NewEncoder(r.Out)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
}

// Calendar writes an iCalendar document.
func (r *Renderer) Calendar(cal *ics.Calendar) error {
	return cal.SerializeTo(r.Out)
}

// IsData reports whether the format is a machine readable data format.
func (f Format) IsData() bool { return f == JSON || f == YAML }

// numbers converts json.Number values to int64/float64 so YAML does not
// print integers such as 20261002 in scientific notation.
func numbers(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			x[k] = numbers(e)
		}
	case []any:
		for i, e := range x {
			x[i] = numbers(e)
		}
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i
		}
		f, _ := x.Float64()
		return f
	}
	return v
}
