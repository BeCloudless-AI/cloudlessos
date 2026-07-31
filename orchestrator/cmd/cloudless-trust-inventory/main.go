// Command cloudless-trust-inventory emits the canonical offline inventory of
// reviewed recipe and distributed compatibility metadata in this source tree.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"

	"github.com/cloudless/orchestrator/internal/distributedprofiles"
	"github.com/cloudless/orchestrator/internal/localrecipes"
)

type distributedEntry struct {
	Profile distributedprofiles.Profile `json:"profile"`
	Digest  string                      `json:"metadataDigest"`
}

type inventory struct {
	Schema                   string                `json:"schema"`
	SourceCommit             string                `json:"sourceCommit"`
	ArchiveKeyFingerprint    string                `json:"archiveKeyFingerprint"`
	ReviewedRecipes          []localrecipes.Review `json:"reviewedRecipes"`
	DistributedCompatibility []distributedEntry    `json:"distributedCompatibility"`
}

func main() {
	sourceCommit := flag.String("source-commit", "", "full source commit represented by this inventory")
	flag.Parse()
	if len(*sourceCommit) != 40 {
		fmt.Fprintln(os.Stderr, "a full 40-character source commit is required")
		os.Exit(2)
	}
	recipes := localrecipes.ReviewInventory()
	sort.Slice(recipes, func(i, j int) bool { return recipes[i].RecipeID < recipes[j].RecipeID })
	distributed := make([]distributedEntry, 0)
	for _, profile := range distributedprofiles.All() {
		if err := distributedprofiles.Validate(profile); err != nil {
			fmt.Fprintf(os.Stderr, "invalid reviewed distributed profile %s: %v\n", profile.ID, err)
			os.Exit(1)
		}
		canonical, err := json.Marshal(profile)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		sum := sha256.Sum256(canonical)
		distributed = append(distributed, distributedEntry{
			Profile: profile, Digest: "sha256:" + hex.EncodeToString(sum[:]),
		})
	}
	sort.Slice(distributed, func(i, j int) bool {
		return distributed[i].Profile.ID < distributed[j].Profile.ID
	})
	doc := inventory{
		Schema: "cloudless.trust-inventory.v1", SourceCommit: *sourceCommit,
		ArchiveKeyFingerprint: localrecipes.CloudlessArchiveFingerprint,
		ReviewedRecipes:       recipes, DistributedCompatibility: distributed,
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(doc); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
