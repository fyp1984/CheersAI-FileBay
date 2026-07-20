// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package knowledge

import "code.gitea.io/gitea/modules/timeutil"

// LifecycleState is the complete, fail-closed publication lifecycle tuple.
type LifecycleState struct {
	Governance GovernanceStatus
	Validity   ValidityStatus
	Index      IndexStatus
}

// CanTransition reports whether to is one explicitly declared lifecycle edge
// from from. Changing multiple dimensions is permitted only for the two atomic
// governance edges defined by the production workflow.
func CanTransition(from, to LifecycleState) bool {
	allowed := [][2]LifecycleState{
		{{GovernanceStatusDraft, ValidityStatusUnpublished, IndexStatusUnindexed}, {GovernanceStatusPending, ValidityStatusUnpublished, IndexStatusUnindexed}},
		{{GovernanceStatusPending, ValidityStatusUnpublished, IndexStatusUnindexed}, {GovernanceStatusRejected, ValidityStatusUnpublished, IndexStatusUnindexed}},
		{{GovernanceStatusPending, ValidityStatusUnpublished, IndexStatusUnindexed}, {GovernanceStatusApproved, ValidityStatusScheduled, IndexStatusUnindexed}},
		{{GovernanceStatusApproved, ValidityStatusScheduled, IndexStatusUnindexed}, {GovernanceStatusApproved, ValidityStatusScheduled, IndexStatusQueued}},
		{{GovernanceStatusApproved, ValidityStatusScheduled, IndexStatusQueued}, {GovernanceStatusApproved, ValidityStatusScheduled, IndexStatusIndexing}},
		{{GovernanceStatusApproved, ValidityStatusScheduled, IndexStatusIndexing}, {GovernanceStatusApproved, ValidityStatusScheduled, IndexStatusEvaluation}},
		{{GovernanceStatusApproved, ValidityStatusScheduled, IndexStatusEvaluation}, {GovernanceStatusApproved, ValidityStatusCurrent, IndexStatusSearchable}},
		{{GovernanceStatusApproved, ValidityStatusCurrent, IndexStatusSearchable}, {GovernanceStatusApproved, ValidityStatusUnpublished, IndexStatusSearchable}},
		{{GovernanceStatusApproved, ValidityStatusUnpublished, IndexStatusSearchable}, {GovernanceStatusApproved, ValidityStatusUnpublished, IndexStatusDeleting}},
		{{GovernanceStatusApproved, ValidityStatusUnpublished, IndexStatusDeleting}, {GovernanceStatusApproved, ValidityStatusUnpublished, IndexStatusDeleted}},
		{{GovernanceStatusApproved, ValidityStatusScheduled, IndexStatusIndexing}, {GovernanceStatusApproved, ValidityStatusScheduled, IndexStatusFailed}},
		{{GovernanceStatusApproved, ValidityStatusScheduled, IndexStatusFailed}, {GovernanceStatusApproved, ValidityStatusScheduled, IndexStatusQueued}},
	}
	for _, edge := range allowed {
		if edge[0] == from && edge[1] == to {
			return true
		}
	}
	return false
}

// IsPublished reports whether every required governance, time, index, pointer,
// and revocation check agrees that this snapshot is currently retrievable.
func (p *Publication) IsPublished(now timeutil.TimeStamp, currentPublicationID, currentRevocationGeneration int64) bool {
	if p == nil || p.ID <= 0 || !p.IsCurrent {
		return false
	}
	if p.GovernanceStatus != GovernanceStatusApproved || p.ValidityStatus != ValidityStatusCurrent || p.IndexStatus != IndexStatusSearchable {
		return false
	}
	if p.ID != currentPublicationID || p.RevocationGeneration != currentRevocationGeneration {
		return false
	}
	if p.EffectiveUnix > 0 && now < p.EffectiveUnix {
		return false
	}
	return p.ExpiresUnix == 0 || now < p.ExpiresUnix
}
