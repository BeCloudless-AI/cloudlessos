package api

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudless/orchestrator/internal/recipeops"
)

func immutableRecipeImageReference(reference, digest string) (string, error) {
	reference, digest = strings.TrimSpace(reference), strings.ToLower(strings.TrimSpace(digest))
	if !recipeImageDigestPattern.MatchString(digest) {
		return "", errors.New("runtime image has no immutable SHA-256 identity")
	}
	if reference == "" || strings.ContainsAny(reference, "\x00\r\n\t ") {
		return "", errors.New("runtime image reference is invalid")
	}
	if recipeImageDigestPattern.MatchString(strings.ToLower(reference)) {
		if strings.ToLower(reference) != digest {
			return "", errors.New("runtime image ID disagrees with its prepared digest")
		}
		return digest, nil
	}
	if base, _, found := strings.Cut(reference, "@"); found {
		reference = base
	}
	return reference + "@" + digest, nil
}

func writePreparedRecipeImageReference(checkout string, environment map[string]string, reference string) error {
	path := filepath.Join(checkout, ".env.dspark")
	payload, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	replacements := map[string]string{"DSPARK_VLLM_IMAGE": reference, "ENGINE_IMAGE": reference}
	seen := make(map[string]bool, len(replacements))
	lines := strings.Split(strings.TrimSuffix(string(payload), "\n"), "\n")
	for index, line := range lines {
		key, _, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		if value, ok := replacements[key]; ok {
			lines[index], seen[key] = key+"="+value, true
		}
	}
	for _, key := range []string{"DSPARK_VLLM_IMAGE", "ENGINE_IMAGE"} {
		if !seen[key] {
			lines = append(lines, key+"="+replacements[key])
		}
		environment[key] = reference
	}
	temporary := path + ".image-pin.tmp"
	if err := os.WriteFile(temporary, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		return err
	}
	if err := os.Chmod(temporary, 0o600); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

func (s *Server) bindPreparedRecipeImage(operationID, reference, digest string) (recipeops.Operation, error) {
	if s.recipeOps == nil {
		return recipeops.Operation{}, errors.New("recipe operation journal is unavailable")
	}
	immutableReference, err := immutableRecipeImageReference(reference, digest)
	if err != nil {
		return recipeops.Operation{}, err
	}
	operation, err := s.recipeOps.BindPreparedImage(operationID, immutableReference, digest)
	if err != nil {
		return recipeops.Operation{}, fmt.Errorf("bind immutable prepared image: %w", err)
	}
	return operation, nil
}
