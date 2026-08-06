package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"

	"github.com/cloudless/orchestrator/internal/communityrecipes"
	"github.com/cloudless/orchestrator/internal/localrecipes"
)

const communityTrustKeyringPath = "/usr/share/cloudless/community-keys.json"

var communitySlugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

type communityTrustKey struct {
	ID        string `json:"id"`
	PublicKey string `json:"publicKey"`
}

type communityInstallRequest struct {
	Slug    string `json:"slug"`
	Version string `json:"version"`
}

type communityRollbackRequest struct {
	ID string `json:"id"`
}

func (s *Server) syncCommunityRevocations(r *http.Request) error {
	keys, err := loadCommunityTrustKeys()
	if err != nil {
		return err
	}
	var feed struct {
		Revocations []communityrecipes.Revocation `json:"revocations"`
	}
	if err := s.fetchCommunityJSON(r, "/v1/recipes/trust/revocations", &feed); err != nil {
		return err
	}
	for _, revocation := range feed.Revocations {
		key, ok := keys[revocation.SigningKeyID]
		if !ok || communityrecipes.VerifyRevocation(revocation, key) != nil {
			return errors.New("community revocation feed signature verification failed")
		}
		if err := s.recipes.MarkCommunityRevoked(revocation.RevisionID, revocation.Reason); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func loadCommunityTrustKeys() (map[string]string, error) {
	path := strings.TrimSpace(os.Getenv("CLOUDLESS_COMMUNITY_KEYRING"))
	if path == "" {
		path = communityTrustKeyringPath
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read community trust root: %w", err)
	}
	var document struct {
		Keys []communityTrustKey `json:"keys"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, errors.New("community trust root is malformed")
	}
	keys := make(map[string]string, len(document.Keys))
	for _, key := range document.Keys {
		if key.ID != "" && key.PublicKey != "" {
			keys[key.ID] = key.PublicKey
		}
	}
	if len(keys) == 0 {
		return nil, errors.New("community trust root contains no keys")
	}
	return keys, nil
}

func (s *Server) fetchCommunityJSON(r *http.Request, path string, target any) error {
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, s.accountAPIBase()+path, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "CloudlessOS/community-installer")
	response, err := s.accountClient().Do(request)
	if err != nil {
		return fmt.Errorf("reach community service: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("community service returned HTTP %d", response.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, communityrecipes.MaxReleaseBytes+(64<<10)))
	if err := decoder.Decode(target); err != nil {
		return errors.New("community service returned invalid JSON")
	}
	return nil
}

func (s *Server) communityRecipeInstall(w http.ResponseWriter, r *http.Request) {
	var input communityInstallRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	input.Slug, input.Version = strings.TrimSpace(input.Slug), strings.TrimSpace(input.Version)
	if !communitySlugPattern.MatchString(input.Slug) || len(input.Slug) > 80 || input.Version == "" || len(input.Version) > 64 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "a valid recipe slug and exact version are required"})
		return
	}

	var remote struct {
		Recipe struct {
			Slug string `json:"slug"`
		} `json:"recipe"`
		Release    json.RawMessage                   `json:"release"`
		Validation *localrecipes.CommunityValidation `json:"validation"`
	}
	if err := s.fetchCommunityJSON(r, "/v1/recipes/"+input.Slug+"/revisions/"+input.Version, &remote); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	keys, err := loadCommunityTrustKeys()
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}
	var unsigned communityrecipes.Release
	if err := json.Unmarshal(remote.Release, &unsigned); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "community release envelope is malformed"})
		return
	}
	publicKey, trusted := keys[unsigned.SigningKeyID]
	if !trusted {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "community release uses an unknown signing key"})
		return
	}
	release, err := communityrecipes.ParseAndVerify(remote.Release, publicKey)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	if release.Version != input.Version || remote.Recipe.Slug != input.Slug {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "community release identity does not match the request"})
		return
	}
	if err := communityrecipes.ValidateManagedManifest(release.Manifest); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}

	var revocationFeed struct {
		Revocations []communityrecipes.Revocation `json:"revocations"`
	}
	if err := s.fetchCommunityJSON(r, "/v1/recipes/trust/revocations", &revocationFeed); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "revocation status could not be checked; installation was not changed"})
		return
	}
	for _, revocation := range revocationFeed.Revocations {
		key, ok := keys[revocation.SigningKeyID]
		if !ok || communityrecipes.VerifyRevocation(revocation, key) != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "revocation feed signature verification failed; installation was not changed"})
			return
		}
		if revocation.RevisionID == release.RevisionID {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "this community recipe revision was revoked: " + revocation.Reason})
			return
		}
	}

	manifestJSON, err := json.Marshal(release.Manifest)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "community manifest could not be decoded"})
		return
	}
	_, draft, err := localrecipes.ParseNativeManifest(manifestJSON)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	installed, err := s.recipes.InstallCommunity(draft, localrecipes.CommunityProvenance{
		RecipeID: release.RecipeID, RevisionID: release.RevisionID, Slug: input.Slug,
		Version: release.Version, Digest: release.ManifestDigest, SigningKeyID: release.SigningKeyID,
		Validation: remote.Validation,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.auditSecurity(gatewayAuditEvent{Category: "recipe", Event: "community.install", Outcome: "succeeded", Actor: "local-ui", Target: installed.ID})
	writeJSON(w, http.StatusCreated, map[string]any{"recipe": installed, "verified": true})
}

func (s *Server) communityRecipeRollback(w http.ResponseWriter, r *http.Request) {
	var input communityRollbackRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || !localRecipeIDPattern.MatchString(input.ID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "a valid installed community recipe ID is required"})
		return
	}
	state := s.state.Get()
	if state.LocalRecipeID == input.ID && !state.EngineUnloaded {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "stop the running recipe before rolling it back"})
		return
	}
	var restored localrecipes.Recipe
	err := s.executeRecipeMutation(input.ID, func() error { var err error; restored, err = s.recipes.RollbackCommunity(input.ID); return err })
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	s.auditSecurity(gatewayAuditEvent{Category: "recipe", Event: "community.rollback", Outcome: "succeeded", Actor: "local-ui", Target: restored.ID})
	writeJSON(w, http.StatusOK, map[string]any{"recipe": restored})
}
