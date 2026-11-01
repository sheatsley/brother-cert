package printer

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"
	"time"
)

// settingsPage is a cut-down MFC-L2900DW HTTP Server Settings form
const settingsPage = `<form method="post" action="/home/status.html"><input type="hidden" id="CSRFToken" name="CSRFToken" value="menu"/></form>
<div id="pageContents"><form method="post" action="http.html" id="http_setting" onsubmit="netews_set_restart_flag(this,certselect_items)">
<input type="hidden" id="pageid" name="pageid" value="404" />
<input type="hidden" id="CSRFToken1" name="CSRFToken" value="abc&#43;
def=="/>
<select id="B10e5" name="B10e5" onchange="fncCertSelect(this)" ><option value="0" >Preset</option><option value="1"  selected="selected">&#109;&#102;&#99;</option></select>
<input type="checkbox" id="B1003" name="B1003" value="1" checked="checked" />
<input type="checkbox" id="B1005" name="B1005" value="1"/>
<input type="checkbox" id="cb_ipp_https" name="cb_ipp_https" checked="checked" />
<input type="checkbox" id="B1021" name="B1021" value="1" checked="checked" disabled="disabled" />
<input type="hidden" id="B10f3" name="B10f3"  value="0" />
<input type="button" value="Cancel" onclick="x()"/><input type="submit"  value="Submit" />
</form></div>`

// TestParseForm checks a form is read the way a browser would submit it
func TestParseForm(t *testing.T) {
	f, err := parseForm([]byte(settingsPage), httpSettingsFormID, "")
	if err != nil {
		t.Fatal(err)
	}
	if f.action != "http.html" {
		t.Errorf("action %q", f.action)
	}
	want := []formField{
		{name: "pageid", value: "404"},
		{name: "CSRFToken", value: "abc+\ndef=="},
		{name: "B10e5", value: "1"},
		{name: "B1003", value: "1"},
		{name: "cb_ipp_https", value: "on"},
		{name: "B10f3", value: "0"},
	}
	if !reflect.DeepEqual(f.fields, want) {
		t.Errorf("fields\n got  %v\n want %v", f.fields, want)
	}
	if !reflect.DeepEqual(f.selects, []string{"B10e5"}) {
		t.Errorf("selects %v", f.selects)
	}

	// set changes a field in place, or adds it
	f.set("B10f3", "1")
	f.set("extra", "x")
	if v, _ := f.get("B10f3"); v != "1" {
		t.Errorf("B10f3 %q", v)
	}
	if v, ok := f.get("extra"); !ok || v != "x" {
		t.Errorf("extra %q %v", v, ok)
	}

	// a form can also be found by its action
	if f, err = parseForm([]byte(settingsPage), "", "http.html"); err != nil || f.id != httpSettingsFormID {
		t.Errorf("by action: %v %v", f, err)
	}
	if _, err = parseForm([]byte(settingsPage), "missing", ""); err == nil {
		t.Error("a missing form was found")
	}
}

// importPage is a cut-down MFC-L2900DW certificate import form
const importPage = `<form method="post" enctype="multipart/form-data" action="/net/security/certificate/import.html" id="cert_import" onsubmit="return false;">
<input type="hidden" id="pageid" name="pageid" value="477" />
<input type="hidden" id="CSRFToken1" name="CSRFToken" value="tok"/>
<input type="hidden" id="B10cc" name="B10cc"  value="" /><input type="hidden" id="B10da" name="B10da"  value="" />
<input type="hidden" id="hidden_certificate_process_control" name="hidden_certificate_process_control" value="1" />
<input type="file" class="inputFile" id="Bfb6" name="Bfb6"  value="" />
<input type="password" class="password" id="Bfb7" name="Bfb7" maxlength="51"  value="" />
<input type="hidden" id="hidden_cert_import_password" name="hidden_cert_import_password" value="" />
<input type="button" onclick="onetime_submit()"  value="Submit" /></form>`

// TestPostMultipartOrder checks the upload's parts are sent in page order,
// with the file in its input's place, as a browser sends them
func TestPostMultipartOrder(t *testing.T) {
	var parts []string
	var fileType string
	var file []byte
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mr, err := r.MultipartReader()
		if err != nil {
			t.Error(err)
			return
		}
		for {
			part, err := mr.NextPart()
			if err != nil {
				break
			}
			parts = append(parts, part.FormName())
			if part.FileName() != "" {
				fileType = part.Header.Get("Content-Type")
				file, _ = io.ReadAll(part)
			}
		}
	}))
	defer server.Close()

	f, err := parseForm([]byte(importPage), importFormID, "")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.fileInputs, []string{"Bfb6"}) || !reflect.DeepEqual(f.passwordInputs, []string{"Bfb7"}) {
		t.Fatalf("inputs %v %v", f.fileInputs, f.passwordInputs)
	}
	if _, ok := f.values()["Bfb6"]; ok {
		t.Error("a file input is in the url-encoded values")
	}

	p := &printer{httpClient: server.Client(), baseUrl: server.URL}
	u, _ := url.Parse(server.URL + "/net/security/certificate/import.html")
	if _, err = p.postMultipart(u, f, "Bfb6", "certkey.p12", []byte("p12")); err != nil {
		t.Fatal(err)
	}
	want := []string{"pageid", "CSRFToken", "B10cc", "B10da", "hidden_certificate_process_control", "Bfb6", "Bfb7", "hidden_cert_import_password"}
	if !reflect.DeepEqual(parts, want) {
		t.Errorf("parts\n got  %v\n want %v", parts, want)
	}
	if fileType != p12ContentType || string(file) != "p12" {
		t.Errorf("file part %q %q", fileType, file)
	}
}

// TestPageMessages checks the printer's notes are read from a page
func TestPageMessages(t *testing.T) {
	page := `<p class="errorMessage">The&#32;certificate
could not be <b>imported</b>.</p><p class="other">x</p><p class="noteMessage">Try again.</p>`
	if got := pageMessages([]byte(page)); got != "The certificate could not be imported. / Try again." {
		t.Errorf("got %q", got)
	}
}

// TestFollowWaitPages checks the "Please wait" pages are submitted again, to
// the page they came from and then to their argUrl, until the work is done
func TestFollowWaitPages(t *testing.T) {
	waitInterval = time.Millisecond
	var posted []string
	wait := func(script string) string {
		return `<form method="post" action="/net/security/certificate/certificate_wait.html" id="cert_wait">` +
			`<input type="hidden" name="hidden_certificate_process_control" value="2" /></form>` +
			`<script type="text/javascript"><!--` + script + `cert_wait_countdown_post( );--></script>`
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/net/security/certificate/import.html", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		posted = append(posted, r.URL.Path+" "+r.PostForm.Encode())
		_, _ = w.Write([]byte(wait(`var argInterval='3';var argUrl='/net/security/certificate/certificate.html';cert_wait_setjumppage_timer( argInterval, argUrl );`)))
	})
	mux.HandleFunc("/net/security/certificate/certificate.html", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		posted = append(posted, r.URL.Path+" "+r.PostForm.Encode())
		_, _ = w.Write([]byte(`<p>done</p>`))
	})
	server := httptest.NewTLSServer(mux)
	defer server.Close()

	jar, _ := cookiejar.New(nil)
	client := server.Client()
	client.Jar = jar
	p := &printer{httpClient: client, baseUrl: server.URL}
	from, _ := url.Parse(server.URL + "/net/security/certificate/import.html")

	body, err := p.followWaitPages([]byte(wait("")), from)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `<p>done</p>` {
		t.Errorf("final page %q", body)
	}
	want := []string{
		"/net/security/certificate/import.html hidden_certificate_process_control=2",
		"/net/security/certificate/certificate.html hidden_certificate_process_control=2",
	}
	if !reflect.DeepEqual(posted, want) {
		t.Errorf("posted\n got  %v\n want %v", posted, want)
	}
}
