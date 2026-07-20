// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"code.gitea.io/gitea/models/db"
	"code.gitea.io/gitea/modules/timeutil"
)

var (
	// ErrPublicationNotFound is returned when a publication cannot be resolved.
	ErrPublicationNotFound = errors.New("knowledge publication not found")
	// ErrPublicationConflict is returned for stale or invalid lifecycle input.
	ErrPublicationConflict = errors.New("knowledge publication conflict")
)

// ActivatePublicationOptions contains the generations used by the activation
// compare-and-swap transaction.
type ActivatePublicationOptions struct {
	PublicationID                 int64
	ActorID                       int64
	ExpectedPublicationGeneration int64
	ExpectedRevocationGeneration  int64
	TraceID                       string
}

// ActivatePublication atomically makes an evaluated candidate current. Stale
// publication or revocation generations fail without changing either pointer.
func ActivatePublication(ctx context.Context, opts ActivatePublicationOptions) error {
	if opts.PublicationID <= 0 || opts.ActorID <= 0 || opts.ExpectedPublicationGeneration <= 0 ||
		opts.ExpectedRevocationGeneration <= 0 || !validTraceID(opts.TraceID) {
		return ErrPublicationConflict
	}

	return db.WithTx(ctx, func(txCtx context.Context) error {
		candidate, exists, err := db.GetByID[Publication](txCtx, opts.PublicationID)
		if err != nil {
			return fmt.Errorf("load knowledge publication: %w", err)
		}
		if !exists {
			return ErrPublicationNotFound
		}
		document, space, err := loadPublicationParents(txCtx, candidate)
		if err != nil {
			return err
		}
		if candidate.GovernanceStatus != GovernanceStatusApproved ||
			candidate.ValidityStatus != ValidityStatusScheduled ||
			candidate.IndexStatus != IndexStatusEvaluation || candidate.IsCurrent ||
			candidate.Generation != opts.ExpectedPublicationGeneration ||
			candidate.RevocationGeneration != opts.ExpectedRevocationGeneration ||
			space.RevocationGeneration != opts.ExpectedRevocationGeneration {
			return ErrPublicationConflict
		}

		oldPublicationID := document.CurrentPublicationID
		if oldPublicationID > 0 {
			oldCurrent, exists, err := db.GetByID[Publication](txCtx, oldPublicationID)
			if err != nil {
				return fmt.Errorf("load current knowledge publication: %w", err)
			}
			if !exists || oldCurrent.SpaceID != space.ID || oldCurrent.DocumentID != document.ID || !oldCurrent.IsCurrent {
				return ErrPublicationConflict
			}
			oldCurrent.IsCurrent = false
			oldCurrent.IndexStatus = IndexStatusSuperseded
			updated, err := db.GetEngine(txCtx).
				Where("id = ? AND is_current = ?", oldCurrent.ID, true).
				Cols("is_current", "index_status").
				Update(oldCurrent)
			if err != nil {
				return fmt.Errorf("supersede current knowledge publication: %w", err)
			}
			if updated != 1 {
				return ErrPublicationConflict
			}
		}

		now := timeutil.TimeStampNow()
		candidate.IsCurrent = true
		candidate.ValidityStatus = ValidityStatusCurrent
		candidate.IndexStatus = IndexStatusSearchable
		candidate.EffectiveUnix = now
		updated, err := db.GetEngine(txCtx).
			Where("id = ? AND generation = ? AND revocation_generation = ? AND governance_status = ? AND validity_status = ? AND index_status = ? AND is_current = ?",
				candidate.ID, opts.ExpectedPublicationGeneration, opts.ExpectedRevocationGeneration,
				GovernanceStatusApproved, ValidityStatusScheduled, IndexStatusEvaluation, false).
			Cols("is_current", "validity_status", "index_status", "effective_unix").
			Update(candidate)
		if err != nil {
			return fmt.Errorf("activate knowledge publication: %w", err)
		}
		if updated != 1 {
			return ErrPublicationConflict
		}

		document.CurrentRevisionID = candidate.RevisionID
		document.CurrentPublicationID = candidate.ID
		updated, err = db.GetEngine(txCtx).
			Where("id = ? AND current_publication_id = ?", document.ID, oldPublicationID).
			Cols("current_revision_id", "current_publication_id").
			Update(document)
		if err != nil {
			return fmt.Errorf("switch knowledge publication pointer: %w", err)
		}
		if updated != 1 {
			return ErrPublicationConflict
		}

		return appendPublicationAudit(txCtx, &AuditEvent{
			ActorID:      opts.ActorID,
			Action:       "knowledge.publication.activated",
			EntityType:   "knowledge_publication",
			EntityID:     candidate.ID,
			SpaceID:      space.ID,
			Result:       "succeeded",
			ReasonCode:   "activated",
			TraceID:      opts.TraceID,
			MetadataJSON: publicationAuditMetadata(candidate.ID, candidate.Generation, candidate.RevocationGeneration),
		})
	})
}

// UnpublishPublicationOptions contains the generations used by the synchronous
// revocation transaction and the derived-engine profile used for deletion.
type UnpublishPublicationOptions struct {
	PublicationID                 int64
	ActorID                       int64
	ExpectedPublicationGeneration int64
	ExpectedRevocationGeneration  int64
	EngineProfileVersion          string
	TraceID                       string
	Reason                        string
}

// UnpublishPublication synchronously revokes the authoritative current pointer,
// then durably schedules derived deletion. A replay returns the original result.
func UnpublishPublication(ctx context.Context, opts UnpublishPublicationOptions) error {
	if opts.PublicationID <= 0 || opts.ActorID <= 0 || opts.ExpectedPublicationGeneration <= 0 ||
		opts.ExpectedRevocationGeneration <= 0 || strings.TrimSpace(opts.EngineProfileVersion) == "" ||
		!validTraceID(opts.TraceID) || !validGovernanceText(opts.Reason, false) {
		return ErrPublicationConflict
	}

	return db.WithTx(ctx, func(txCtx context.Context) error {
		tombstone := new(RevocationTombstone)
		exists, err := db.GetEngine(txCtx).Where("publication_id = ?", opts.PublicationID).Get(tombstone)
		if err != nil {
			return fmt.Errorf("load knowledge revocation: %w", err)
		}
		if exists {
			if tombstone.PublicationGeneration == opts.ExpectedPublicationGeneration &&
				tombstone.RevocationGeneration == opts.ExpectedRevocationGeneration &&
				tombstone.EngineProfileVersion == opts.EngineProfileVersion &&
				tombstone.Reason == opts.Reason &&
				tombstone.TraceID == opts.TraceID {
				return nil
			}
			return ErrPublicationConflict
		}

		publication, exists, err := db.GetByID[Publication](txCtx, opts.PublicationID)
		if err != nil {
			return fmt.Errorf("load knowledge publication: %w", err)
		}
		if !exists {
			return ErrPublicationNotFound
		}
		document, space, err := loadPublicationParents(txCtx, publication)
		if err != nil {
			return err
		}
		if !publication.IsCurrent || publication.ValidityStatus != ValidityStatusCurrent ||
			publication.IndexStatus != IndexStatusSearchable || document.CurrentPublicationID != publication.ID ||
			publication.Generation != opts.ExpectedPublicationGeneration ||
			publication.RevocationGeneration != opts.ExpectedRevocationGeneration ||
			space.RevocationGeneration != opts.ExpectedRevocationGeneration {
			return ErrPublicationConflict
		}

		revision, exists, err := db.GetByID[Revision](txCtx, publication.RevisionID)
		if err != nil {
			return fmt.Errorf("load knowledge revision: %w", err)
		}
		if !exists {
			return ErrPublicationConflict
		}
		newRevocationGeneration := publication.RevocationGeneration

		document.CurrentPublicationID = 0
		updated, err := db.GetEngine(txCtx).
			Where("id = ? AND current_publication_id = ?", document.ID, publication.ID).
			Cols("current_publication_id").
			Update(document)
		if err != nil {
			return fmt.Errorf("revoke knowledge publication pointer: %w", err)
		}
		if updated != 1 {
			return ErrPublicationConflict
		}

		publication.IsCurrent = false
		publication.ValidityStatus = ValidityStatusUnpublished
		updated, err = db.GetEngine(txCtx).
			Where("id = ? AND is_current = ? AND validity_status = ?", publication.ID, true, ValidityStatusCurrent).
			Cols("is_current", "validity_status").
			Update(publication)
		if err != nil {
			return fmt.Errorf("unpublish knowledge publication: %w", err)
		}
		if updated != 1 {
			return ErrPublicationConflict
		}

		tombstone = &RevocationTombstone{
			PublicationID:         publication.ID,
			SpaceID:               space.ID,
			PublicationGeneration: publication.Generation,
			RevocationGeneration:  newRevocationGeneration,
			EngineProfileVersion:  opts.EngineProfileVersion,
			ActorID:               opts.ActorID,
			Reason:                opts.Reason,
			TraceID:               opts.TraceID,
		}
		if err := db.Insert(txCtx, tombstone); err != nil {
			return fmt.Errorf("create knowledge revocation: %w", err)
		}

		key := BuildIndexIdempotencyKey(IndexIdempotencyKeyInput{
			OwnerID:              space.OwnerID,
			SpaceID:              space.ID,
			PublicationID:        publication.ID,
			Generation:           publication.Generation,
			RevisionSHA256:       revision.ContentSHA256,
			EngineProfileVersion: opts.EngineProfileVersion,
			JobType:              IndexJobTypeDelete,
		})
		payload, err := json.Marshal(map[string]any{
			"publication_id":        publication.ID,
			"revocation_generation": newRevocationGeneration,
			"job_type":              IndexJobTypeDelete,
		})
		if err != nil {
			return errors.New("encode knowledge deletion payload")
		}
		outbox := &Outbox{
			AggregateType:  "knowledge_publication",
			AggregateID:    publication.ID,
			EventType:      "knowledge.index.delete.requested",
			IdempotencyKey: key,
			PayloadJSON:    string(payload),
			Status:         OutboxStatusPending,
		}
		job := &IndexJob{
			PublicationID:        publication.ID,
			JobType:              IndexJobTypeDelete,
			IdempotencyKey:       key,
			Status:               IndexJobStatusQueued,
			RevisionSHA256:       revision.ContentSHA256,
			EngineProfileVersion: opts.EngineProfileVersion,
			MaxAttempts:          5,
		}
		if err := db.Insert(txCtx, outbox); err != nil {
			return fmt.Errorf("create knowledge deletion outbox: %w", err)
		}
		if err := db.Insert(txCtx, job); err != nil {
			return fmt.Errorf("create knowledge deletion job: %w", err)
		}

		return appendPublicationAudit(txCtx, &AuditEvent{
			ActorID:      opts.ActorID,
			Action:       "knowledge.publication.unpublished",
			EntityType:   "knowledge_publication",
			EntityID:     publication.ID,
			SpaceID:      space.ID,
			Result:       "succeeded",
			ReasonCode:   "unpublished",
			TraceID:      opts.TraceID,
			MetadataJSON: publicationAuditMetadata(publication.ID, publication.Generation, newRevocationGeneration),
		})
	})
}

func loadPublicationParents(ctx context.Context, publication *Publication) (*Document, *Space, error) {
	document, exists, err := db.GetByID[Document](ctx, publication.DocumentID)
	if err != nil {
		return nil, nil, fmt.Errorf("load knowledge document: %w", err)
	}
	if !exists || document.SpaceID != publication.SpaceID {
		return nil, nil, ErrPublicationConflict
	}
	space, exists, err := db.GetByID[Space](ctx, publication.SpaceID)
	if err != nil {
		return nil, nil, fmt.Errorf("load knowledge space: %w", err)
	}
	if !exists || space.Status != SpaceStatusActive {
		return nil, nil, ErrPublicationConflict
	}
	return document, space, nil
}

func appendPublicationAudit(ctx context.Context, event *AuditEvent) error {
	if err := db.Insert(ctx, event); err != nil {
		return fmt.Errorf("append knowledge audit event: %w", err)
	}
	return nil
}

func publicationAuditMetadata(publicationID, generation, revocationGeneration int64) string {
	metadata, err := json.Marshal(map[string]int64{
		"publication_id":        publicationID,
		"generation":            generation,
		"revocation_generation": revocationGeneration,
	})
	if err != nil {
		return "{}"
	}
	return string(metadata)
}
