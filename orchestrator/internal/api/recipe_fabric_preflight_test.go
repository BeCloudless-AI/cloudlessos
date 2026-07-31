package api

import (
	"strings"
	"testing"
	"time"
)

func TestRecipeFabricProbePayloadIsDeterministicAndNonZero(t *testing.T) {
	first, second := recipeFabricProbePayload(), recipeFabricProbePayload()
	if len(first) != recipeFabricProbeBytes || recipeSHA256(first) != recipeSHA256(second) {
		t.Fatal("fabric probe payload is not deterministic")
	}
	if recipeSHA256(first) == recipeSHA256(make([]byte, recipeFabricProbeBytes)) {
		t.Fatal("fabric upload probe is indistinguishable from zero-filled download probe")
	}
}

func TestRecipeNCCLProbeArgsUseExactImageAndDistributedContract(t *testing.T) {
	env := map[string]string{
		"CLOUDLESS_RECIPE_OPERATION_ID": "rop-0123456789abcdef0123456789abcdef",
		"MASTER_ADDR":                   "192.168.100.1", "MASTER_PORT": "25000",
		"NCCL_SOCKET_IFNAME": "enP2p1s0f0np0", "NCCL_IB_HCA": "roceP2p1s0f0",
	}
	image := "example.invalid/runtime@sha256:" + strings.Repeat("a", 64)
	args := recipeNCCLProbeArgs(image, 1, 2, env)
	joined := strings.Join(args, " ")
	for _, required := range []string{image, "MASTER_ADDR=192.168.100.1", "MASTER_PORT=25000", "WORLD_SIZE=2", "RANK=1", "NCCL_SOCKET_IFNAME=enP2p1s0f0np0", "cloudless-nccl-probe-1-01234567"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("probe args omit %q: %s", required, joined)
		}
	}
}

func TestRecipeTransferRate(t *testing.T) {
	if got := recipeTransferRate(1024, time.Second); got != 1024 {
		t.Fatalf("transfer rate = %d", got)
	}
	if got := recipeTransferRate(1024, 0); got != 0 {
		t.Fatalf("zero-duration transfer rate = %d", got)
	}
}
