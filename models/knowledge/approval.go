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
	// ErrApprovalNotFound is returned when an approval cannot be resolved.
	ErrApprovalNotFound = errors.New("knowledge approval not found")
	// ErrApprovalConflict is returned for an invalid or concurrently changed decision.
	ErrApprovalConflict = errors.New("knowledge approval conflict")
	// ErrSelfApproval is returned when a submitter attempts to approve their own revision.
	ErrSelfApproval = errors.New("knowledge self-approval is forbidden")
)

// ApproveRevisionOptions contains trusted authorization results from the
// service layer. Authorization itself remains outside the model package.
type ApproveRevisionOptions struct {
	ApprovalID           int64
	DecidedBy            int64
	Comment              string
	EngineProfileVersion string
	ACLPolicyVersion     int64
	TraceID              string
}

// RejectRevisionOptions contains a terminal rejection decision for a pending
// revision. Rejection never removes the immutable repository artifact.
type RejectRevisionOptions struct {
	ApprovalID int64
	DecidedBy  int64
	Comment    string
	TraceID    string
}

// ApproveRevisionResult returns the three durable records created atomically.
type ApproveRevisionResult struct {
	Publication *Publication
	Outbox      *Outbox
	IndexJob    *IndexJob
	Created     bool
}

// IndexIdempotencyKeyInput is the stable identity of one engine operation.
type IndexIdempotencyKeyInput struct {
	OwnerID              int64
	SpaceID              int64
	PublicationID        int64
	Generation           int64
	RevisionSHA256       string
	EngineProfileVersion string
	JobType              IndexJobType
}

// BuildIndexIdempotencyKey builds the documented, deterministic operation key.
func BuildIndexIdempotencyKey(input IndexIdempotencyKeyInput) string {
	return fmt.Sprintf("%d:%d:%d:%d:%s:%s:%s",
		input.OwnerID,
		input.SpaceID,
		input.PublicationID,
		input.Generation,
		strings.ToLower(input.RevisionSHA256),
		input.EngineProfileVersion,
		input.JobType,
	)
}

// ApproveRevision atomically decides an approval and creates its publication,
// outbox event, index job, document pointer, and audit event. Repeating an
// already completed approval returns the original records without new writes.
func ApproveRevision(ctx context.Context, opts ApproveRevisionOptions) (*ApproveRevisionResult, error) {
	if opts.ApprovalID <= 0 || opts.DecidedBy <= 0 || opts.ACLPolicyVersion <= 0 ||
		strings.TrimSpace(opts.EngineProfileVersion) == "" || !validTraceID(opts.TraceID) ||
		!validGovernanceText(opts.Comment, true) {
		return nil, ErrApprovalConflict
	}

	return db.WithTx2(ctx, func(txCtx context.Context) (*ApproveRevisionResult, error) {
		approval, exists, err := db.GetByID[Approval](txCtx, opts.ApprovalID)
		if err != nil {
			return nil, fmt.Errorf("load knowledge approval: %w", err)
		}
		if !exists {
			return nil, ErrApprovalNotFound
		}

		if approval.Status == ApprovalStatusApproved {
			return loadApprovalResult(txCtx, approval.ID)
		}
		if approval.Status != ApprovalStatusPending {
			return nil, ErrApprovalConflict
		}
		if approval.RequestedBy == opts.DecidedBy {
			return nil, ErrSelfApproval
		}

		revision, exists, err := db.GetByID[Revision](txCtx, approval.RevisionID)
		if err != nil {
			return nil, fmt.Errorf("load knowledge revision: %w", err)
		}
		if !exists {
			return nil, ErrApprovalConflict
		}
		document, exists, err := db.GetByID[Document](txCtx, revision.DocumentID)
		if err != nil {
			return nil, fmt.Errorf("load knowledge document: %w", err)
		}
		if !exists || document.GovernanceStatus != GovernanceStatusPending || document.CurrentRevisionID != revision.ID {
			return nil, ErrApprovalConflict
		}
		space, exists, err := db.GetByID[Space](txCtx, document.SpaceID)
		if err != nil {
			return nil, fmt.Errorf("load knowledge space: %w", err)
		}
		if !exists || space.Status != SpaceStatusActive {
			return nil, ErrApprovalConflict
		}

		now := timeutil.TimeStampNow()
		generation := space.PublicationGeneration + 1
		publication := &Publication{
			SpaceID:              space.ID,
			DocumentID:           document.ID,
			RevisionID:           revision.ID,
			ApprovalID:           approval.ID,
			Generation:           generation,
			GovernanceStatus:     GovernanceStatusApproved,
			ValidityStatus:       ValidityStatusScheduled,
			IndexStatus:          IndexStatusQueued,
			ACLPolicyVersion:     opts.ACLPolicyVersion,
			RevocationGeneration: space.RevocationGeneration,
			IsCurrent:            false,
		}
		if err := db.Insert(txCtx, publication); err != nil {
			return nil, fmt.Errorf("create knowledge publication: %w", err)
		}

		key := BuildIndexIdempotencyKey(IndexIdempotencyKeyInput{
			OwnerID:              space.OwnerID,
			SpaceID:              space.ID,
			PublicationID:        publication.ID,
			Generation:           generation,
			RevisionSHA256:       revision.ContentSHA256,
			EngineProfileVersion: opts.EngineProfileVersion,
			JobType:              IndexJobTypeUpsert,
		})
		payload, err := json.Marshal(map[string]any{
			"publication_id": publication.ID,
			"generation":     generation,
			"job_type":       IndexJobTypeUpsert,
		})
		if err != nil {
			return nil, errors.New("encode knowledge outbox payload")
		}
		outbox := &Outbox{
			AggregateType:  "knowledge_publication",
			AggregateID:    publication.ID,
			EventType:      "knowledge.index.upsert.requested",
			IdempotencyKey: key,
			PayloadJSON:    string(payload),
			Status:         OutboxStatusPending,
		}
		job := &IndexJob{
			PublicationID:        publication.ID,
			JobType:              IndexJobTypeUpsert,
			IdempotencyKey:       key,
			Status:               IndexJobStatusQueued,
			RevisionSHA256:       revision.ContentSHA256,
			EngineProfileVersion: opts.EngineProfileVersion,
			MaxAttempts:          5,
		}
		if err := db.Insert(txCtx, outbox); err != nil {
			return nil, fmt.Errorf("create knowledge outbox event: %w", err)
		}
		if err := db.Insert(txCtx, job); err != nil {
			return nil, fmt.Errorf("create knowledge index job: %w", err)
		}

		approval.Status = ApprovalStatusApproved
		approval.DecidedBy = opts.DecidedBy
		approval.DecidedUnix = now
		approval.Comment = opts.Comment
		updated, err := db.GetEngine(txCtx).
			Where("id = ? AND status = ?", approval.ID, ApprovalStatusPending).
			Cols("status", "decided_by", "decided_unix", "comment").
			Update(approval)
		if err != nil {
			return nil, fmt.Errorf("decide knowledge approval: %w", err)
		}
		if updated != 1 {
			return nil, ErrApprovalConflict
		}

		space.PublicationGeneration = generation
		updated, err = db.GetEngine(txCtx).
			Where("id = ? AND publication_generation = ?", space.ID, generation-1).
			Cols("publication_generation").
			Update(space)
		if err != nil {
			return nil, fmt.Errorf("advance knowledge publication generation: %w", err)
		}
		if updated != 1 {
			return nil, ErrApprovalConflict
		}

		document.GovernanceStatus = GovernanceStatusApproved
		document.CurrentRevisionID = revision.ID
		updated, err = db.GetEngine(txCtx).
			Where("id = ? AND governance_status = ? AND current_revision_id = ?", document.ID, GovernanceStatusPending, revision.ID).
			Cols("governance_status", "current_revision_id").
			Update(document)
		if err != nil {
			return nil, fmt.Errorf("advance knowledge document: %w", err)
		}
		if updated != 1 {
			return nil, ErrApprovalConflict
		}

		auditMetadata, err := json.Marshal(map[string]any{
			"approval_id":    approval.ID,
			"revision_id":    revision.ID,
			"publication_id": publication.ID,
			"generation":     generation,
		})
		if err != nil {
			return nil, errors.New("encode knowledge audit metadata")
		}
		audit := &AuditEvent{
			ActorID:      opts.DecidedBy,
			Action:       "knowledge.revision.approved",
			EntityType:   "knowledge_publication",
			EntityID:     publication.ID,
			SpaceID:      space.ID,
			Result:       "succeeded",
			ReasonCode:   "approved",
			TraceID:      opts.TraceID,
			MetadataJSON: string(auditMetadata),
		}
		if err := db.Insert(txCtx, audit); err != nil {
			return nil, fmt.Errorf("append knowledge audit event: %w", err)
		}

		return &ApproveRevisionResult{
			Publication: publication,
			Outbox:      outbox,
			IndexJob:    job,
			Created:     true,
		}, nil
	})
}

// RejectRevision records a terminal decision and restores the document's last
// searchable revision when it has one. This makes a failed update fail closed
// without withdrawing an already valid publication.
func RejectRevision(ctx context.Context, opts RejectRevisionOptions) error {
	if opts.ApprovalID <= 0 || opts.DecidedBy <= 0 || !validTraceID(opts.TraceID) || !validGovernanceText(opts.Comment, false) {
		return ErrApprovalConflict
	}

	return db.WithTx(ctx, func(txCtx context.Context) error {
		approval, exists, err := db.GetByID[Approval](txCtx, opts.ApprovalID)
		if err != nil {
			return fmt.Errorf("load knowledge approval: %w", err)
		}
		if !exists {
			return ErrApprovalNotFound
		}
		if approval.Status != ApprovalStatusPending {
			return ErrApprovalConflict
		}
		if approval.RequestedBy == opts.DecidedBy {
			return ErrSelfApproval
		}
		revision, exists, err := db.GetByID[Revision](txCtx, approval.RevisionID)
		if err != nil || !exists {
			return ErrApprovalConflict
		}
		document, exists, err := db.GetByID[Document](txCtx, revision.DocumentID)
		if err != nil || !exists {
			return ErrApprovalConflict
		}

		now := timeutil.TimeStampNow()
		approval.Status = ApprovalStatusRejected
		approval.DecidedBy = opts.DecidedBy
		approval.DecidedUnix = now
		approval.Comment = opts.Comment
		updated, err := db.GetEngine(txCtx).Where("id = ? AND status = ?", approval.ID, ApprovalStatusPending).
			Cols("status", "decided_by", "decided_unix", "comment").Update(approval)
		if err != nil {
			return fmt.Errorf("reject knowledge approval: %w", err)
		}
		if updated != 1 {
			return ErrApprovalConflict
		}

		// An existing current publication remains authoritative after an update
		// revision is rejected. A first revision stays rejected and unsearchable.
		document.GovernanceStatus = GovernanceStatusRejected
		if document.CurrentPublicationID > 0 {
			current, found, err := db.GetByID[Publication](txCtx, document.CurrentPublicationID)
			if err != nil {
				return fmt.Errorf("load current knowledge publication: %w", err)
			}
			if found && current.IsCurrent && current.ValidityStatus == ValidityStatusCurrent {
				document.GovernanceStatus = GovernanceStatusApproved
				document.CurrentRevisionID = current.RevisionID
			}
		}
		if _, err := db.GetEngine(txCtx).ID(document.ID).Cols("governance_status", "current_revision_id").Update(document); err != nil {
			return fmt.Errorf("restore knowledge document after rejection: %w", err)
		}

		return appendPublicationAudit(txCtx, &AuditEvent{
			ActorID: opts.DecidedBy, Action: "knowledge.revision.rejected", EntityType: "knowledge_revision", EntityID: revision.ID,
			SpaceID: document.SpaceID, Result: "succeeded", ReasonCode: "rejected", TraceID: opts.TraceID,
			MetadataJSON: `{"approval_id":` + fmt.Sprint(approval.ID) + `}`,
		})
	})
}

func loadApprovalResult(ctx context.Context, approvalID int64) (*ApproveRevisionResult, error) {
	publication := new(Publication)
	exists, err := db.GetEngine(ctx).Where("approval_id = ?", approvalID).Get(publication)
	if err != nil {
		return nil, fmt.Errorf("load knowledge publication: %w", err)
	}
	if !exists {
		return nil, ErrApprovalConflict
	}
	job := new(IndexJob)
	exists, err = db.GetEngine(ctx).Where("publication_id = ? AND job_type = ?", publication.ID, IndexJobTypeUpsert).Get(job)
	if err != nil {
		return nil, fmt.Errorf("load knowledge index job: %w", err)
	}
	if !exists {
		return nil, ErrApprovalConflict
	}
	outbox := new(Outbox)
	exists, err = db.GetEngine(ctx).Where("idempotency_key = ?", job.IdempotencyKey).Get(outbox)
	if err != nil {
		return nil, fmt.Errorf("load knowledge outbox event: %w", err)
	}
	if !exists {
		return nil, ErrApprovalConflict
	}
	return &ApproveRevisionResult{Publication: publication, Outbox: outbox, IndexJob: job}, nil
}
