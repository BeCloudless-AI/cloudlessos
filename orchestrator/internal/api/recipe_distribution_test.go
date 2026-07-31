package api

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/cloudless/orchestrator/internal/jobs"
)

func TestRunRecipeTransferCancelsProducerWhenConsumerFails(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	producer := exec.CommandContext(ctx, "/bin/sh", "-c", `while :; do printf '0123456789'; done`)
	consumer := exec.CommandContext(ctx, "/bin/sh", "-c", `exit 9`)
	started := time.Now()
	err := runRecipeTransfer(ctx, jobs.NewManager().Create("recipe:test"), "transfer", "test transfer", 0, 1024, producer, consumer)
	if err == nil || !strings.Contains(err.Error(), "exit status 9") {
		t.Fatalf("transfer error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("failed consumer left producer running for %s", elapsed)
	}
}
