package printer

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

const urlHttpCertServerSettings = "/net/net/certificate/http.html"

var (
	errCurrentCertIdNotFound = errors.New("printer: get: failed to find current cert id")
)

// getHttpSettings fetches the HTTP Server Settings page
func (p *printer) getHttpSettings() ([]byte, error) {
	// get url & set path
	u, err := url.ParseRequestURI(p.baseUrl)
	if err != nil {
		return nil, err
	}
	u.Path = urlHttpCertServerSettings

	// make and do request
	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// read body of response
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// OK status?
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("printer: get of http settings page failed (status code %d)", resp.StatusCode)
	}

	return bodyBytes, nil
}

// httpSettingsFormID is the id of the form on the HTTP Server Settings page
const httpSettingsFormID = "http_setting"

// restartFlagField is the hidden field the page's script (restart_flag.js)
// sets to 1 when a setting that needs a restart changed; choosing another
// certificate does. Its code is the MFC-L2900DW's
const restartFlagField = "B10f3"

// SetActiveCert sets the printer's active certificate to the specified ID
// and then restarts the printer (to make the new cert active). Only the
// certificate changes: every other setting on the page (which protocols may
// use HTTP, and so on) is submitted as it stands, so it stays as-is
// Note: This function even works if the `id` is not in the dropdown box of the
// printer's cert picker (which happens when the cert does not have a Common Name)
func (p *printer) SetActiveCert(id string) error {
	// GET http settings and read the form
	u, err := p.pageURL(urlHttpCertServerSettings)
	if err != nil {
		return err
	}
	bodyBytes, err := p.getPage(u)
	if err != nil {
		return err
	}
	form, err := parseForm(bodyBytes, httpSettingsFormID, "")
	if err != nil {
		return fmt.Errorf("printer: set active cert: %w", err)
	}

	// the certificate is the page's only select box
	if len(form.selects) != 1 {
		return fmt.Errorf("printer: set active cert: expected one certificate selector on the http settings page, found %d", len(form.selects))
	}
	form.set(form.selects[0], id)

	// what the page's script does on submit: flag that a restart is needed
	if _, ok := form.get(restartFlagField); !ok {
		return fmt.Errorf("printer: set active cert: the http settings page has no %s field (different firmware?)", restartFlagField)
	}
	form.set(restartFlagField, "1")

	// submit the settings
	actionURL, err := form.actionURL(u)
	if err != nil {
		return err
	}
	bodyBytes, err = p.postForm(actionURL, form.values())
	if err != nil {
		return fmt.Errorf("printer: failed to post set active cert form (%w)", err)
	}

	// the printer answers with a confirmation ("restart immediately?", with
	// "activate other protocols that have secure settings" checked); submit
	// it as it stands
	confirm, err := parseForm(bodyBytes, "", "http.html")
	if err != nil {
		return fmt.Errorf("printer: set active cert: no restart confirmation (%w)", err)
	}
	confirmURL, err := confirm.actionURL(actionURL)
	if err != nil {
		return err
	}

	// the printer restarts; it may drop the connection instead of answering
	if _, err = p.postForm(confirmURL, confirm.values()); err != nil && !isConnectionDropped(err) {
		return fmt.Errorf("printer: failed to post restart confirmation (%w)", err)
	}

	return nil
}

// isConnectionDropped reports whether a request failed without any HTTP
// answer (the connection closed, reset or broke off), as it does when the
// printer restarts. An HTTP answer that isn't 200 OK is not this. Treating
// such a failure of the restart confirmation as the restart is safe: the old
// certificate is only deleted once the printer is seen using the new one
func isConnectionDropped(err error) bool {
	var urlErr *url.Error
	return errors.As(err, &urlErr)
}
