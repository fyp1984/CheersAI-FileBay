// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package knowledge

import (
	"testing"
	"time"

	knowledge_model "code.gitea.io/gitea/models/knowledge"
	"code.gitea.io/gitea/modules/timeutil"

	"github.com/stretchr/testify/assert"
)

func TestDataSourceFreshnessCreatesReviewReminders(t *testing.T) {
	now := timeutil.TimeStamp(time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC).Unix())
	base := timeutil.TimeStamp(time.Date(2026, time.June, 1, 12, 0, 0, 0, time.UTC).Unix())
	source := knowledge_model.DataSource{
		Status:          knowledge_model.DataSourceStatusEnabled,
		ReviewFrequency: knowledge_model.ReviewFrequencyMonthly,
		EffectiveUnix:   base,
	}

	overdue := evaluateDataSourceFreshness(source, 0, now)
	assert.True(t, overdue.IsTask)
	assert.True(t, overdue.IsOverdue)
	assert.Contains(t, overdue.Label, "逾期")

	confirmed := timeutil.TimeStamp(time.Date(2026, time.July, 18, 12, 0, 0, 0, time.UTC).Unix())
	current := evaluateDataSourceFreshness(source, confirmed, now)
	assert.False(t, current.IsTask)
	assert.Equal(t, "复审正常", current.Label)
}

func TestDataSourceFreshnessSkipsEventTriggeredSources(t *testing.T) {
	now := timeutil.TimeStamp(time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC).Unix())
	source := knowledge_model.DataSource{
		Status:          knowledge_model.DataSourceStatusEnabled,
		ReviewFrequency: knowledge_model.ReviewFrequencyEventTriggered,
		EffectiveUnix:   timeutil.TimeStamp(time.Date(2026, time.June, 1, 12, 0, 0, 0, time.UTC).Unix()),
	}

	state := evaluateDataSourceFreshness(source, 0, now)
	assert.False(t, state.IsTask)
	assert.Equal(t, "事件触发时复审", state.Label)
}
