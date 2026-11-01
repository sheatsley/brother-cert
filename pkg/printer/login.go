package printer

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

const urlLogin = "/general/status.html"

// maxLoginRedirects is how many redirects are followed to reach the login
// form (the MFC-L2900DW sends /general/status.html to /etc/loginerror.html)
const maxLoginRedirects = 5

var (
	errLoginNoAuth           = errors.New("printer: login: no auth cookie received (wrong password?)")
	errPasswordFieldNotFound = errors.New("printer: login: password field not found in login form")
	errLoginTooManyRedirects = errors.New("printer: login: too many redirects before the login form")
)

// loginForm is what the printer's login form asks for
type loginForm struct {
	// passwordField is the name of the password input
	passwordField string
	// action is the path the form posts to
	action string
	// loginURL is the value of the form's hidden loginurl input
	loginURL string
}

// parsePasswordFieldName returns the name attribute of the password input field
// from the HTML login form
func parsePasswordFieldName(bodyBytes []byte) (fieldName string, err error) {
	// Look for input elements with type="password"
	// This regex handles both orders: type first or name first
	// e.g. <input type="password" name="Baf9" ... /> or <input name="Baf9" type="password" ... />
	regex := regexp.MustCompile(`<input[^>]+(?:type="password"[^>]+name="([^"]+)"[^>]*|name="([^"]+)"[^>]+type="password"[^>]*)>`)
	caps := regex.FindSubmatch(bodyBytes)

	// error if didn't find what was expected
	if len(caps) < 2 {
		return "", errPasswordFieldNotFound
	}

	// return the non-empty capture group (either caps[1] or caps[2])
	if len(caps[1]) > 0 {
		return string(caps[1]), nil
	}
	return string(caps[2]), nil
}

// parseLoginForm returns what the login form in the HTML asks for. The form's
// action and its loginurl value default to urlLogin when the page doesn't
// give them, which is how the MFC-L2710DW's form behaves
func parseLoginForm(bodyBytes []byte) (form loginForm, err error) {
	form.passwordField, err = parsePasswordFieldName(bodyBytes)
	if err != nil {
		return loginForm{}, err
	}

	// e.g. <form method="post" action="/home/status.html">
	form.action = urlLogin
	actionRegex := regexp.MustCompile(`<form[^>]+action="([^"]+)"`)
	if caps := actionRegex.FindSubmatch(bodyBytes); len(caps) == 2 {
		form.action = string(caps[1])
	}

	// e.g. <input type="hidden" name="loginurl" value="/general/status.html"/>
	form.loginURL = urlLogin
	loginURLRegex := regexp.MustCompile(`<input[^>]+(?:name="loginurl"[^>]+value="([^"]*)"|value="([^"]*)"[^>]+name="loginurl")`)
	if caps := loginURLRegex.FindSubmatch(bodyBytes); len(caps) == 3 {
		if len(caps[1]) > 0 {
			form.loginURL = string(caps[1])
		} else if len(caps[2]) > 0 {
			form.loginURL = string(caps[2])
		}
	}

	return form, nil
}

// getLoginPage fetches the login page, following redirects (the client
// otherwise doesn't, because the printer answers POSTs with them), and
// returns its HTML and the URL it was served from
func (p *printer) getLoginPage(u *url.URL) (bodyBytes []byte, pageURL *url.URL, err error) {
	pageURL = u
	for range maxLoginRedirects + 1 {
		req, err := http.NewRequest(http.MethodGet, pageURL.String(), nil)
		if err != nil {
			return nil, nil, err
		}

		resp, err := p.httpClient.Do(req)
		if err != nil {
			return nil, nil, err
		}
		bodyBytes, err = io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, nil, err
		}

		// a redirect: go where it says
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			location, err := resp.Location()
			if err != nil {
				return nil, nil, fmt.Errorf("printer: login: redirect without a location (%w)", err)
			}
			pageURL = location
			continue
		}

		return bodyBytes, pageURL, nil
	}

	return nil, nil, errLoginTooManyRedirects
}

// login performs the login command against the remote printer. it is
// used internally as part of the printer creation process to ensure
// credentials are valid
func (p *printer) login(password string) error {
	// get url & set path
	u, err := url.ParseRequestURI(p.baseUrl)
	if err != nil {
		return err
	}
	u.Path = urlLogin

	// first, fetch the login page to discover what its form asks for
	bodyBytes, pageURL, err := p.getLoginPage(u)
	if err != nil {
		return err
	}
	form, err := parseLoginForm(bodyBytes)
	if err != nil {
		return err
	}

	// login form values using the discovered field name
	data := url.Values{}
	data.Set(form.passwordField, password)
	data.Set("loginurl", form.loginURL)

	// post them where the form does
	actionURL, err := pageURL.Parse(form.action)
	if err != nil {
		return fmt.Errorf("printer: login: bad form action %q (%w)", form.action, err)
	}

	// make and do login request
	req, err := http.NewRequest(http.MethodPost, actionURL.String(), strings.NewReader(data.Encode()))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// read and discard entire body (should be empty)
	_, _ = io.Copy(io.Discard, resp.Body)

	// confirm got cookie
	foundAuthCookie := false
	for _, c := range resp.Cookies() {
		if c.Name == "AuthCookie" {
			foundAuthCookie = true
			break
		}
	}
	if !foundAuthCookie {
		return errLoginNoAuth
	}

	// set cookies in jar
	p.httpClient.Jar.SetCookies(u, resp.Cookies())

	return nil
}
