package httpapi

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"time"

	"loopbee/internal/core"
	"loopbee/internal/ui"
)

type Server struct {
	App   *core.App
	Token string
	Host  string
}

func New(a *core.App, host string) *Server {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return &Server{App: a, Token: hex.EncodeToString(b), Host: host}
}
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/session", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Token string `json:"token"`
		}
		if decode(w, r, &v) != nil || subtle.ConstantTimeCompare([]byte(v.Token), []byte(s.Token)) != 1 {
			http.Error(w, "Unauthorized", 401)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "loopbee", Value: s.Token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
		jsonResponse(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /api/state", func(w http.ResponseWriter, r *http.Request) { s.state(w) })
	mux.HandleFunc("GET /api/digest", func(w http.ResponseWriter, r *http.Request) {
		text, err := s.App.Digest(time.Now())
		if err != nil {
			fail(w, err)
			return
		}
		jsonResponse(w, map[string]string{"text": text})
	})
	mux.HandleFunc("POST /api/definition/parse", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Source string `json:"source"`
		}
		if err := decode(w, r, &v); err != nil {
			fail(w, err)
			return
		}
		l, err := core.ParseDefinition(v.Source)
		if err != nil {
			fail(w, err)
			return
		}
		jsonResponse(w, l)
	})
	mux.HandleFunc("GET /api/import/claude", func(w http.ResponseWriter, r *http.Request) {
		v, err := core.ClaudeCandidates()
		if err != nil {
			fail(w, err)
			return
		}
		jsonResponse(w, v)
	})
	mux.HandleFunc("POST /api/loops", func(w http.ResponseWriter, r *http.Request) {
		var l core.Loop
		if err := decode(w, r, &l); err != nil {
			fail(w, err)
			return
		}
		if _, err := s.App.SaveLoop(l); err != nil {
			fail(w, err)
			return
		}
		s.state(w)
	})
	mux.HandleFunc("DELETE /api/loops/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := s.App.DeleteLoop(r.PathValue("id")); err != nil {
			fail(w, err)
			return
		}
		s.state(w)
	})
	mux.HandleFunc("POST /api/loops/{id}/run", func(w http.ResponseWriter, r *http.Request) {
		if _, err := s.App.Start(r.PathValue("id")); err != nil {
			fail(w, err)
			return
		}
		s.state(w)
	})
	mux.HandleFunc("GET /api/runs/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		events, err := s.App.Events(r.PathValue("id"))
		if err != nil {
			fail(w, err)
			return
		}
		jsonResponse(w, events)
	})
	mux.HandleFunc("POST /api/runs/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		if err := s.App.Cancel(r.PathValue("id")); err != nil {
			fail(w, err)
			return
		}
		s.state(w)
	})
	mux.HandleFunc("POST /api/author", func(w http.ResponseWriter, r *http.Request) {
		var v core.AuthorRequest
		if err := decode(w, r, &v); err != nil {
			fail(w, err)
			return
		}
		reply, err := s.App.Author(r.Context(), v)
		if err != nil {
			fail(w, err)
			return
		}
		jsonResponse(w, reply)
	})
	mux.HandleFunc("POST /api/runs/{id}/rerun", func(w http.ResponseWriter, r *http.Request) {
		if _, err := s.App.Rerun(r.PathValue("id")); err != nil {
			fail(w, err)
			return
		}
		s.state(w)
	})
	mux.HandleFunc("POST /api/runs/{id}/ack", func(w http.ResponseWriter, r *http.Request) {
		if err := s.App.Acknowledge(r.PathValue("id")); err != nil {
			fail(w, err)
			return
		}
		s.state(w)
	})
	mux.HandleFunc("POST /api/scheduler/pause", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Paused bool `json:"paused"`
		}
		if err := decode(w, r, &v); err != nil {
			fail(w, err)
			return
		}
		if err := s.App.SetPaused(v.Paused); err != nil {
			fail(w, err)
			return
		}
		s.state(w)
	})
	// loopbee's own updates: ask GitHub now (and download), or restart into the downloaded version (when: "" waits
	// for active Runs to end, "now" does not, "cancel" stops a restart that waits).
	mux.HandleFunc("POST /api/update/check", func(w http.ResponseWriter, r *http.Request) {
		s.App.CheckUpdate()
		s.state(w)
	})
	mux.HandleFunc("POST /api/update/install", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			When string `json:"when"`
		}
		if err := decode(w, r, &v); err != nil {
			fail(w, err)
			return
		}
		if v.When != "" && v.When != "now" && v.When != "cancel" {
			fail(w, errors.New("when is empty, now or cancel"))
			return
		}
		if _, err := s.App.RestartToUpdate(v.When); err != nil {
			fail(w, err)
			return
		}
		s.state(w)
	})
	mux.HandleFunc("POST /api/onboarding/done", func(w http.ResponseWriter, r *http.Request) {
		if err := s.App.CompleteOnboarding(); err != nil {
			fail(w, err)
			return
		}
		s.state(w)
	})
	mux.HandleFunc("GET /api/diagnostics", func(w http.ResponseWriter, r *http.Request) {
		v, err := s.App.State()
		if err != nil {
			fail(w, err)
			return
		}
		jsonResponse(w, map[string]any{"info": v.Info, "agents": v.Agents, "loops": len(v.Loops), "runs": len(v.Runs), "role": v.Settings.Role, "paused": v.Settings.Paused, "listen": s.Host})
	})
	mux.HandleFunc("POST /api/runs/{id}/gate", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Approve bool   `json:"approve"`
			Note    string `json:"note"`
		}
		if err := decode(w, r, &v); err != nil {
			fail(w, err)
			return
		}
		if err := s.App.Decide(r.PathValue("id"), v.Approve, v.Note); err != nil {
			fail(w, err)
			return
		}
		s.state(w)
	})
	mux.HandleFunc("GET /api/artifacts/{id}/{name}", func(w http.ResponseWriter, r *http.Request) {
		data, err := s.App.ReadArtifact(r.PathValue("id"), r.PathValue("name"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if r.URL.Query().Get("inline") == "1" {
			w.Header().Set("Content-Disposition", "inline")
		} else {
			w.Header().Set("Content-Disposition", "attachment; filename=\""+r.PathValue("name")+"\"")
		}
		w.Write(data)
	})
	mux.HandleFunc("POST /api/settings", func(w http.ResponseWriter, r *http.Request) {
		var c core.Settings
		if err := decode(w, r, &c); err != nil {
			fail(w, err)
			return
		}
		if err := s.App.SaveSettings(c); err != nil {
			fail(w, err)
			return
		}
		s.state(w)
	})
	mux.HandleFunc("POST /api/agents/detect", func(w http.ResponseWriter, r *http.Request) { s.App.DetectAgents(); s.state(w) })
	mux.HandleFunc("POST /api/notify/{kind}/test", func(w http.ResponseWriter, r *http.Request) {
		if err := s.App.TestNotify(r.PathValue("kind")); err != nil {
			fail(w, err)
			return
		}
		s.state(w)
	})
	mux.HandleFunc("POST /api/deliveries/{id}/retry", func(w http.ResponseWriter, r *http.Request) {
		if err := s.App.RetryDelivery(r.PathValue("id")); err != nil {
			fail(w, err)
			return
		}
		s.state(w)
	})
	// The system's folder chooser, so nobody has to type a full path; "" when the person cancelled.
	mux.HandleFunc("POST /api/pick", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Title string `json:"title"`
			Start string `json:"start"`
			Files bool   `json:"files"`
		}
		if err := decode(w, r, &in); err != nil {
			fail(w, err)
			return
		}
		path, err := s.App.PickPath(in.Title, in.Start, in.Files)
		if err != nil {
			fail(w, err)
			return
		}
		jsonResponse(w, map[string]string{"path": path})
	})
	mux.HandleFunc("POST /api/sync", func(w http.ResponseWriter, r *http.Request) {
		if err := s.App.SyncTeam(); err != nil {
			fail(w, err)
			return
		}
		s.state(w)
	})
	mux.HandleFunc("POST /api/prune", func(w http.ResponseWriter, r *http.Request) {
		if _, err := s.App.Prune(); err != nil {
			fail(w, err)
			return
		}
		s.state(w)
	})
	mux.HandleFunc("GET /api/export", func(w http.ResponseWriter, r *http.Request) {
		var b strings.Builder
		if err := s.App.Export(&b); err != nil {
			fail(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="loopbee.zip"`)
		io.WriteString(w, b.String())
	})
	mux.HandleFunc("POST /api/archive/import", func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 32<<20))
		if err != nil {
			fail(w, err)
			return
		}
		if err = s.App.ImportArchive(data); err != nil {
			fail(w, err)
			return
		}
		s.state(w)
	})
	assets, _ := fs.Sub(ui.Files, "web")
	mux.Handle("/", http.FileServer(http.FS(assets)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		w.Header().Set("Cache-Control", "no-store")
		if strings.HasSuffix(r.URL.Path, ".js") || strings.HasSuffix(r.URL.Path, ".css") || strings.HasSuffix(r.URL.Path, ".svg") {
			w.Header().Set("Cache-Control", "private, max-age=3600")
		}
		if r.Host != s.Host {
			http.Error(w, "Invalid host", 403)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+s.Host {
			http.Error(w, "Invalid origin", 403)
			return
		}
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			http.Error(w, "Cross-site request blocked", 403)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api/session" {
			cookie, err := r.Cookie("loopbee")
			if err != nil || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(s.Token)) != 1 {
				http.Error(w, "Unauthorized", 401)
				return
			}
		}
		if r.Method != "GET" && r.Method != "HEAD" && r.Method != "DELETE" && !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") && !(r.URL.Path == "/api/archive/import" && r.Header.Get("Content-Type") == "application/zip") {
			http.Error(w, "JSON required", 415)
			return
		}
		mux.ServeHTTP(w, r)
	})
}
func (s *Server) state(w http.ResponseWriter) {
	v, err := s.App.State()
	if err != nil {
		fail(w, err)
		return
	}
	jsonResponse(w, v)
}
func jsonResponse(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(400)
	json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if err := d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("expected one JSON object")
	}
	return nil
}
func Listen(address string) (net.Listener, error) {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return nil, errors.New("server must bind a numeric loopback address")
	}
	return net.Listen("tcp", address)
}
func HTTPServer(handler http.Handler) *http.Server {
	return &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
}
