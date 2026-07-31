// Package distributedprofiles contains the small, reviewed allow-list of
// distributed model launch contracts CloudlessOS may start automatically.
// Arbitrary recipes remain an advanced, separately reviewed workflow.
package distributedprofiles

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/models"
)

const SchemaVersion = 1

type Environment struct {
	Engine       string
	Architecture string
	Platform     string
	MemoryType   string
	Nodes        int
}

type Profile struct {
	SchemaVersion   int    `json:"schemaVersion"`
	ID              string `json:"id"`
	ModelID         string `json:"modelId"`
	ModelRevision   string `json:"modelRevision"`
	Engine          string `json:"engine"`
	Architecture    string `json:"architecture"`
	Platform        string `json:"platform"`
	MemoryType      string `json:"memoryType"`
	MinNodes        int    `json:"minNodes"`
	MaxNodes        int    `json:"maxNodes"`
	Backend         string `json:"backend"`
	Fabric          string `json:"fabric"`
	GPUsPerNode     int    `json:"gpusPerNode"`
	ContextTokens   int    `json:"contextTokens"`
	ServedModelName string `json:"servedModelName"`
	Evidence        string `json:"evidence"`
	Source          string `json:"source"`
}

var reviewed = []Profile{
	{
		SchemaVersion: SchemaVersion,
		ID:            "qwen36-35b-a3b-dgx-spark-pair-v1",
		ModelID:       "Qwen/Qwen3.6-35B-A3B",
		ModelRevision: "995ad96eacd98c81ed38be0c5b274b04031597b0",
		Engine:        "vllm", Architecture: "arm64", Platform: "dgx-spark", MemoryType: "unified",
		MinNodes: 2, MaxNodes: 2, Backend: "ray", Fabric: "roce-v2-dual-path",
		GPUsPerNode: 1, ContextTokens: 32768, ServedModelName: "cloudless",
		Evidence: "measured", Source: "CloudlessOS DGX Spark pair qualification",
	},
}

func All() []Profile {
	result := make([]Profile, len(reviewed))
	copy(result, reviewed)
	return result
}

func Validate(profile Profile) error {
	switch {
	case profile.SchemaVersion != SchemaVersion:
		return fmt.Errorf("unsupported distributed profile schema %d", profile.SchemaVersion)
	case strings.TrimSpace(profile.ID) == "", strings.TrimSpace(profile.ModelID) == "":
		return errors.New("distributed profile identity is incomplete")
	case len(profile.ModelRevision) != 40:
		return errors.New("distributed profile model revision must be an immutable 40-character commit")
	case profile.Engine == "", profile.Architecture == "", profile.Platform == "", profile.MemoryType == "":
		return errors.New("distributed profile runtime envelope is incomplete")
	case profile.MinNodes < 2, profile.MaxNodes < profile.MinNodes, profile.MaxNodes > 8:
		return errors.New("distributed profile node range must be between two and eight")
	case profile.Backend != "ray":
		return errors.New("unsupported distributed backend")
	case profile.Fabric != "roce-v2-dual-path":
		return errors.New("unsupported distributed fabric")
	case profile.GPUsPerNode != 1:
		return errors.New("DGX Spark profiles require exactly one GB10 accelerator per node")
	case profile.ContextTokens < 1024:
		return errors.New("distributed profile context is invalid")
	case profile.ServedModelName != "cloudless":
		return errors.New("distributed profile must preserve the stable Cloudless model alias")
	case profile.Evidence != "measured":
		return errors.New("automatic distributed profiles require measured evidence")
	}
	return nil
}

func Resolve(model models.Model, environment Environment) (Profile, error) {
	for _, profile := range reviewed {
		if profile.ModelID != model.ID ||
			!strings.EqualFold(profile.Engine, environment.Engine) ||
			!strings.EqualFold(profile.Architecture, environment.Architecture) ||
			!strings.EqualFold(profile.Platform, environment.Platform) ||
			!strings.EqualFold(profile.MemoryType, environment.MemoryType) ||
			environment.Nodes < profile.MinNodes || environment.Nodes > profile.MaxNodes {
			continue
		}
		if err := Validate(profile); err != nil {
			return Profile{}, err
		}
		if model.Revision == "" || model.Revision != profile.ModelRevision {
			return Profile{}, errors.New("the model catalog revision does not match the reviewed distributed profile")
		}
		return profile, nil
	}
	return Profile{}, fmt.Errorf(
		"no measured distributed profile matches %s on %d %s nodes with %s",
		model.ID, environment.Nodes, environment.Platform, environment.Engine,
	)
}

func Apply(spec engine.RunSpec, profile Profile) (engine.RunSpec, error) {
	if err := Validate(profile); err != nil {
		return engine.RunSpec{}, err
	}
	args := append([]string(nil), spec.Args...)
	args = setFlag(args, "--revision", profile.ModelRevision)
	args = setFlag(args, "--max-model-len", strconv.Itoa(profile.ContextTokens))
	args = setFlag(args, "--served-model-name", profile.ServedModelName)
	spec.Args = args
	return spec, nil
}

func setFlag(args []string, flag, value string) []string {
	for index := 0; index < len(args); index++ {
		if args[index] == flag {
			if index+1 < len(args) {
				args[index+1] = value
			} else {
				args = append(args, value)
			}
			return args
		}
		if strings.HasPrefix(args[index], flag+"=") {
			args[index] = flag + "=" + value
			return args
		}
	}
	return append(args, flag, value)
}
