package api

import (
	"math"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/cloudless/orchestrator/internal/jobs"
	"github.com/cloudless/orchestrator/internal/securityaudit"
)

const (
	gatewayRequestsPerMinute = 60
	gatewayBurst             = 15
)

type gatewayRateBucket struct {
	tokens float64
	at     time.Time
}

type gatewayRateLimiter struct {
	mu      sync.Mutex
	buckets map[string]gatewayRateBucket
	now     func() time.Time
	rate    float64
	burst   float64
}

func newGatewayRateLimiter() *gatewayRateLimiter {
	return newGatewayRateLimiterWith(gatewayRequestsPerMinute, gatewayBurst)
}

func newGatewayRateLimiterWith(requestsPerMinute, burst int) *gatewayRateLimiter {
	return &gatewayRateLimiter{
		buckets: map[string]gatewayRateBucket{}, now: time.Now,
		rate: float64(requestsPerMinute), burst: float64(burst),
	}
}

func (l *gatewayRateLimiter) allow(key string) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	bucket, ok := l.buckets[key]
	if !ok {
		bucket = gatewayRateBucket{tokens: l.burst, at: now}
	}
	elapsed := now.Sub(bucket.at).Seconds()
	bucket.tokens = math.Min(l.burst, bucket.tokens+elapsed*l.rate/60)
	bucket.at = now
	if bucket.tokens < 1 {
		l.buckets[key] = bucket
		wait := int(math.Ceil((1 - bucket.tokens) / (l.rate / 60)))
		return false, max(1, wait)
	}
	bucket.tokens--
	l.buckets[key] = bucket
	return true, 0
}

type gatewayAuditEvent = securityaudit.Event
type gatewayAuditLog = securityaudit.Log

func newGatewayAuditLog(dir string) *gatewayAuditLog { return securityaudit.New(dir) }

func gatewaySource(remoteAddr string) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(remoteAddr))
	if err == nil {
		return host
	}
	return strings.TrimSpace(remoteAddr)
}

func gatewayRequestSource(request *http.Request) string {
	source := gatewaySource(request.RemoteAddr)
	if address := net.ParseIP(source); address != nil && address.IsLoopback() {
		if forwarded := strings.TrimSpace(request.Header.Get("CF-Connecting-IP")); forwarded != "" {
			return forwarded
		}
		if forwarded := strings.TrimSpace(strings.Split(request.Header.Get("X-Forwarded-For"), ",")[0]); forwarded != "" {
			return forwarded
		}
	}
	return source
}

func (s *Server) gatewaySecurity() (*gatewayRateLimiter, *gatewayAuditLog) {
	s.gatewaySecurityMu.Lock()
	defer s.gatewaySecurityMu.Unlock()
	if s.gatewayLimiter == nil {
		s.gatewayLimiter = newGatewayRateLimiter()
	}
	if s.gatewaySourceLimiter == nil {
		s.gatewaySourceLimiter = newGatewayRateLimiterWith(120, 30)
	}
	if s.gatewayAudit == nil && s.state != nil {
		s.gatewayAudit = newGatewayAuditLog(s.state.Dir())
	}
	return s.gatewayLimiter, s.gatewayAudit
}

func (s *Server) gatewaySourceRateLimiter() *gatewayRateLimiter {
	s.gatewaySecurityMu.Lock()
	defer s.gatewaySecurityMu.Unlock()
	if s.gatewaySourceLimiter == nil {
		s.gatewaySourceLimiter = newGatewayRateLimiterWith(120, 30)
	}
	return s.gatewaySourceLimiter
}

func (s *Server) auditGateway(event gatewayAuditEvent) {
	event.Category = "gateway"
	s.auditSecurity(event)
}

func (s *Server) auditSecurity(event gatewayAuditEvent) {
	_, audit := s.gatewaySecurity()
	if audit == nil {
		return
	}
	audit.Append(event)
}

func (s *Server) auditMutation(r *http.Request, category, event, target, outcome, detail string, status int) {
	record := gatewayAuditEvent{
		Category: category,
		Event:    event,
		Outcome:  outcome,
		Actor:    "local-ui",
		Target:   target,
		Status:   status,
		Detail:   detail,
	}
	if r != nil {
		record.Method = r.Method
		record.Path = r.URL.Path
		record.Source = gatewayRequestSource(r)
	}
	s.auditSecurity(record)
}

func (s *Server) auditJob(job *jobs.Job, category, event, target string) {
	if job == nil {
		return
	}
	s.auditSecurity(gatewayAuditEvent{
		Category: category, Event: event, Outcome: "started", Actor: "local-ui",
		Target: target, OperationID: job.ID,
	})
	var once sync.Once
	job.Observe(func(update jobs.Update) {
		if !update.Done {
			return
		}
		once.Do(func() {
			outcome := "succeeded"
			if update.Error != "" || update.Phase == "error" {
				outcome = "failed"
			}
			s.auditSecurity(gatewayAuditEvent{
				Category: category, Event: event, Outcome: outcome, Actor: "cloudlessd",
				Target: target, OperationID: job.ID, Detail: update.Error,
			})
		})
	})
}
