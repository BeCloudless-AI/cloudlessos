package api

import (
	"context"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/cloudless/orchestrator/internal/provision"
)

// internetProbes are well-known, almost-always-up endpoints. A successful TCP
// connect to any one means the machine has a working route to the internet.
var internetProbes = []string{"1.1.1.1:443", "8.8.8.8:53", "1.1.1.1:53"}

// internetReachable reports whether any probe connects within the deadline.
func internetReachable(ctx context.Context) bool {
	type res struct{ ok bool }
	ch := make(chan res, len(internetProbes))
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	for _, addr := range internetProbes {
		go func(a string) {
			d := net.Dialer{}
			c, err := d.DialContext(cctx, "tcp", a)
			if err == nil {
				_ = c.Close()
			}
			ch <- res{ok: err == nil}
		}(addr)
	}
	for range internetProbes {
		select {
		case r := <-ch:
			if r.ok {
				return true
			}
		case <-cctx.Done():
			return false
		}
	}
	return false
}

// connectivity caches the internet check briefly so polling stays snappy.
var connectivity struct {
	mu       sync.Mutex
	internet bool
	at       time.Time
}

func cachedInternet(ctx context.Context) bool {
	connectivity.mu.Lock()
	fresh := !connectivity.at.IsZero() && time.Since(connectivity.at) < 12*time.Second
	val := connectivity.internet
	connectivity.mu.Unlock()
	if fresh {
		return val
	}
	ok := internetReachable(ctx)
	connectivity.mu.Lock()
	connectivity.internet, connectivity.at = ok, time.Now()
	connectivity.mu.Unlock()
	return ok
}

// networkStatus reports the machine's connectivity: internet, local-network-only,
// or offline — so the UI can explain what's reachable.
func (s *Server) networkStatus(w http.ResponseWriter, r *http.Request) {
	ip := provision.PrimaryLANIP()
	lan := ip != ""
	internet := cachedInternet(r.Context())
	state := "offline"
	if internet {
		state = "internet"
	} else if lan {
		state = "local"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"state": state, "internet": internet, "lan": lan, "ip": ip,
	})
}
