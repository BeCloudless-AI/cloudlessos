package api

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"

	"github.com/cloudless/orchestrator/internal/catalog"
)

// appViewProxy keeps embedded applications on the Cloudless origin. This is
// required for applications such as n8n that deny cross-origin framing.
func (s *Server) appViewProxy(w http.ResponseWriter, r *http.Request) {
	app, ok := catalog.Get(r.PathValue("id"))
	if !ok || app.EmbeddedPath == "" || !strings.HasPrefix(r.URL.Path, app.EmbeddedPath) {
		http.NotFound(w, r)
		return
	}
	var hostPort int
	for port := range app.Ports {
		hostPort = port
	}
	if hostPort == 0 {
		http.Error(w, "application has no local endpoint", http.StatusBadGateway)
		return
	}
	target := &url.URL{Scheme: "http", Host: "127.0.0.1:" + strconv.Itoa(hostPort)}
	proxy := httputil.NewSingleHostReverseProxy(target)
	direct := proxy.Director
	proxy.Director = func(request *http.Request) {
		direct(request)
		request.URL.Path = appUpstreamPath(request.URL.Path, app.EmbeddedPath)
		request.URL.RawPath = ""
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(w, "application is not ready", http.StatusBadGateway)
	}
	proxy.ServeHTTP(w, r)
}

func appUpstreamPath(requestPath, embeddedPath string) string {
	return "/" + strings.TrimPrefix(strings.TrimPrefix(requestPath, embeddedPath), "/")
}
