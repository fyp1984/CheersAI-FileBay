// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"code.gitea.io/gitea/models/db"
	knowledge_model "code.gitea.io/gitea/models/knowledge"
	user_model "code.gitea.io/gitea/models/user"
	"code.gitea.io/gitea/modules/timeutil"
)

var (
	dataSourceCodePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	sourceReference       = regexp.MustCompile(`^(grant-id|approval-ref|contract-ref):[A-Za-z0-9][A-Za-z0-9._-]{0,255}$`)
	documentVersion       = regexp.MustCompile(`^V[0-9]+(?:\.[0-9]+){0,3}$`)
)

// CreateDataSourceOptions describes one governed source-register request.
// Source credential values deliberately have no field in this request.
type CreateDataSourceOptions struct {
	SpaceID                int64
	ActorID                int64
	SourceCode             string
	Name                   string
	SourceType             knowledge_model.DataSourceType
	BusinessDomainCode     string
	ContentTypeCode        string
	SourceDepartment       string
	ContentOwnerUsername   string
	MaintenanceUsername    string
	EffectiveUnix          timeutil.TimeStamp
	ExpiresUnix            timeutil.TimeStamp
	ReviewFrequency        knowledge_model.ReviewFrequency
	SecurityLevel          knowledge_model.SecurityLevel
	ApprovalBasisRef       string
	ApplicableScopeJSON    string
	SourceAuthorizationRef string
	SourceFormat           string
	SearchabilityStatus    knowledge_model.SearchabilityStatus
	ConnectorType          string
	SecretRef              string
	ReadScope              string
	QueryTemplateRef       string
	SchemaVersion          string
	FieldMappingJSON       string
	IncrementalCursorField string
	DataRetentionRule      string
	MaskPolicyVersion      string
}

// CreateDataSource registers a source in pending-review state. A second site
// administrator must explicitly enable it before it can supply knowledge.
func CreateDataSource(ctx context.Context, opts CreateDataSourceOptions) (*knowledge_model.DataSource, error) {
	space, actor, err := loadManagedSpace(ctx, opts.SpaceID, opts.ActorID)
	if err != nil {
		return nil, err
	}
	if err := validateDataSourceOptions(opts); err != nil {
		return nil, err
	}
	contentOwner, err := user_model.GetUserByName(ctx, strings.TrimSpace(opts.ContentOwnerUsername))
	if err != nil {
		return nil, fmt.Errorf("load data-source content owner: %w", err)
	}
	maintenanceOwner, err := user_model.GetUserByName(ctx, strings.TrimSpace(opts.MaintenanceUsername))
	if err != nil {
		return nil, fmt.Errorf("load data-source maintenance owner: %w", err)
	}

	source := &knowledge_model.DataSource{
		SpaceID:                space.ID,
		SourceCode:             strings.TrimSpace(opts.SourceCode),
		Name:                   strings.TrimSpace(opts.Name),
		SourceType:             opts.SourceType,
		BusinessDomainCode:     strings.TrimSpace(opts.BusinessDomainCode),
		ContentTypeCode:        strings.TrimSpace(opts.ContentTypeCode),
		SourceDepartment:       strings.TrimSpace(opts.SourceDepartment),
		ContentOwnerID:         contentOwner.ID,
		MaintenanceOwnerID:     maintenanceOwner.ID,
		EffectiveUnix:          opts.EffectiveUnix,
		ExpiresUnix:            opts.ExpiresUnix,
		ReviewFrequency:        opts.ReviewFrequency,
		SecurityLevel:          opts.SecurityLevel,
		ApprovalBasisRef:       strings.TrimSpace(opts.ApprovalBasisRef),
		ApplicableScopeJSON:    strings.TrimSpace(opts.ApplicableScopeJSON),
		SourceAuthorizationRef: strings.TrimSpace(opts.SourceAuthorizationRef),
		SourceFormat:           strings.TrimSpace(opts.SourceFormat),
		SearchabilityStatus:    opts.SearchabilityStatus,
		Status:                 knowledge_model.DataSourceStatusPendingReview,
		ConnectorType:          strings.TrimSpace(opts.ConnectorType),
		SecretRef:              strings.TrimSpace(opts.SecretRef),
		ReadScope:              strings.TrimSpace(opts.ReadScope),
		QueryTemplateRef:       strings.TrimSpace(opts.QueryTemplateRef),
		SchemaVersion:          strings.TrimSpace(opts.SchemaVersion),
		FieldMappingJSON:       strings.TrimSpace(opts.FieldMappingJSON),
		IncrementalCursorField: strings.TrimSpace(opts.IncrementalCursorField),
		DataRetentionRule:      strings.TrimSpace(opts.DataRetentionRule),
		MaskPolicyVersion:      strings.TrimSpace(opts.MaskPolicyVersion),
		CreatedBy:              actor.ID,
	}
	if err := db.WithTx(ctx, func(ctx context.Context) error {
		if err := db.Insert(ctx, source); err != nil {
			return fmt.Errorf("create knowledge data source: %w", err)
		}
		return insertDataSourceAudit(ctx, source, actor.ID, "data_source_created", "pending_review")
	}); err != nil {
		return nil, err
	}
	return source, nil
}

// EnableDataSource approves a pending source and makes it available for new
// governed uploads. The creator cannot approve their own source.
func EnableDataSource(ctx context.Context, sourceID, actorID int64) error {
	actor, err := user_model.GetUserByID(ctx, actorID)
	if err != nil {
		return fmt.Errorf("load data-source approver: %w", err)
	}
	if !actor.IsAdmin {
		return errors.New("data-source approval requires an administrator")
	}
	return db.WithTx(ctx, func(ctx context.Context) error {
		source, exists, err := db.GetByID[knowledge_model.DataSource](ctx, sourceID)
		if err != nil {
			return fmt.Errorf("load knowledge data source: %w", err)
		}
		if !exists || source.Status != knowledge_model.DataSourceStatusPendingReview || source.CreatedBy == actorID ||
			source.SecurityLevel == knowledge_model.SecurityLevelProhibited {
			return errors.New("knowledge data source cannot be approved")
		}
		source.Status = knowledge_model.DataSourceStatusEnabled
		updated, err := db.GetEngine(ctx).ID(source.ID).Cols("status").Update(source)
		if err != nil {
			return fmt.Errorf("enable knowledge data source: %w", err)
		}
		if updated != 1 {
			return errors.New("knowledge data source approval conflict")
		}
		return insertDataSourceAudit(ctx, source, actorID, "data_source_enabled", "approved")
	})
}

func loadManagedSpace(ctx context.Context, spaceID, actorID int64) (*knowledge_model.Space, *user_model.User, error) {
	if spaceID <= 0 || actorID <= 0 {
		return nil, nil, errors.New("invalid knowledge space or actor")
	}
	space, exists, err := db.GetByID[knowledge_model.Space](ctx, spaceID)
	if err != nil {
		return nil, nil, fmt.Errorf("load knowledge space: %w", err)
	}
	if !exists || space.Status != knowledge_model.SpaceStatusActive {
		return nil, nil, errors.New("knowledge space is not active")
	}
	actor, err := user_model.GetUserByID(ctx, actorID)
	if err != nil {
		return nil, nil, fmt.Errorf("load knowledge actor: %w", err)
	}
	if actor.ID != space.OwnerID && !actor.IsAdmin {
		return nil, nil, errors.New("knowledge space operation is not authorized")
	}
	return space, actor, nil
}

func validateDataSourceOptions(opts CreateDataSourceOptions) error {
	if !dataSourceCodePattern.MatchString(opts.SourceCode) || !validDisplayText(opts.Name, false) ||
		!validDataSourceType(opts.SourceType) || !validDisplayText(opts.BusinessDomainCode, false) ||
		!validDisplayText(opts.ContentTypeCode, false) || !validDisplayText(opts.SourceDepartment, false) ||
		strings.TrimSpace(opts.ContentOwnerUsername) == "" || strings.TrimSpace(opts.MaintenanceUsername) == "" ||
		opts.EffectiveUnix <= 0 || !validReviewFrequency(opts.ReviewFrequency) || !validSecurityLevel(opts.SecurityLevel) ||
		!validSafeReference(opts.ApprovalBasisRef) || !validScopeJSON(opts.ApplicableScopeJSON) ||
		!sourceReference.MatchString(opts.SourceAuthorizationRef) || !validDisplayText(opts.SourceFormat, false) ||
		!validSearchabilityStatus(opts.SearchabilityStatus) || !validMaskPolicyVersion(opts.MaskPolicyVersion) {
		return errors.New("invalid knowledge data source input")
	}
	if opts.ExpiresUnix > 0 && opts.ExpiresUnix <= opts.EffectiveUnix {
		return errors.New("knowledge data source expiry must be after its effective date")
	}
	if opts.SourceType == knowledge_model.DataSourceTypeDatabase || opts.SourceType == knowledge_model.DataSourceTypeAPI {
		if !validConnectorOptions(opts) {
			return errors.New("database or API data source is missing required controlled-connection settings")
		}
	}
	return nil
}

func validDataSourceType(value knowledge_model.DataSourceType) bool {
	switch value {
	case knowledge_model.DataSourceTypePolicy, knowledge_model.DataSourceTypeProcedure, knowledge_model.DataSourceTypeProduct,
		knowledge_model.DataSourceTypeTraining, knowledge_model.DataSourceTypeFAQ, knowledge_model.DataSourceTypeCase,
		knowledge_model.DataSourceTypeTicket, knowledge_model.DataSourceTypePublicMaterial, knowledge_model.DataSourceTypeDatabase,
		knowledge_model.DataSourceTypeAPI, knowledge_model.DataSourceTypeFileRepository:
		return true
	default:
		return false
	}
}

func validReviewFrequency(value knowledge_model.ReviewFrequency) bool {
	return value == knowledge_model.ReviewFrequencyWeekly || value == knowledge_model.ReviewFrequencyMonthly ||
		value == knowledge_model.ReviewFrequencyQuarterly || value == knowledge_model.ReviewFrequencyEventTriggered
}

func validSecurityLevel(value knowledge_model.SecurityLevel) bool {
	return value == knowledge_model.SecurityLevelPublic || value == knowledge_model.SecurityLevelInternal ||
		value == knowledge_model.SecurityLevelRestricted || value == knowledge_model.SecurityLevelProhibited
}

func validSearchabilityStatus(value knowledge_model.SearchabilityStatus) bool {
	return value == knowledge_model.SearchabilityStatusSearchable || value == knowledge_model.SearchabilityStatusOCRRequired ||
		value == knowledge_model.SearchabilityStatusNotSearchable
}

func validConnectorOptions(opts CreateDataSourceOptions) bool {
	if opts.SourceType == knowledge_model.DataSourceTypeDatabase && opts.ConnectorType != "mysql" {
		return false
	}
	return validDisplayText(opts.ConnectorType, false) && validSecretRef(opts.SecretRef) && validSafeReference(opts.QueryTemplateRef) &&
		validDisplayText(opts.SchemaVersion, false) && validScopeJSON(opts.FieldMappingJSON) &&
		validDisplayText(opts.IncrementalCursorField, false) && validDisplayText(opts.DataRetentionRule, false) && validScopeJSON(opts.ReadScope)
}

func validMaskPolicyVersion(value string) bool {
	return regexp.MustCompile(`^mask-policy-[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`).MatchString(strings.TrimSpace(value))
}

func validDocumentVersion(value string) bool {
	return documentVersion.MatchString(strings.TrimSpace(value))
}

func validRevisionRelation(value string) bool {
	return value == "new" || value == "revision" || value == "replacement" || value == "supplement"
}

func validSafeReference(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 255 || strings.ContainsAny(value, "\\\x00\n\r") {
		return false
	}
	lower := strings.ToLower(value)
	return !strings.Contains(lower, "password") && !strings.Contains(lower, "api_key") && !strings.Contains(lower, "token=") &&
		!strings.HasPrefix(lower, "/") && !regexp.MustCompile(`^[a-z]:`).MatchString(lower)
}

func validSecretRef(value string) bool {
	return regexp.MustCompile(`^secret-ref:[A-Za-z0-9][A-Za-z0-9._/-]{0,255}$`).MatchString(strings.TrimSpace(value))
}

func validScopeJSON(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) == 0 || len(value) > 16*1024 || !json.Valid([]byte(value)) {
		return false
	}
	var object map[string]json.RawMessage
	return json.Unmarshal([]byte(value), &object) == nil && len(object) > 0
}

func insertDataSourceAudit(ctx context.Context, source *knowledge_model.DataSource, actorID int64, action, result string) error {
	return db.Insert(ctx, &knowledge_model.AuditEvent{
		ActorID:      actorID,
		Action:       action,
		EntityType:   "data_source",
		EntityID:     source.ID,
		SpaceID:      source.SpaceID,
		Result:       result,
		ReasonCode:   "governance",
		TraceID:      fmt.Sprintf("data-source-%d-%d", source.ID, time.Now().UnixNano()),
		MetadataJSON: fmt.Sprintf(`{"source_code":%q,"source_type":%q,"security_level":%q}`, source.SourceCode, source.SourceType, source.SecurityLevel),
	})
}
