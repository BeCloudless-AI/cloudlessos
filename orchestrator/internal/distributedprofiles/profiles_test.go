package distributedprofiles

import (
	"strings"
	"testing"

	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/models"
)

func TestReviewedProfilesAreImmutableAndValid(t *testing.T) {
	if len(All()) == 0 {
		t.Fatal("reviewed profile library is empty")
	}
	for _, profile := range All() {
		if err := Validate(profile); err != nil {
			t.Fatalf("%s: %v", profile.ID, err)
		}
	}
}

func TestResolveRequiresExactArtifactRuntimeAndTopology(t *testing.T) {
	model, ok := models.Get("Qwen/Qwen3.6-35B-A3B")
	if !ok {
		t.Fatal("reviewed model missing")
	}
	base := Environment{Engine: "vllm", Architecture: "arm64", Platform: "dgx-spark", MemoryType: "unified", Nodes: 2}
	if _, err := Resolve(model, base); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Environment){
		"unqualified node count": func(env *Environment) { env.Nodes = 3 },
		"wrong engine":           func(env *Environment) { env.Engine = "sglang" },
		"wrong architecture":     func(env *Environment) { env.Architecture = "amd64" },
		"wrong platform":         func(env *Environment) { env.Platform = "generic" },
	} {
		t.Run(name, func(t *testing.T) {
			env := base
			mutate(&env)
			if _, err := Resolve(model, env); err == nil {
				t.Fatal("unreviewed environment was accepted")
			}
		})
	}
	model.Revision = strings.Repeat("a", 40)
	if _, err := Resolve(model, base); err == nil {
		t.Fatal("wrong model revision was accepted")
	}
}

func TestApplyPinsRevisionContextAndStableAlias(t *testing.T) {
	profile := All()[0]
	spec, err := Apply(engine.RunSpec{Args: []string{"vllm", "serve", profile.ModelID, "--served-model-name", "wrong"}}, profile)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(spec.Args, " ")
	for _, want := range []string{"--revision " + profile.ModelRevision, "--max-model-len 32768", "--served-model-name cloudless"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("launch contract missing %q: %s", want, joined)
		}
	}
}
