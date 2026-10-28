package main

import (
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/hacka-tron/relay/internal/atomicfile"
	"github.com/hacka-tron/relay/internal/manifest"
	"github.com/hacka-tron/relay/internal/rollout"
)

// The admin page lets an operator do by hand what `release promote` does:
// point a channel at a version, change its percentage, pause it. It also
// shows the releases on disk and the clients that checked in recently.
//
// It is protected by HTTP basic auth with the ADMIN_TOKEN as password and
// is switched off entirely when no token is configured. Browsers send
// basic auth credentials automatically, so state-changing requests also
// require that the form was posted from this site (see sameOrigin).

//go:embed admin.html
var adminHTML string

var adminTmpl = template.Must(template.New("admin").Funcs(template.FuncMap{
	"ago": func(t time.Time) string { return time.Since(t).Truncate(time.Second).String() + " ago" },
}).Parse(adminHTML))

// adminPage is everything the template needs.
type adminPage struct {
	Config   rollout.Config
	Channels []string // sorted channel names, since maps have no order
	Releases []manifest.Release
	Clients  []clientSeen
	Message  string
	Error    string
}

func (h *handler) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.adminToken == "" {
			http.NotFound(w, r)
			return
		}
		_, pass, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(pass), []byte(h.adminToken)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="relay admin"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodPost && !sameOrigin(r) {
			http.Error(w, "cross-site request refused", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

// sameOrigin reports whether a POST came from a page on this server. Every
// current browser sends Origin on form posts; a request without one is
// refused rather than trusted.
func sameOrigin(r *http.Request) bool {
	o, err := url.Parse(r.Header.Get("Origin"))
	return err == nil && o.Host == r.Host
}

func (h *handler) adminGet(w http.ResponseWriter, r *http.Request) {
	h.renderAdmin(w, r.URL.Query().Get("msg"), r.URL.Query().Get("err"))
}

func (h *handler) renderAdmin(w http.ResponseWriter, msg, errMsg string) {
	page := adminPage{Message: msg, Error: errMsg, Clients: h.clients.list()}
	cfg, err := h.store.rollout()
	if err != nil {
		page.Error = "rollout.json: " + err.Error()
	}
	if cfg.Channels == nil {
		cfg.Channels = map[string]rollout.Channel{}
	}
	page.Config = cfg
	for name := range cfg.Channels {
		page.Channels = append(page.Channels, name)
	}
	sort.Strings(page.Channels)
	page.Releases = h.store.releases()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := adminTmpl.Execute(w, page); err != nil {
		h.log.Error("render admin", "err", err)
	}
}

// adminSetChannel handles the promote / pause / percent form. It validates
// the same way `release promote` does and writes rollout.json atomically.
func (h *handler) adminSetChannel(w http.ResponseWriter, r *http.Request) {
	channel := r.FormValue("channel")
	version := r.FormValue("version")
	percent, err := strconv.Atoi(r.FormValue("percent"))
	paused := r.FormValue("paused") == "on"
	switch {
	case channel == "":
		err = errors.New("channel is required")
	case err != nil || percent < 0 || percent > 100:
		err = errors.New("percent must be 0 to 100")
	case !h.store.hasRelease(version):
		err = fmt.Errorf("no release %q on disk", version)
	}
	if err == nil {
		err = h.store.setChannel(channel, rollout.Channel{Version: version, RolloutPercent: percent, Paused: paused})
	}
	if err != nil {
		h.log.Warn("admin set channel refused", "err", err)
		h.redirect(w, r, "", err.Error())
		return
	}
	h.log.Info("admin set channel", "channel", channel, "version", version, "percent", percent, "paused", paused)
	h.redirect(w, r, fmt.Sprintf("%s now offers %s to %d%% of clients", channel, version, percent), "")
}

// adminDeleteChannel removes a channel entirely.
func (h *handler) adminDeleteChannel(w http.ResponseWriter, r *http.Request) {
	channel := r.FormValue("channel")
	if err := h.store.deleteChannel(channel); err != nil {
		h.redirect(w, r, "", err.Error())
		return
	}
	h.log.Info("admin delete channel", "channel", channel)
	h.redirect(w, r, "removed channel "+channel, "")
}

// adminImport pulls a release that CI published to GitHub into the
// releases directory, which is the step between "tagged" and "promotable".
func (h *handler) adminImport(w http.ResponseWriter, r *http.Request) {
	version := r.FormValue("version")
	if h.githubRepo == "" {
		h.redirect(w, r, "", "GITHUB_REPO is not configured on the server")
		return
	}
	if err := h.importRelease(r.Context(), version); err != nil {
		h.log.Warn("admin import failed", "version", version, "err", err)
		h.redirect(w, r, "", err.Error())
		return
	}
	h.log.Info("admin import", "version", version)
	h.redirect(w, r, "imported "+version+" from GitHub; promote it when ready", "")
}

// redirect goes back to the admin page with a message, so a browser
// refresh does not resubmit the form.
func (h *handler) redirect(w http.ResponseWriter, r *http.Request, msg, errMsg string) {
	q := url.Values{}
	if msg != "" {
		q.Set("msg", msg)
	}
	if errMsg != "" {
		q.Set("err", errMsg)
	}
	http.Redirect(w, r, "/admin?"+q.Encode(), http.StatusSeeOther)
}

// Store helpers used only by the admin page.

func (s store) hasRelease(version string) bool {
	_, err := os.Stat(filepath.Join(s.dir, version, "release.json"))
	return err == nil
}

// releases lists every release.json on disk, newest version first.
func (s store) releases() []manifest.Release {
	var out []manifest.Release
	matches, _ := filepath.Glob(filepath.Join(s.dir, "*", "release.json"))
	for _, p := range matches {
		var r manifest.Release
		if readJSON(p, &r) == nil {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version > out[j].Version })
	return out
}

func (s store) setChannel(name string, ch rollout.Channel) error {
	cfg, err := s.rollout()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if cfg.Channels == nil {
		cfg.Channels = map[string]rollout.Channel{}
	}
	cfg.Channels[name] = ch
	return s.saveRollout(cfg)
}

func (s store) deleteChannel(name string) error {
	cfg, err := s.rollout()
	if err != nil {
		return err
	}
	if _, ok := cfg.Channels[name]; !ok {
		return fmt.Errorf("no channel %q", name)
	}
	delete(cfg.Channels, name)
	return s.saveRollout(cfg)
}

func (s store) saveRollout(cfg rollout.Config) error {
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(s.dir, "rollout.json"), append(b, '\n'), 0o644)
}
