// Package communityrecipes verifies immutable community recipe releases.
package communityrecipes

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	ReleaseSchema          = "cloudless.recipe.release/v1"
	ManifestSchema         = "cloudless.recipe/v1"
	AdvancedManifestSchema = "cloudless.recipe/v2"
	MaxReleaseBytes        = 2 << 20
)

// Release is deliberately strict: signed executable metadata must not acquire
// ignored fields whose meaning differs between the service and the OS.
type Release struct {
	Schema         string         `json:"schema"`
	RecipeID       string         `json:"recipeId"`
	RevisionID     string         `json:"revisionId"`
	Version        string         `json:"version"`
	PublisherID    string         `json:"publisherId"`
	ManifestDigest string         `json:"manifestDigest"`
	SigningKeyID   string         `json:"signingKeyId"`
	Manifest       map[string]any `json:"manifest"`
	Signature      string         `json:"signature,omitempty"`
}

type Revocation struct {
	Schema       string `json:"schema"`
	RevisionID   string `json:"revisionId"`
	Reason       string `json:"reason"`
	SigningKeyID string `json:"signingKeyId"`
	CreatedAt    string `json:"createdAt"`
	Signature    string `json:"signature,omitempty"`
}

func publicKey(publicKeyBase64 string) (ed25519.PublicKey, error) {
	publicDER, err := base64.StdEncoding.DecodeString(publicKeyBase64)
	if err != nil {
		return nil, errors.New("invalid community public key encoding")
	}
	prefix, _ := hex.DecodeString("302a300506032b6570032100")
	if len(publicDER) != len(prefix)+ed25519.PublicKeySize || !bytes.Equal(publicDER[:len(prefix)], prefix) {
		return nil, errors.New("invalid Ed25519 SPKI public key")
	}
	return ed25519.PublicKey(publicDER[len(prefix):]), nil
}

func Canonical(value any) ([]byte, error) {
	// Normalize structs and typed values through JSON before encoding. The
	// community service signs RFC-style canonical objects with keys sorted at
	// every level; encoding a Go struct directly would preserve declaration
	// order instead and reject an otherwise valid signature.
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var normalized any
	if err := decoder.Decode(&normalized); err != nil {
		return nil, err
	}

	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(normalized); err != nil {
		return nil, err
	}
	encoded := bytes.TrimSuffix(buffer.Bytes(), []byte("\n"))
	encoded = bytes.ReplaceAll(encoded, []byte(`\u2028`), []byte("\u2028"))
	encoded = bytes.ReplaceAll(encoded, []byte(`\u2029`), []byte("\u2029"))
	if !json.Valid(encoded) {
		return nil, errors.New("canonical encoder produced invalid JSON")
	}
	return encoded, nil
}

func Digest(value any) (string, []byte, error) {
	canonical, err := Canonical(value)
	if err != nil {
		return "", nil, err
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:]), canonical, nil
}

func ParseAndVerify(raw []byte, publicKeyBase64 string) (*Release, error) {
	if len(raw) == 0 || len(raw) > MaxReleaseBytes {
		return nil, fmt.Errorf("community release size must be between 1 and %d bytes", MaxReleaseBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	var release Release
	if err := decoder.Decode(&release); err != nil {
		return nil, fmt.Errorf("decode community release: %w", err)
	}
	manifestSchema, _ := release.Manifest["schema"].(string)
	if release.Schema != ReleaseSchema || (manifestSchema != ManifestSchema && manifestSchema != AdvancedManifestSchema) {
		return nil, errors.New("unsupported community recipe schema")
	}
	signature, err := base64.StdEncoding.DecodeString(release.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return nil, errors.New("invalid community recipe signature encoding")
	}
	key, err := publicKey(publicKeyBase64)
	if err != nil {
		return nil, err
	}
	providedDigest := release.ManifestDigest
	wantDigest, _, err := Digest(release.Manifest)
	if err != nil || !strings.EqualFold(providedDigest, wantDigest) {
		return nil, errors.New("community manifest digest mismatch")
	}
	release.Signature = ""
	canonical, err := Canonical(release)
	if err != nil {
		return nil, err
	}
	if !ed25519.Verify(key, canonical, signature) {
		return nil, errors.New("community recipe signature verification failed")
	}
	release.Signature = base64.StdEncoding.EncodeToString(signature)
	return &release, nil
}

func VerifyRevocation(revocation Revocation, publicKeyBase64 string) error {
	if revocation.Schema != "cloudless.recipe.revocation/v1" || strings.TrimSpace(revocation.RevisionID) == "" || strings.TrimSpace(revocation.Reason) == "" || strings.TrimSpace(revocation.CreatedAt) == "" {
		return errors.New("invalid community revocation envelope")
	}
	signature, err := base64.StdEncoding.DecodeString(revocation.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return errors.New("invalid community revocation signature encoding")
	}
	key, err := publicKey(publicKeyBase64)
	if err != nil {
		return err
	}
	revocation.Signature = ""
	canonical, err := Canonical(revocation)
	if err != nil {
		return err
	}
	if !ed25519.Verify(key, canonical, signature) {
		return errors.New("community revocation signature verification failed")
	}
	return nil
}
