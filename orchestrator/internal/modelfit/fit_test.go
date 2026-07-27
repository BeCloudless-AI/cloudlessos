package modelfit

import (
	"testing"

	"github.com/cloudless/orchestrator/internal/models"
)

func TestQuantizationAndUnifiedReserveChangeFit(t *testing.T) {
	full := models.Model{Params: "35B / 3B active", ContextK: 262}
	fp8 := full
	fp8.Quant = "FP8"
	env := Envelope{MemoryGB: 31, MemoryType: "discrete", Nodes: 1}
	if got := EstimateModel(full, env); got.Status != "over" || got.WeightsGB < 60 {
		t.Fatalf("full estimate = %#v", got)
	}
	if got := EstimateModel(fp8, env); got.WeightsGB >= EstimateModel(full, env).WeightsGB {
		t.Fatalf("FP8 did not reduce weights: %#v", got)
	}
	unified := EstimateModel(full, Envelope{MemoryGB: 128, MemoryType: "unified", Nodes: 1})
	if unified.Status == "over" || unified.UsablePerNodeGB >= 128 {
		t.Fatalf("Spark estimate = %#v", unified)
	}
}

func TestTensorShardingUsesEveryNodeWithoutPretendingMemoryIsOnePool(t *testing.T) {
	m := models.Model{Params: "284B / 13B active", Quant: "FP4 + FP8", ContextK: 1000}
	single := EstimateModel(m, Envelope{MemoryGB: 128, MemoryType: "unified", Nodes: 1})
	cluster := EstimateModel(m, Envelope{MemoryGB: 128, MemoryType: "unified", Nodes: 2, Sharded: true})
	if single.Status != "over" || cluster.RequiredPerNodeGB >= single.RequiredPerNodeGB {
		t.Fatalf("single=%#v cluster=%#v", single, cluster)
	}
	if cluster.Nodes != 2 || !cluster.Sharded {
		t.Fatalf("cluster topology missing: %#v", cluster)
	}
}

func TestUnknownCustomModelFailsHonestly(t *testing.T) {
	got := EstimateModel(models.Model{Params: "?"}, Envelope{MemoryGB: 24, MemoryType: "discrete"})
	if got.Status != "unknown" || got.Confidence != "low" {
		t.Fatalf("unknown = %#v", got)
	}
}
