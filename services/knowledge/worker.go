// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

// Package knowledge runs FileBay-owned background knowledge operations.
package knowledge

import (
	"context"
	"time"

	"code.gitea.io/gitea/models/db"
	knowledge_model "code.gitea.io/gitea/models/knowledge"
	"code.gitea.io/gitea/modules/graceful"
	"code.gitea.io/gitea/modules/log"
	"code.gitea.io/gitea/modules/setting"
	"code.gitea.io/gitea/services/knowledge/catalog"
)

const indexWorkerBatchSize = 20

// Init starts the durable FileBay-to-RAGFlow worker only when knowledge
// integration is explicitly enabled. Index jobs are persisted before this
// worker sees them, so restarting FileBay cannot lose an approved publication.
func Init(_ context.Context) error {
	if !setting.Knowledge.Enabled {
		return nil
	}
	if err := setting.ValidateKnowledgeSettings(); err != nil {
		return err
	}
	go graceful.GetManager().RunWithShutdownContext(runIndexWorker)
	return nil
}

func runIndexWorker(ctx context.Context) {
	processIndexJobs(ctx)
	ticker := time.NewTicker(setting.Knowledge.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			processIndexJobs(ctx)
		}
	}
}

func processIndexJobs(ctx context.Context) {
	if reconciled, err := catalog.ReconcileSearchableBindingsSystem(ctx); err != nil {
		log.Warn("knowledge index worker: reconcile legacy index bindings: %v", err)
	} else if reconciled > 0 {
		log.Info("knowledge index worker: reconciled %d legacy index binding(s)", reconciled)
	}
	var jobs []knowledge_model.IndexJob
	err := db.GetEngine(ctx).
		Where("status IN (?, ?)", knowledge_model.IndexJobStatusQueued, knowledge_model.IndexJobStatusRetry).
		Asc("id").
		Limit(indexWorkerBatchSize).
		Find(&jobs)
	if err != nil {
		log.Error("knowledge index worker: list jobs: %v", err)
		return
	}
	for _, job := range jobs {
		if ctx.Err() != nil {
			return
		}
		if err := catalog.ProcessIndexJobSystem(ctx, job.ID); err != nil {
			// The processor persists a redacted terminal/retry state. Never log
			// source paths, document content, or integration credentials here.
			log.Warn("knowledge index worker: process job %d: %v", job.ID, err)
		}
	}
}
