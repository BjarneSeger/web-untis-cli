package render

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ErrNoBrowser is returned when no Chrome/Chromium-based browser was found.
var ErrNoBrowser = errors.New("no Chrome, Chromium or Edge found; install one, set $WEBUNTIS_BROWSER to its executable or use --pdf-engine native")

// FindBrowser returns the path of a Chrome/Chromium-based browser that can
// print to PDF headlessly. $WEBUNTIS_BROWSER and $CHROME_PATH take precedence.
func FindBrowser() (string, error) {
	for _, env := range []string{"WEBUNTIS_BROWSER", "CHROME_PATH"} {
		if p := os.Getenv(env); p != "" {
			if lp, err := exec.LookPath(p); err == nil {
				return lp, nil
			}
			return "", fmt.Errorf("$%s: %q not found", env, p)
		}
	}
	for _, name := range []string{"chromium", "chromium-browser", "google-chrome", "google-chrome-stable", "chrome", "microsoft-edge", "microsoft-edge-stable", "msedge", "brave-browser", "brave"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	var candidates []string
	switch runtime.GOOS {
	case "darwin":
		for _, app := range []string{"Google Chrome", "Chromium", "Microsoft Edge", "Brave Browser"} {
			candidates = append(candidates, "/Applications/"+app+".app/Contents/MacOS/"+app)
		}
	case "windows":
		for _, base := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LocalAppData")} {
			if base != "" {
				candidates = append(candidates,
					filepath.Join(base, `Google\Chrome\Application\chrome.exe`),
					filepath.Join(base, `Microsoft\Edge\Application\msedge.exe`))
			}
		}
	default:
		candidates = append(candidates, "/snap/bin/chromium", "/opt/google/chrome/chrome")
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c, nil
		}
	}
	return "", ErrNoBrowser
}

// HTMLToPDF prints a standalone HTML page to PDF with a headless
// Chrome/Chromium. Page size and orientation come from the page's @page CSS.
func HTMLToPDF(ctx context.Context, html string) ([]byte, error) {
	browser, err := FindBrowser()
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "webuntis-pdf-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	src := filepath.Join(dir, "page.html")
	if err := os.WriteFile(src, []byte(html), 0o600); err != nil {
		return nil, err
	}
	pdf := filepath.Join(dir, "page.pdf")
	args := []string{
		"--headless",
		"--disable-gpu",
		"--disable-extensions",
		"--no-first-run",
		"--no-default-browser-check",
		"--hide-scrollbars",
		"--user-data-dir=" + filepath.Join(dir, "profile"),
		"--no-pdf-header-footer",
		"--print-to-pdf-no-header",
		"--print-to-pdf=" + pdf,
	}
	if runtime.GOOS == "linux" && os.Geteuid() == 0 {
		// Chrome refuses to start its sandbox as root (e.g. in containers).
		args = append(args, "--no-sandbox")
	}
	path := filepath.ToSlash(src)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path // C:/… on Windows
	}
	args = append(args, (&url.URL{Scheme: "file", Path: path}).String())
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, browser, args...)
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	data, err := os.ReadFile(pdf)
	if err != nil || len(data) == 0 {
		msg := strings.TrimSpace(stderr.String())
		if runErr != nil {
			return nil, fmt.Errorf("%s: %w\n%s", filepath.Base(browser), runErr, msg)
		}
		return nil, fmt.Errorf("%s did not produce a PDF\n%s", filepath.Base(browser), msg)
	}
	return data, nil
}
