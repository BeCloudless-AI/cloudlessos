package api

import (
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
)

const terminalUpstream = "http://127.0.0.1:7681"

// terminalProxyHandler keeps the system terminal inside the Cloudless origin.
// ttyd itself only listens on loopback and starts /bin/login, so reaching this
// endpoint never bypasses the machine's normal Linux authentication.
func terminalProxyHandler(upstream *url.URL) http.Handler {
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(w, "The Cloudless terminal is not available yet.", http.StatusServiceUnavailable)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil || !net.ParseIP(host).IsLoopback() {
			http.Error(w, "The system terminal is only available on this machine.", http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		proxy.ServeHTTP(w, r)
	})
}

func cloudlessTerminalProxy() http.Handler {
	target, err := url.Parse(terminalUpstream)
	if err != nil {
		panic(err)
	}
	return terminalProxyHandler(target)
}
