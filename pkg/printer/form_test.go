package printer

import (
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
		{"pageid", "404"},
		{"CSRFToken", "abc+\ndef=="},
		{"B10e5", "1"},
		{"B1003", "1"},
		{"cb_ipp_https", "on"},
		{"B10f3", "0"},
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
