package localrecipes

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const ManagedContainerAdapter = "managed-container-v1"

var (
	immutableContainerImagePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@-]{0,430}@sha256:[0-9a-fA-F]{64}$`)
	immutableModelRevisionPattern  = regexp.MustCompile(`^[0-9a-fA-F]{40}(?:[0-9a-fA-F]{24})?$`)
)

var managedVLLMBooleanArguments = map[string]struct{}{
	"--disable-log-requests":      {},
	"--disable-sliding-window":    {},
	"--enable-chunked-prefill":    {},
	"--enable-prefix-caching":     {},
	"--enforce-eager":             {},
	"--enable-reasoning":          {},
	"--disable-custom-all-reduce": {},
}

// ValidateManagedContainerRecipe proves that an editable recipe can be
// represented without executing repository code, host commands, SSH, arbitrary
// mounts, or a Docker socket. The runtime broker remains responsible for
// synthesizing the complete container command and security boundary.
func ValidateManagedContainerRecipe(recipe Recipe) error {
	return validateManagedContainerDraft(DraftFromRecipe(recipe))
}

func validateManagedContainerDraft(d Draft) error {
	if strings.TrimSpace(d.Runtime.Adapter) != ManagedContainerAdapter {
		return fmt.Errorf("runtime adapter must be %q", ManagedContainerAdapter)
	}
	if d.Source.URL != "" || d.Source.Revision != "" || len(d.Source.Files) != 0 {
		return errors.New("managed container recipes cannot include source repositories")
	}
	if !immutableContainerImagePattern.MatchString(strings.TrimSpace(d.Engine.Image)) {
		return errors.New("managed container recipes require an image pinned as repository@sha256:<digest>")
	}
	if strings.ToLower(strings.TrimSpace(d.Engine.Type)) != "vllm" {
		return errors.New("managed container recipes currently support the vLLM engine")
	}
	if d.Engine.ServedModelName != CloudlessModelAlias || d.Engine.APIPath != "/v1" {
		return errors.New("managed container recipes must preserve the Cloudless model and /v1 API contract")
	}
	if d.Engine.ProxyHost != "host.docker.internal" {
		return errors.New("managed container recipes must use the Cloudless host gateway")
	}
	if !immutableModelRevisionPattern.MatchString(strings.TrimSpace(d.Model.Revision)) {
		return errors.New("managed container recipes require an immutable 40- or 64-character model revision")
	}
	if d.Distributed.Nodes != 1 || len(d.Distributed.SelectedNodes) != 0 || d.Runtime.BuildOnce || d.Runtime.DownloadOnce {
		return errors.New("managed container recipes currently support one local node")
	}
	if len(d.Runtime.Prerequisites) != 0 {
		return errors.New("managed container recipes cannot request host prerequisites")
	}
	if d.Runtime.WorkingDir != "" && d.Runtime.WorkingDir != "." {
		return errors.New("managed container recipes cannot select a host working directory")
	}
	for label, command := range map[string]Command{
		"build": d.Runtime.Lifecycle.Build, "download": d.Runtime.Lifecycle.Download,
		"start": d.Runtime.Lifecycle.Start, "stop": d.Runtime.Lifecycle.Stop,
	} {
		if command.Program != "" || len(command.Args) != 0 {
			return fmt.Errorf("managed container recipes cannot provide a %s command", label)
		}
	}
	for _, argument := range d.Engine.Arguments {
		if _, ok := managedVLLMBooleanArguments[argument]; !ok {
			return fmt.Errorf("vLLM argument %q is not available in the constrained recipe runtime", argument)
		}
	}
	if d.Health.Scheme != "http" || d.Health.Host != "127.0.0.1" || d.Health.Port != d.Engine.ContainerPort {
		return errors.New("managed container health checks must use the managed loopback port over HTTP")
	}
	return nil
}
