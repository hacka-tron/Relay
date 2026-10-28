// Command updateserver answers update checks and serves release binaries.
//
//	GET /v1/update?client_id=..&version=..&os=..&arch=..&channel=..
//	    200 with a signed manifest JSON, or 204 when there is nothing to do
//	GET /files/<version>/<binary>
//	GET /healthz
//
// The server holds no secrets. It only decides which already signed
// manifest to hand out, so a compromised server can withhold updates but
// cannot forge one.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/hacka-tron/relay/internal/rollout"
)

func main() {
	addr := flag.String("addr", ":"+envOr("PORT", "8080"), "listen address")
	dir := flag.String("releases", envOr("RELEASES_DIR", "releases"), "directory holding rollout.json and release folders")
	baseURL := flag.String("base-url", envOr("BASE_URL", "http://localhost:8080"), "public URL of this server, used to build download links")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	srv := &http.Server{
		Addr:              *addr,
		Handler:           newHandler(store{dir: *dir}, *baseURL, logger),
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Stop cleanly on Ctrl-C or SIGTERM (what Cloud Run and Docker send):
	// finish in-flight requests, then exit.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	logger.Info("listening", "addr", *addr, "releases", *dir, "base_url", *baseURL)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

type handler struct {
	store   store
	baseURL string
	log     *slog.Logger
}

func newHandler(s store, baseURL string, log *slog.Logger) http.Handler {
	h := &handler{store: s, baseURL: strings.TrimRight(baseURL, "/"), log: log}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/update", h.update)
	mux.Handle("GET /files/", http.StripPrefix("/files/", noDirListing(http.FileServer(http.Dir(s.dir)))))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok\n"))
	})
	return mux
}

// update is the whole check flow: read the client's facts, ask rollout
// which version it should get, load that version's manifest for the
// client's platform, make the download URL absolute, and reply.
func (h *handler) update(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := rollout.Facts{
		ClientID: q.Get("client_id"),
		Version:  q.Get("version"),
		OS:       q.Get("os"),
		Arch:     q.Get("arch"),
		Channel:  q.Get("channel"),
	}
	if f.Channel == "" {
		f.Channel = "stable"
	}
	if f.ClientID == "" || f.Version == "" || f.OS == "" || f.Arch == "" {
		http.Error(w, "client_id, version, os and arch are required", http.StatusBadRequest)
		return
	}
	log := h.log.With("client_id", f.ClientID, "version", f.Version, "os", f.OS, "arch", f.Arch, "channel", f.Channel)

	cfg, err := h.store.rollout()
	if err != nil {
		log.Error("read rollout.json", "err", err)
		http.Error(w, "rollout config unavailable", http.StatusInternalServerError)
		return
	}
	target, ok := rollout.Decide(f, cfg)
	if !ok {
		log.Info("no update")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	rel, err := h.store.release(target)
	if err != nil {
		log.Error("read release.json", "target", target, "err", err)
		http.Error(w, "release unavailable", http.StatusInternalServerError)
		return
	}
	m, ok := rel.Target(f.OS, f.Arch)
	if !ok {
		log.Info("no build for platform", "target", target)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	m.URL = h.absoluteURL(target, m.URL)

	log.Info("offer", "target", target, "url", m.URL)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(m)
}

// absoluteURL turns the URL stored in release.json into something the
// client can fetch. The release tool writes just the file name, and the
// server prefixes its own /files/<version>/ path. If release.json already
// holds an absolute URL (say, an S3 or GitHub Releases link) it is used as
// is, so moving binary storage is a release tool change, not a server one.
func (h *handler) absoluteURL(version, stored string) string {
	if u, err := url.Parse(stored); err == nil && u.IsAbs() {
		return stored
	}
	return h.baseURL + "/files/" + version + "/" + stored
}

// noDirListing serves files but answers 404 for directories, so the
// releases tree cannot be browsed.
func noDirListing(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") || r.URL.Path == "" {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}
