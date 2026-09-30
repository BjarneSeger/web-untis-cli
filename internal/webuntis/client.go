// Package webuntis is a read-only client for the (undocumented) APIs used by
// the WebUntis web UI plus the public JSON-RPC API.
//
// Authentication: a JSESSIONID is obtained via the JSON-RPC "authenticate"
// method (fallback: the web form login j_spring_security_check). Accounts
// that can only sign in via Microsoft/SSO use the Untis Mobile secret
// instead: getUserData2017 on /WebUntis/jsonrpc_intern.do with a TOTP
// (RFC 6238) derived from the key shown next to the QR code under
// "Zugriff über Untis Mobile"; it yields the same JSESSIONID. With that session cookie,
// /WebUntis/api/token/new returns a short lived JWT which is sent as Bearer
// token to the /WebUntis/api/rest/view/... endpoints. Older endpoints under
// /WebUntis/api/... only need the session cookie.
package webuntis

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/dgrieser/web-untis-cli/internal/cache"
	"github.com/dgrieser/web-untis-cli/internal/config"
)

const userAgent = "webuntis-cli/1.0 (+https://github.com/dgrieser/web-untis-cli)"

// Client talks to one WebUntis school.
type Client struct {
	Profile *config.Profile
	Cache   *cache.Cache
	Debug   bool

	http    *http.Client
	jar     *cookiejar.Jar
	session *config.Session
	mu      sync.Mutex

	appData  *AppData
	password func() (string, error)
	secret   func() (string, error)
}

// Options configure a Client.
type Options struct {
	NoCache bool // bypass cache reads
	Debug   bool
	// Password is called when a (re-)login is needed. Default: the
	// profile's Password field, then $WEBUNTIS_PASSWORD.
	Password func() (string, error)
	// Secret is the same for the Untis Mobile secret. Default: the
	// profile's Secret field, then $WEBUNTIS_SECRET.
	Secret func() (string, error)
}

// New creates a client for the given profile and restores its session.
func New(p *config.Profile, opts Options) (*Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	c := &Client{
		Profile:  p,
		Cache:    cache.New(p.Path("cache"), opts.NoCache),
		Debug:    opts.Debug || os.Getenv("WEBUNTIS_DEBUG") != "",
		jar:      jar,
		session:  p.LoadSession(),
		password: opts.Password,
		secret:   opts.Secret,
	}
	c.http = &http.Client{Jar: jar, Timeout: 60 * time.Second}
	c.restoreCookies()
	return c, nil
}

// HTTPClient returns the underlying cookie-aware HTTP client (used for SSO
// based platform applications).
func (c *Client) HTTPClient() *http.Client { return c.http }

func (c *Client) debugf(format string, args ...any) {
	if c.Debug {
		fmt.Fprintf(os.Stderr, "[debug] "+format+"\n", args...)
	}
}

func (c *Client) restoreCookies() {
	for host, cookies := range c.session.Cookies {
		u := &url.URL{Scheme: "https", Host: host, Path: "/"}
		var hc []*http.Cookie
		for _, ck := range cookies {
			path := ck.Path
			if path == "" {
				path = "/"
			}
			hc = append(hc, &http.Cookie{Name: ck.Name, Value: ck.Value, Path: path, Secure: true})
		}
		c.jar.SetCookies(u, hc)
	}
}

// TrackHost makes sure cookies of host are persisted with the session.
func (c *Client) TrackHost(host string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.session.Cookies[host]; !ok {
		c.session.Cookies[host] = nil
	}
}

// SaveSession persists cookies and token to disk.
func (c *Client) SaveSession() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	hosts := map[string]bool{c.Profile.Server: true}
	for h := range c.session.Cookies {
		hosts[h] = true
	}
	for h := range hosts {
		// Look at the most specific paths we use so that path-scoped cookies
		// (JSESSIONID is scoped to /WebUntis) are included.
		seen := map[string]bool{}
		var out []config.Cookie
		for _, p := range []string{"/WebUntis/", "/"} {
			for _, ck := range c.jar.Cookies(&url.URL{Scheme: "https", Host: h, Path: p}) {
				if seen[ck.Name] {
					continue
				}
				seen[ck.Name] = true
				path := "/"
				if p == "/WebUntis/" && ck.Name == "JSESSIONID" {
					path = "/WebUntis"
				}
				out = append(out, config.Cookie{Name: ck.Name, Value: ck.Value, Path: path})
			}
		}
		if len(out) > 0 {
			c.session.Cookies[h] = out
		}
	}
	return c.Profile.SaveSession(c.session)
}

func (c *Client) base() string { return c.Profile.BaseURL() }

func (c *Client) hasSessionCookie() bool { return c.sessionCookie() != "" }

// sessionCookie returns the current JSESSIONID value ("" if none).
func (c *Client) sessionCookie() string {
	for _, ck := range c.jar.Cookies(&url.URL{Scheme: "https", Host: c.Profile.Server, Path: "/WebUntis/"}) {
		if ck.Name == "JSESSIONID" && ck.Value != "" {
			return ck.Value
		}
	}
	return ""
}

func (c *Client) setSchoolCookies(sessionID string) {
	u := &url.URL{Scheme: "https", Host: c.Profile.Server, Path: "/WebUntis/"}
	cookies := []*http.Cookie{
		{Name: "schoolname", Value: "_" + base64.StdEncoding.EncodeToString([]byte(c.Profile.School)), Path: "/", Secure: true},
	}
	if c.Profile.TenantID != "" {
		cookies = append(cookies, &http.Cookie{Name: "Tenant-Id", Value: c.Profile.TenantID, Path: "/", Secure: true})
	}
	if sessionID != "" {
		cookies = append(cookies, &http.Cookie{Name: "JSESSIONID", Value: sessionID, Path: "/WebUntis", Secure: true})
	}
	c.jar.SetCookies(u, cookies)
}

// APIError is returned for non-2xx responses.
type APIError struct {
	Status int
	URL    string
	Body   string
}

func (e *APIError) Error() string {
	body := strings.TrimSpace(e.Body)
	if len(body) > 300 {
		body = body[:300] + "…"
	}
	return fmt.Sprintf("HTTP %d for %s: %s", e.Status, e.URL, body)
}

// ErrAuth signals that the session is not (or no longer) valid.
var ErrAuth = errors.New("authentication required")

// raw performs a request and returns the body. It does not handle re-auth.
func (c *Client) raw(ctx context.Context, method, rawURL string, body io.Reader, headers map[string]string) (*http.Response, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	start := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	c.debugf("%s %s -> %d (%d bytes, %s)", method, redactURL(rawURL), resp.StatusCode, len(b), time.Since(start).Round(time.Millisecond))
	return resp, b, err
}

func redactURL(s string) string {
	u, err := url.Parse(s)
	if err != nil {
		return s
	}
	q := u.Query()
	for k := range q {
		lk := strings.ToLower(k)
		if strings.Contains(lk, "token") || strings.Contains(lk, "code") || strings.Contains(lk, "password") || strings.Contains(lk, "sig") || lk == "key" {
			q.Set(k, "***")
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// looksLikeLogin detects HTML login pages returned instead of JSON.
func looksLikeLogin(resp *http.Response, body []byte) bool {
	ct := resp.Header.Get("Content-Type")
	if strings.Contains(ct, "json") {
		return false
	}
	if strings.Contains(ct, "html") {
		return true
	}
	if resp.Request != nil && strings.Contains(resp.Request.URL.Path, "login") {
		return true
	}
	return bytes.HasPrefix(bytes.TrimSpace(body), []byte("<"))
}

// Token returns a valid JWT, fetching a new one if necessary.
func (c *Client) Token(ctx context.Context) (string, error) {
	if c.session.Token != "" && time.Until(c.session.TokenExp) > time.Minute {
		return c.session.Token, nil
	}
	if !c.hasSessionCookie() {
		if err := c.Login(ctx); err != nil {
			return "", err
		}
	}
	tok, err := c.fetchToken(ctx)
	if errors.Is(err, ErrAuth) {
		if err := c.Login(ctx); err != nil {
			return "", err
		}
		tok, err = c.fetchToken(ctx)
	}
	return tok, err
}

func (c *Client) fetchToken(ctx context.Context) (string, error) {
	resp, b, err := c.raw(ctx, http.MethodGet, c.base()+"/WebUntis/api/token/new", nil, nil)
	if err != nil {
		return "", err
	}
	tok := strings.TrimSpace(string(b))
	if resp.StatusCode != http.StatusOK || strings.Count(tok, ".") != 2 || strings.ContainsAny(tok, " <") {
		return "", ErrAuth
	}
	c.session.Token = tok
	c.session.TokenExp = jwtExpiry(tok)
	return tok, nil
}

func jwtExpiry(tok string) time.Time {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return time.Now().Add(5 * time.Minute)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Now().Add(5 * time.Minute)
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if json.Unmarshal(payload, &claims) != nil || claims.Exp == 0 {
		return time.Now().Add(5 * time.Minute)
	}
	return time.Unix(claims.Exp, 0)
}

// Get performs an authenticated GET returning the raw body. ttl > 0 enables
// caching. path may be absolute ("/WebUntis/...") or a full URL.
func (c *Client) Get(ctx context.Context, path string, query url.Values, ttl time.Duration) ([]byte, error) {
	u := path
	if !strings.HasPrefix(u, "http") {
		u = c.base() + path
	}
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	key := c.Profile.Server + "|" + c.Profile.Username + "|" + u
	if ttl > 0 {
		if b, ok := c.Cache.Get(key); ok {
			c.debugf("cache hit %s", redactURL(u))
			return b, nil
		}
	}
	b, err := c.getAuthed(ctx, u, true)
	if err != nil {
		return nil, err
	}
	c.Cache.Put(key, b, ttl)
	return b, nil
}

func (c *Client) getAuthed(ctx context.Context, u string, retry bool) ([]byte, error) {
	tok, err := c.Token(ctx)
	if err != nil {
		return nil, err
	}
	resp, b, err := c.raw(ctx, http.MethodGet, u, nil, map[string]string{"Authorization": "Bearer " + tok})
	if err != nil {
		return nil, err
	}
	authFail := resp.StatusCode == http.StatusUnauthorized || (resp.StatusCode == http.StatusOK && looksLikeLogin(resp, b))
	if resp.StatusCode == http.StatusForbidden && strings.Contains(string(b), "token") {
		authFail = true
	}
	if authFail {
		if !retry {
			return nil, fmt.Errorf("%w: server rejected session for %s", ErrAuth, redactURL(u))
		}
		c.session.Token = ""
		if err := c.Login(ctx); err != nil {
			return nil, err
		}
		return c.getAuthed(ctx, u, false)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, &APIError{Status: resp.StatusCode, URL: redactURL(u), Body: string(b)}
	}
	return b, nil
}

// GetJSON is Get + json.Unmarshal.
func (c *Client) GetJSON(ctx context.Context, path string, query url.Values, ttl time.Duration, out any) error {
	b, err := c.Get(ctx, path, query, ttl)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

// Download fetches a binary resource with the session (no caching).
func (c *Client) Download(ctx context.Context, rawURL string, headers map[string]string, authed bool) ([]byte, string, error) {
	h := map[string]string{"Accept": "*/*"}
	maps.Copy(h, headers)
	if authed {
		tok, err := c.Token(ctx)
		if err != nil {
			return nil, "", err
		}
		h["Authorization"] = "Bearer " + tok
	}
	resp, b, err := c.raw(ctx, http.MethodGet, rawURL, nil, h)
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, "", &APIError{Status: resp.StatusCode, URL: redactURL(rawURL), Body: string(b)}
	}
	return b, resp.Header.Get("Content-Type"), nil
}
