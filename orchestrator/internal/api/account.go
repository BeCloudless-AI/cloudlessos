package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const defaultCloudlessAccountAPI = "https://becloudless.ai/api"

func (s *Server) accountAPIBase() string {
	if base := strings.TrimRight(strings.TrimSpace(s.accountBaseURL), "/"); base != "" {
		return base
	}
	if base := strings.TrimRight(strings.TrimSpace(os.Getenv("CLOUDLESS_ACCOUNT_API")), "/"); base != "" {
		return base
	}
	return defaultCloudlessAccountAPI
}

func (s *Server) accountClient() *http.Client {
	if s.accountHTTPClient != nil {
		return s.accountHTTPClient
	}
	return &http.Client{Timeout: 20 * time.Second}
}

func accountOAuthRedirect(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "http" || parsed.Host != "127.0.0.1:8765" || parsed.Path != "/auth/callback" {
		return "", errors.New("OAuth must return to the local CloudlessOS callback")
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func (s *Server) accountForward(w http.ResponseWriter, r *http.Request, remotePath string, maxBody int64) {
	var body io.Reader
	if r.Body != nil {
		body = http.MaxBytesReader(w, r.Body, maxBody)
	}
	request, err := http.NewRequestWithContext(r.Context(), r.Method, s.accountAPIBase()+remotePath, body)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "could not prepare the Cloudless account request"})
		return
	}
	for _, header := range []string{"Authorization", "Content-Type", "Accept", "Idempotency-Key"} {
		if value := r.Header.Get(header); value != "" {
			request.Header.Set(header, value)
		}
	}
	request.Header.Set("User-Agent", "CloudlessOS/account-client")
	response, err := s.accountClient().Do(request)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "could not reach the Cloudless account service"})
		return
	}
	defer response.Body.Close()
	w.Header().Set("Cache-Control", "no-store")
	if contentType := response.Header.Get("Content-Type"); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(response.Body, 8<<20))
}

func (s *Server) communityRecipes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost && r.Method != http.MethodPut && r.Method != http.MethodPatch && r.Method != http.MethodDelete {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "unsupported community recipe method"})
		return
	}
	rest := strings.TrimPrefix(r.PathValue("rest"), "/")
	remotePath := "/v1/recipes"
	if rest != "" {
		remotePath += "/" + rest
	}
	if r.URL.RawQuery != "" {
		remotePath += "?" + r.URL.RawQuery
	}
	s.accountForward(w, r, remotePath, 2<<20)
}

func (s *Server) communityModeration(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "unsupported community moderation method"})
		return
	}
	rest := strings.TrimPrefix(r.PathValue("rest"), "/")
	remotePath := "/v1/moderation"
	if rest != "" {
		remotePath += "/" + rest
	}
	if r.URL.RawQuery != "" {
		remotePath += "?" + r.URL.RawQuery
	}
	s.accountForward(w, r, remotePath, 512<<10)
}

func (s *Server) communitySocial(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPut && r.Method != http.MethodDelete {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "unsupported community method"})
		return
	}
	rest := strings.TrimPrefix(r.PathValue("rest"), "/")
	remotePath := "/v1/community"
	if rest != "" {
		remotePath += "/" + rest
	}
	if r.URL.RawQuery != "" {
		remotePath += "?" + r.URL.RawQuery
	}
	s.accountForward(w, r, remotePath, 128<<10)
}

func (s *Server) accountSignup(w http.ResponseWriter, r *http.Request) {
	s.accountForward(w, r, "/auth/signup", 64<<10)
}

func (s *Server) accountLogin(w http.ResponseWriter, r *http.Request) {
	s.accountForward(w, r, "/auth/login", 64<<10)
}

func (s *Server) accountRefresh(w http.ResponseWriter, r *http.Request) {
	s.accountForward(w, r, "/auth/refresh", 64<<10)
}

func (s *Server) accountCurrent(w http.ResponseWriter, r *http.Request) {
	s.accountForward(w, r, "/auth/me", 1)
}

func (s *Server) accountLogout(w http.ResponseWriter, r *http.Request) {
	s.accountForward(w, r, "/auth/logout", 1)
}

func (s *Server) accountOAuth(w http.ResponseWriter, r *http.Request) {
	provider := r.PathValue("provider")
	if provider != "google" && provider != "github" && provider != "x" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported OAuth provider"})
		return
	}
	redirectTo, err := accountOAuthRedirect(r.URL.Query().Get("redirectTo"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	query := url.Values{"redirectTo": []string{redirectTo}}
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, s.accountAPIBase()+"/auth/oauth/"+provider+"?"+query.Encode(), nil)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "could not prepare social sign-in"})
		return
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "CloudlessOS/account-client")
	response, err := s.accountClient().Do(request)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "could not reach the Cloudless account service"})
		return
	}
	defer response.Body.Close()
	var payload struct {
		Provider string `json:"provider"`
		URL      string `json:"url"`
		Error    string `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "the Cloudless account service returned an invalid response"})
		return
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if payload.Error == "" {
			payload.Error = "social sign-in is unavailable"
		}
		writeJSON(w, response.StatusCode, map[string]string{"error": payload.Error})
		return
	}
	authorizationURL, err := url.Parse(payload.URL)
	if err != nil || authorizationURL.Scheme != "https" || authorizationURL.Host != "kaftyfeclcciwwmykbms.supabase.co" {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "the account service returned an untrusted authorization URL"})
		return
	}
	authorizationQuery := authorizationURL.Query()
	authorizationQuery.Set("redirect_to", redirectTo)
	authorizationURL.RawQuery = authorizationQuery.Encode()
	writeJSON(w, http.StatusOK, map[string]string{"provider": provider, "url": authorizationURL.String()})
}

func (s *Server) accountPicture(w http.ResponseWriter, r *http.Request) {
	s.accountForward(w, r, "/profile/picture", 6<<20)
}

func (s *Server) accountPublisherKeys(w http.ResponseWriter, r *http.Request) {
	path := "/keys"
	if id := strings.TrimSpace(r.PathValue("id")); id != "" {
		path += "/" + url.PathEscape(id)
	}
	s.accountForward(w, r, path, 32<<10)
}
