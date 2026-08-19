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
	"github.com/cloudless/orchestrator/internal/state"
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

type gatewayKeyWindow struct {
	minute        time.Time
	requests      int
	modelRequests int
	tokens        int64
	modelTokens   int64
	active        int
}

// gatewayKeyQuota enforces user-configured limits without persisting transient
// minute windows. The configuration itself lives with the API key in state.
type gatewayKeyQuota struct {
	mu      sync.Mutex
	windows map[string]gatewayKeyWindow
	now     func() time.Time
}

func newGatewayKeyQuota() *gatewayKeyQuota {
	return &gatewayKeyQuota{windows: map[string]gatewayKeyWindow{}, now: time.Now}
}

func (q *gatewayKeyQuota) current(key string) (gatewayKeyWindow, time.Time) {
	now := q.now()
	minute := now.Truncate(time.Minute)
	window := q.windows[key]
	if window.minute.IsZero() || !window.minute.Equal(minute) {
		active := window.active
		window = gatewayKeyWindow{minute: minute, active: active}
	}
	return window, now
}

func quotaRetryAfter(now, minute time.Time) int {
	return max(1, int(math.Ceil(minute.Add(time.Minute).Sub(now).Seconds())))
}

// begin reserves one request and one parallel slot. A zero limit is unlimited.
func (q *gatewayKeyQuota) begin(key, kind string, limits state.APIKeyLimits) (allowed bool, retryAfter int, detail string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	window, now := q.current(key)
	retry := quotaRetryAfter(now, window.minute)
	if limits.MaxParallelRequests > 0 && window.active >= limits.MaxParallelRequests {
		return false, 1, "parallel request limit exceeded"
	}
	if limits.RequestsPerMinute > 0 && window.requests >= limits.RequestsPerMinute {
		return false, retry, "requests per minute limit exceeded"
	}
	if limits.TokensPerMinute > 0 && window.tokens >= limits.TokensPerMinute {
		return false, retry, "tokens per minute limit exceeded"
	}
	model := state.NormalizeAPIKeyScope(kind) == state.APIKeyScopeModel
	if model && limits.ModelRequestsPerMinute > 0 && window.modelRequests >= limits.ModelRequestsPerMinute {
		return false, retry, "model requests per minute limit exceeded"
	}
	if model && limits.ModelTokensPerMinute > 0 && window.modelTokens >= limits.ModelTokensPerMinute {
		return false, retry, "model tokens per minute limit exceeded"
	}
	window.requests++
	window.active++
	if model {
		window.modelRequests++
	}
	q.windows[key] = window
	return true, 0, ""
}

// complete releases the parallel slot and accounts for actual tokens reported
// by the OpenAI-compatible response. A request that crosses a token threshold
// finishes normally; subsequent requests wait for the next minute window.
func (q *gatewayKeyQuota) complete(key, kind string, promptTokens, completionTokens int64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	window, _ := q.current(key)
	if window.active > 0 {
		window.active--
	}
	tokens := max(int64(0), promptTokens) + max(int64(0), completionTokens)
	window.tokens += tokens
	if state.NormalizeAPIKeyScope(kind) == state.APIKeyScopeModel {
		window.modelTokens += tokens
	}
	q.windows[key] = window
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

func (s *Server) gatewayKeyQuotaManager() *gatewayKeyQuota {
	s.gatewaySecurityMu.Lock()
	defer s.gatewaySecurityMu.Unlock()
	if s.gatewayQuota == nil {
		s.gatewayQuota = newGatewayKeyQuota()
	}
	return s.gatewayQuota
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
