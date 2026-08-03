package api

import (
	"log"
	"sync"
	"time"

	"github.com/cloudless/orchestrator/internal/jobs"
	"github.com/cloudless/orchestrator/internal/recipeops"
)

// observeRecipeJob mirrors the ephemeral SSE job into the durable operation
// journal. It throttles message-only churn from Docker output while persisting
// every stage or percentage change immediately.
func (s *Server) observeRecipeJob(job *jobs.Job, operationID string) {
	if job == nil || s.recipeOps == nil {
		return
	}
	var mu sync.Mutex
	lastStage := ""
	lastOverall, lastPhase := -1, -1
	lastWrite := time.Time{}
	job.Observe(func(update jobs.Update) {
		mu.Lock()
		defer mu.Unlock()
		overall := update.Percent
		if update.LayersTotal > 0 && update.LayersDone >= 0 {
			overall = update.LayersDone * 100 / update.LayersTotal
		}
		if update.Done && update.Error == "" && update.Phase != "canceled" {
			overall = 100
		}
		phasePercent := update.Percent
		if update.BytesTotal > 0 {
			phasePercent = int(update.BytesDone * 100 / update.BytesTotal)
		}
		now := time.Now()
		if !update.Done && update.Phase == lastStage && overall == lastOverall && phasePercent == lastPhase && now.Sub(lastWrite) < time.Second {
			return
		}
		eta := update.ETASecs
		if overall > 0 && overall < 100 && update.ElapsedSecs > 0 {
			eta = update.ElapsedSecs * int64(100-overall) / int64(overall)
		}
		components := make([]recipeops.ProgressComponent, 0, len(update.Components))
		for _, component := range update.Components {
			components = append(components, recipeops.ProgressComponent{Name: component.Name, Status: component.Status, BytesDone: component.BytesDone, BytesTotal: component.BytesTotal, BytesPerSecond: component.BytesPerSec, ETASeconds: component.ETASeconds})
		}
		_, err := s.recipeOps.RecordProgress(operationID, recipeops.Progress{
			Stage: update.Phase, Message: update.Message, OverallPercent: overall, PhasePercent: phasePercent,
			Completed: update.LayersDone, Total: update.LayersTotal,
			BytesDone: update.BytesDone, BytesTotal: update.BytesTotal,
			ElapsedSeconds: update.ElapsedSecs, ETASeconds: eta,
			BytesPerSecond: update.BytesPerSec, StalledSeconds: update.StalledSecs, Components: components,
		})
		if err != nil {
			log.Printf("[recipe-progress] persist %s: %v", operationID, err)
			return
		}
		lastStage, lastOverall, lastPhase, lastWrite = update.Phase, overall, phasePercent, now
	})
}
