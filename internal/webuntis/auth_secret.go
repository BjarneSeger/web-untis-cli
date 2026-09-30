package webuntis

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// timeNow is the clock for OTP generation; tests replace it.
var timeNow = time.Now

// loginSecret authenticates like the Untis Mobile app: getUserData2017 with a
// TOTP derived from the shared secret (the key shown next to the QR code under
// "Zugriff über Untis Mobile" in the WebUntis profile). This is the only way
// for Microsoft/SSO-only accounts (typically students) to log in without a
// browser. The resulting JSESSIONID is the same kind of session as after a
// password login.
func (c *Client) loginSecret(ctx context.Context, secret string) error {
	p := c.Profile
	if p.Username == "" || secret == "" {
		return fmt.Errorf("%w: session expired and no stored Untis Mobile key (run `webuntis setup` or `webuntis login --secret`, or set WEBUNTIS_SECRET)", ErrAuth)
	}
	key, err := decodeSecret(normalizeSecret(secret))
	if err != nil {
		return fmt.Errorf("%w: invalid Untis Mobile key: %v (run `webuntis login --secret` to store a new one)", ErrAuth, err)
	}
	c.debugf("logging in as %s at %s/%s via Untis Mobile key", p.Username, p.Server, p.School)
	c.session.Token = ""
	c.setSchoolCookies("")

	sid, err := c.otpLoginWithRetry(ctx, key)
	if err != nil {
		return err
	}
	c.setSchoolCookies(sid)
	if _, err := c.fetchToken(ctx); err != nil {
		return fmt.Errorf("login failed: Untis Mobile login accepted but no API token was issued: %w", err)
	}
	c.session.LoginAt = time.Now()
	return c.SaveSession()
}

// otpLoginWithRetry tries the current code and, if the server rejects it and
// the code has changed meanwhile (step boundary crossed), once more with the
// fresh code.
func (c *Client) otpLoginWithRetry(ctx context.Context, key []byte) (string, error) {
	code := totp(key, timeNow())
	sid, err := c.otpLogin(ctx, code)
	var re *RPCError
	if !errors.As(err, &re) {
		return sid, err // success or transport/HTTP error
	}
	if next := totp(key, timeNow()); next != code {
		c.debugf("OTP rejected (%v), retrying with the next code", re)
		sid, err = c.otpLogin(ctx, next)
		if err == nil {
			return sid, nil
		}
		if !errors.As(err, &re) {
			return "", fmt.Errorf("login failed: %w", err)
		}
	}
	return "", fmt.Errorf("login failed: %s (user %q, school %q; check the Untis Mobile key, `webuntis login --secret` stores a new one, and the system clock)", re.Message, c.Profile.Username, c.Profile.School)
}

// otpLogin performs one getUserData2017 call and returns the JSESSIONID.
func (c *Client) otpLogin(ctx context.Context, code string) (string, error) {
	// otp is sent as string (like the Untis Mobile app), which keeps leading zeros.
	params := map[string]any{"auth": map[string]any{"clientTime": timeNow().UnixMilli(), "user": c.Profile.Username, "otp": code}}
	resp, err := c.rpcIntern(ctx, "getUserData2017", params, nil)
	if err != nil {
		return "", err
	}
	for _, ck := range resp.Cookies() {
		if ck.Name == "JSESSIONID" && ck.Value != "" {
			return ck.Value, nil
		}
	}
	// The cookie jar may have stored it even if the header was consumed.
	if sid := c.sessionCookie(); sid != "" {
		return sid, nil
	}
	return "", errors.New("login failed: getUserData2017 succeeded but returned no session cookie")
}
