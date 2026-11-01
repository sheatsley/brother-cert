package printer

import (
	"bytes"
	"errors"
	"fmt"
	"html"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

// The MFC-L2900DW's pages name their form fields with codes (e.g. B10e5) that
// differ between models and may change with firmware. Instead of hard-coding
// them, each step reads the form from the live page and submits it the way a
// browser would, changing only the fields it needs to.

var (
	errFormNotFound = errors.New("printer: form not found on page")

	formRegex   = regexp.MustCompile(`(?is)<form\b([^>]*)>(.*?)</form>`)
	inputRegex  = regexp.MustCompile(`(?is)<input\b([^>]*)>`)
	selectRegex = regexp.MustCompile(`(?is)<select\b([^>]*)>(.*?)</select>`)
	optionRegex = regexp.MustCompile(`(?is)<option\b([^>]*)>`)
	attrRegex   = regexp.MustCompile(`([a-zA-Z_:][-a-zA-Z0-9_:.]*)\s*=\s*"([^"]*)"`)

	// e.g. var argUrl='/net/security/certificate/certificate.html';
	waitArgURLRegex = regexp.MustCompile(`argUrl\s*=\s*'([^']+)'`)

	// e.g. <p class="errorMessage">...</p>, <div class="postError">...</div>
	pageMessageRegex = regexp.MustCompile(`(?is)<(?:p|div)\b[^>]*class="(?:[^"]*Message[^"]*|postError[^"]*)"[^>]*>(.*?)</(?:p|div)>`)
	breakRegex       = regexp.MustCompile(`(?i)<br\b[^>]*>`)
	tagRegex         = regexp.MustCompile(`(?s)<[^>]*>`)
)

// waitFormID is the id of the form on the printer's "Please wait" pages,
// which the page's script submits again every few seconds until the work
// is done
const waitFormID = "cert_wait"

// waitInterval is how long the printer's "Please wait" pages wait before
// submitting again (cert_wait.js uses 3 seconds; a variable so tests can
// shorten it)
var waitInterval = 3 * time.Second

// maxWaitPages is how many "Please wait" pages are followed before giving up
const maxWaitPages = 20

// formField is one named value a form submits. A file input is kept in
// its place among the fields (isFile), because the printer reads a
// multipart upload's parts in page order
type formField struct {
	name   string
	value  string
	isFile bool
}

// htmlForm is a form read from a page: where it posts to, and the fields a
// browser would submit with it as it stands
type htmlForm struct {
	// id is the form's id attribute
	id string
	// action is where the form posts to (may be relative to the page)
	action string
	// fields are the values a browser would submit, in page order
	fields []formField
	// selects are the names of the form's select boxes, in page order
	selects []string
	// fileInputs and passwordInputs are the names of those inputs
	fileInputs     []string
	passwordInputs []string
}

// attrs returns an element's attributes. Values are unescaped; attributes
// without a value (none on these pages) are ignored
func attrs(attrText string) map[string]string {
	m := map[string]string{}
	for _, caps := range attrRegex.FindAllStringSubmatch(attrText, -1) {
		m[strings.ToLower(caps[1])] = html.UnescapeString(caps[2])
	}
	return m
}

// has reports whether an element's attribute text sets a boolean attribute
// such as checked or disabled
func has(attrText, name string) bool {
	return regexp.MustCompile(`(?i)\b` + name + `\b`).MatchString(attrText)
}

// parseForm reads the form with the given id from a page (with an empty id,
// the first form whose action ends with actionSuffix). It records the fields
// as a browser would submit them: hidden, text and password inputs with
// their values, checked checkboxes and radios (value "on" if they have none),
// and each select's selected option (or its first). Disabled fields, file
// inputs and buttons are not submitted
func parseForm(body []byte, id, actionSuffix string) (*htmlForm, error) {
	for _, fm := range formRegex.FindAllSubmatch(body, -1) {
		formAttrs := attrs(string(fm[1]))
		if id != "" && formAttrs["id"] != id {
			continue
		}
		if id == "" && !strings.HasSuffix(formAttrs["action"], actionSuffix) {
			continue
		}

		f := &htmlForm{id: formAttrs["id"], action: formAttrs["action"]}
		inner := fm[2]

		// collect fields with their position, so they can be put in page order
		type positioned struct {
			pos int
			formField
		}
		var found []positioned

		for _, loc := range inputRegex.FindAllSubmatchIndex(inner, -1) {
			attrText := string(inner[loc[2]:loc[3]])
			a := attrs(attrText)
			name := a["name"]
			kind := strings.ToLower(a["type"])
			if kind == "" {
				kind = "text"
			}
			if name == "" || has(attrText, "disabled") {
				continue
			}
			switch kind {
			case "submit", "button", "reset", "image":
				continue
			case "file":
				f.fileInputs = append(f.fileInputs, name)
				found = append(found, positioned{loc[0], formField{name: name, isFile: true}})
			case "checkbox", "radio":
				if !has(attrText, "checked") {
					continue
				}
				value, ok := a["value"]
				if !ok {
					value = "on"
				}
				found = append(found, positioned{loc[0], formField{name: name, value: value}})
			default:
				if kind == "password" {
					f.passwordInputs = append(f.passwordInputs, name)
				}
				found = append(found, positioned{loc[0], formField{name: name, value: a["value"]}})
			}
		}

		for _, loc := range selectRegex.FindAllSubmatchIndex(inner, -1) {
			attrText := string(inner[loc[2]:loc[3]])
			a := attrs(attrText)
			name := a["name"]
			if name == "" || has(attrText, "disabled") {
				continue
			}
			f.selects = append(f.selects, name)
			value := ""
			first := true
			for _, opt := range optionRegex.FindAllStringSubmatch(string(inner[loc[4]:loc[5]]), -1) {
				optAttrs := attrs(opt[1])
				if first {
					value = optAttrs["value"]
					first = false
				}
				if has(opt[1], "selected") {
					value = optAttrs["value"]
					break
				}
			}
			found = append(found, positioned{loc[0], formField{name: name, value: value}})
		}

		sort.SliceStable(found, func(i, j int) bool { return found[i].pos < found[j].pos })
		for _, p := range found {
			f.fields = append(f.fields, p.formField)
		}
		return f, nil
	}

	if id != "" {
		return nil, fmt.Errorf("%w (id %q)", errFormNotFound, id)
	}
	return nil, fmt.Errorf("%w (action ending %q)", errFormNotFound, actionSuffix)
}

// set gives a field a value, adding it if the form doesn't submit it yet
func (f *htmlForm) set(name, value string) {
	for i := range f.fields {
		if f.fields[i].name == name {
			f.fields[i].value = value
			return
		}
	}
	f.fields = append(f.fields, formField{name: name, value: value})
}

// get returns a field's value, and whether the form submits it
func (f *htmlForm) get(name string) (string, bool) {
	for _, field := range f.fields {
		if field.name == name {
			return field.value, true
		}
	}
	return "", false
}

// values returns the fields as url.Values (file inputs, which only a
// multipart upload can carry, are left out)
func (f *htmlForm) values() url.Values {
	v := url.Values{}
	for _, field := range f.fields {
		if !field.isFile {
			v.Add(field.name, field.value)
		}
	}
	return v
}

// actionURL resolves the form's action against the page it came from (a form
// without an action posts back to that page)
func (f *htmlForm) actionURL(pageURL *url.URL) (*url.URL, error) {
	if f.action == "" {
		return pageURL, nil
	}
	return pageURL.Parse(f.action)
}

// pageURL returns the printer URL for a path (which may include a query)
func (p *printer) pageURL(path string) (*url.URL, error) {
	base, err := url.ParseRequestURI(p.baseUrl)
	if err != nil {
		return nil, err
	}
	return base.Parse(path)
}

// getPage fetches a printer page and returns its body; anything but 200 OK
// is an error
func (p *printer) getPage(u *url.URL) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("printer: get of %s failed (status code %d)", u.Path, resp.StatusCode)
	}
	return body, nil
}

// postForm submits url-encoded values, as the form on the page at referer
// would, and returns the response body; anything but 200 OK is an error
func (p *printer) postForm(u, referer *url.URL, values url.Values) ([]byte, error) {
	req, err := http.NewRequest(http.MethodPost, u.String(), strings.NewReader(values.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return p.doPost(req, referer)
}

// p12ContentType is the type browsers give a .p12 file in an upload
const p12ContentType = "application/x-pkcs12"

// postMultipart submits a form as multipart/form-data, as the form on the
// page at referer would: in page order like a browser (the printer depends
// on it), with the file attached in the place of the named file input. It
// returns the response body
func (p *printer) postMultipart(u, referer *url.URL, f *htmlForm, fileField, fileName string, file []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	attached := false
	for _, field := range f.fields {
		if !field.isFile {
			if err := w.WriteField(field.name, field.value); err != nil {
				return nil, fmt.Errorf("printer: failed to write form (%w)", err)
			}
			continue
		}

		// a file input: the file goes here, any other file input is sent empty
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, field.name, fileName))
		h.Set("Content-Type", p12ContentType)
		var content []byte
		if field.name == fileField {
			content = file
			attached = true
		} else {
			h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=""`, field.name))
			h.Set("Content-Type", "application/octet-stream")
		}
		part, err := w.CreatePart(h)
		if err != nil {
			return nil, fmt.Errorf("printer: failed to write form (%w)", err)
		}
		if _, err = part.Write(content); err != nil {
			return nil, fmt.Errorf("printer: failed to write form (%w)", err)
		}
	}
	if !attached {
		return nil, fmt.Errorf("printer: the form has no file input %q", fileField)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("printer: failed to close form (%w)", err)
	}

	req, err := http.NewRequest(http.MethodPost, u.String(), &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	return p.doPost(req, referer)
}

// pageMessages returns the text of a page's notes and errors (its
// <p class="...Message"> paragraphs and <div class="postError..."> boxes),
// to report why the printer refused something
func pageMessages(body []byte) string {
	var msgs []string
	for _, caps := range pageMessageRegex.FindAllSubmatch(body, -1) {
		text := breakRegex.ReplaceAllString(string(caps[1]), " ")
		text = tagRegex.ReplaceAllString(text, "")
		text = strings.Join(strings.Fields(html.UnescapeString(text)), " ")
		if text != "" {
			msgs = append(msgs, text)
		}
	}
	return strings.Join(msgs, " / ")
}

// setBrowserHeaders adds the headers a browser sends with a form: the page
// the form came from (Referer) and its origin (Origin). The MFC-L2900DW
// refuses a form submitted without them ("Submit Error: Your request was
// rejected"), and doesn't advance its "Please wait" pages
func setBrowserHeaders(req *http.Request, referer *url.URL) {
	if referer == nil {
		return
	}
	req.Header.Set("Referer", referer.String())
	req.Header.Set("Origin", referer.Scheme+"://"+referer.Host)
}

// doPost sends a POST, as the form on the page at referer would, and returns
// the response body; anything but 200 OK is an error
func (p *printer) doPost(req *http.Request, referer *url.URL) ([]byte, error) {
	setBrowserHeaders(req, referer)
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("printer: post to %s failed (status code %d)", req.URL.Path, resp.StatusCode)
	}
	return body, nil
}

// followWaitPages does what the printer's "Please wait" pages do in a
// browser: while the page has the wait form, wait a few seconds and submit
// it again, to the URL the page names (argUrl) or else back to the page it
// came from (from, which is also each submission's Referer). It returns the
// first page that isn't a wait page
func (p *printer) followWaitPages(body []byte, from *url.URL) ([]byte, error) {
	for range maxWaitPages {
		f, err := parseForm(body, waitFormID, "")
		if err != nil {
			// not a wait page: the work is done
			return body, nil
		}

		target := from
		if caps := waitArgURLRegex.FindSubmatch(body); len(caps) == 2 {
			if target, err = from.Parse(string(caps[1])); err != nil {
				return nil, fmt.Errorf("printer: bad wait page target %q (%w)", caps[1], err)
			}
		}

		time.Sleep(waitInterval)
		if body, err = p.postForm(target, from, f.values()); err != nil {
			return nil, err
		}
		from = target
	}

	return nil, errors.New("printer: the printer kept answering \"Please wait\"")
}
