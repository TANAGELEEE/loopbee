package httpapi

import (
	"loopbee/internal/core"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuthenticationAndOrigin(t *testing.T) {
	a, err := core.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	s := New(a, "127.0.0.1:1234")
	h := s.Handler()
	for _, tc := range []struct {
		name, path, method, host, origin, token, body string
		want                                          int
	}{
		{name: "unauthenticated", path: "/api/state", method: "GET", host: s.Host, want: 401},
		{name: "authenticated", path: "/api/state", method: "GET", host: s.Host, token: s.Token, want: 200},
		{name: "dns rebinding", path: "/api/state", method: "GET", host: "evil.example", token: s.Token, want: 403},
		{name: "cross origin", path: "/api/settings", method: "POST", host: s.Host, origin: "https://evil.example", token: s.Token, body: "{}", want: 403},
		{name: "bad bootstrap", path: "/api/session", method: "POST", host: s.Host, body: `{"token":"bad"}`, want: 401},
		{name: "bootstrap", path: "/api/session", method: "POST", host: s.Host, body: `{"token":"` + s.Token + `"}`, want: 200},
		{name: "unknown fields", path: "/api/settings", method: "POST", host: s.Host, token: s.Token, body: `{"unexpected":true}`, want: 400},
		{name: "multiple values", path: "/api/settings", method: "POST", host: s.Host, token: s.Token, body: `{} {}`, want: 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "http://"+tc.host+tc.path, strings.NewReader(tc.body))
			r.Host = tc.host
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			r.Header.Set("Content-Type", "application/json")
			if tc.token != "" {
				r.AddCookie(&http.Cookie{Name: "loopbee", Value: tc.token})
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("got %d: %s", w.Code, w.Body.String())
			}
			if tc.name == "bootstrap" {
				cookies := w.Result().Cookies()
				if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
					t.Fatal("insecure cookie")
				}
			}
		})
	}
}
func TestLoopbackOnly(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:0", "192.168.1.2:0", "localhost:0"} {
		if l, err := Listen(addr); err == nil {
			l.Close()
			t.Fatalf("accepted %s", addr)
		}
	}
}
