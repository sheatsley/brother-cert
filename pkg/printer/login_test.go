package printer

import (
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"testing"
)

// mfcL2900DWLoginPage is the login form as the MFC-L2900DW (firmware
// 1.08) serves it at /etc/loginerror.html
const mfcL2900DWLoginPage = `<form method="post" action="/home/status.html"><input type="password" id="LogBox" name="B10ba" /><input type="hidden" name="loginurl" value="/general/status.html"/><input id="login" type="submit" value="Login" /></form>`

// TestParseLoginForm checks the form fields are read from each model's page
func TestParseLoginForm(t *testing.T) {
	tests := []struct {
		name string
		html string
		want loginForm
	}{
		{
			name: "MFC-L2900DW",
			html: mfcL2900DWLoginPage,
			want: loginForm{passwordField: "B10ba", action: "/home/status.html", loginURL: "/general/status.html"},
		},
		{
			name: "no action or loginurl (MFC-L2710DW)",
			html: `<form method="post"><input name="Baf9" type="password" /></form>`,
			want: loginForm{passwordField: "Baf9", action: urlLogin, loginURL: urlLogin},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseLoginForm([]byte(tt.html))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}

	if _, err := parseLoginForm([]byte(`<form></form>`)); err != errPasswordFieldNotFound {
		t.Fatalf("a page without a password field: got %v, want %v", err, errPasswordFieldNotFound)
	}
}

// TestLoginFollowsRedirect logs in to a fake MFC-L2900DW, which redirects the
// login page and takes the form at another path
func TestLoginFollowsRedirect(t *testing.T) {
	var posted url.Values
	mux := http.NewServeMux()
	mux.HandleFunc("/general/status.html", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/etc/loginerror.html?url=%2Fgeneral%2Fstatus%2Ehtml", http.StatusFound)
	})
	mux.HandleFunc("/etc/loginerror.html", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(mfcL2900DWLoginPage))
	})
	mux.HandleFunc("/home/status.html", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "post only", http.StatusMethodNotAllowed)
			return
		}
		_ = r.ParseForm()
		posted = r.PostForm
		if r.PostForm.Get("B10ba") == "secret" {
			http.SetCookie(w, &http.Cookie{Name: "AuthCookie", Value: "ok"})
		}
		http.Redirect(w, r, "/general/status.html", http.StatusMovedPermanently)
	})
	server := httptest.NewTLSServer(mux)
	defer server.Close()

	newPrinter := func() *printer {
		jar, _ := cookiejar.New(nil)
		client := server.Client()
		client.Jar = jar
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}
		return &printer{httpClient: client, baseUrl: server.URL}
	}

	if err := newPrinter().login("secret"); err != nil {
		t.Fatalf("login failed: %v", err)
	}
	if posted.Get("loginurl") != "/general/status.html" {
		t.Fatalf("posted loginurl %q, want /general/status.html", posted.Get("loginurl"))
	}
	if err := newPrinter().login("wrong"); err != errLoginNoAuth {
		t.Fatalf("wrong password: got %v, want %v", err, errLoginNoAuth)
	}
}

// mfcL2900DW110LoginForm is the login form firmware 1.10 shows on
// /home/status.html until logged in
const mfcL2900DW110LoginForm = `<form method="post" action="/home/status.html"><div><input type="password" id="LogBox" name="B10ba" /><input type="hidden" name="loginurl" value="/home/status.html"/><input id="login" type="submit" value="Login" /></div></form>`

// TestLoginFirmware110 logs in to a fake MFC-L2900DW on firmware 1.10, which
// sends / to /home/status.html, shows the login form there, and gives no
// cookie to a login that names the older page (/general/status.html, still
// answered with 1.08's form) as its loginurl
func TestLoginFirmware110(t *testing.T) {
	var posted url.Values
	var referer, origin string
	mux := http.NewServeMux()
	mux.HandleFunc("/{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/home/status.html", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/general/status.html", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(mfcL2900DWLoginPage))
	})
	mux.HandleFunc("/home/status.html", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			_, _ = w.Write([]byte(mfcL2900DW110LoginForm))
			return
		}
		_ = r.ParseForm()
		posted = r.PostForm
		referer, origin = r.Header.Get("Referer"), r.Header.Get("Origin")
		if r.PostForm.Get("B10ba") == "secret" && r.PostForm.Get("loginurl") == "/home/status.html" {
			http.SetCookie(w, &http.Cookie{Name: "AuthCookie", Value: "ok", Path: "/"})
		}
		http.Redirect(w, r, "/home/status.html", http.StatusMovedPermanently)
	})
	server := httptest.NewTLSServer(mux)
	defer server.Close()

	jar, _ := cookiejar.New(nil)
	client := server.Client()
	client.Jar = jar
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}
	p := &printer{httpClient: client, baseUrl: server.URL}

	if err := p.login("secret"); err != nil {
		t.Fatalf("login failed: %v", err)
	}
	if posted.Get("loginurl") != "/home/status.html" {
		t.Fatalf("posted loginurl %q, want /home/status.html", posted.Get("loginurl"))
	}
	if referer != server.URL+"/home/status.html" || origin != server.URL {
		t.Fatalf("posted with Referer %q and Origin %q, want the login page's", referer, origin)
	}
}
