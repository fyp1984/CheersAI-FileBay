// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package retrieval

import (
	"context"
	"errors"
	"fmt"

	"code.gitea.io/gitea/models/db"
	knowledge_model "code.gitea.io/gitea/models/knowledge"
	user_model "code.gitea.io/gitea/models/user"
)

const (
	qualityTaskNoAnswer    = "no_answer"
	qualityTaskLowFeedback = "low_feedback"
	qualityTaskResolved    = "knowledge.quality_task.resolved"
)

// ResolveQualityTask records an administrator's handling of a non-answer or
// low-quality feedback signal. The signal itself stays immutable; resolution
// is represented by an append-only audit event so the quality operation is
// traceable without duplicating a user's query or feedback comment.
func ResolveQualityTask(ctx context.Context, actorID int64, taskType string, entityID int64) error {
	actor, err := user_model.GetUserByID(ctx, actorID)
	if err != nil || !actor.IsAdmin {
		return errors.New("knowledge quality task resolution requires an administrator")
	}
	if entityID <= 0 || (taskType != qualityTaskNoAnswer && taskType != qualityTaskLowFeedback) {
		return errors.New("invalid knowledge quality task")
	}

	return db.WithTx(ctx, func(ctx context.Context) error {
		entityType, spaceID, err := loadQualityTask(ctx, taskType, entityID)
		if err != nil {
			return err
		}
		resolved, err := db.GetEngine(ctx).Where("action = ? AND entity_type = ? AND entity_id = ? AND result = ?", qualityTaskResolved, entityType, entityID, "succeeded").Exist(new(knowledge_model.AuditEvent))
		if err != nil {
			return fmt.Errorf("check knowledge quality task resolution: %w", err)
		}
		if resolved {
			return nil
		}
		return appendAudit(ctx, actor.ID, spaceID, qualityTaskResolved, entityType, entityID, "succeeded", taskType)
	})
}

func loadQualityTask(ctx context.Context, taskType string, entityID int64) (string, int64, error) {
	switch taskType {
	case qualityTaskNoAnswer:
		event, exists, err := db.GetByID[knowledge_model.RetrievalEvent](ctx, entityID)
		if err != nil {
			return "", 0, fmt.Errorf("load no-answer retrieval event: %w", err)
		}
		if !exists || event.Outcome != "empty" {
			return "", 0, errors.New("knowledge no-answer task not found")
		}
		return "retrieval_event", event.SpaceID, nil
	case qualityTaskLowFeedback:
		feedback, exists, err := db.GetByID[knowledge_model.Feedback](ctx, entityID)
		if err != nil {
			return "", 0, fmt.Errorf("load low-quality feedback: %w", err)
		}
		if !exists || feedback.Rating > 2 {
			return "", 0, errors.New("knowledge low-quality feedback task not found")
		}
		return "feedback", feedback.SpaceID, nil
	default:
		return "", 0, errors.New("invalid knowledge quality task")
	}
}
