// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package connector

import (
	"testing"

	knowledge_model "code.gitea.io/gitea/models/knowledge"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseMySQLConfigurationOnlyAcceptsDeidentifiedViewContract(t *testing.T) {
	source := &knowledge_model.DataSource{ConnectorType: "mysql", SecretRef: "secret-ref:env/KB_MYSQL_DSN", ReadScope: `{"view":"v_kb_masked_faq"}`, FieldMappingJSON: `{"id":"record_id","title":"title","content":"content","cursor":"updated_at"}`, IncrementalCursorField: "updated_at"}
	scope, mapping, envName, err := ParseMySQLConfiguration(source)
	require.NoError(t, err)
	assert.Equal(t, "v_kb_masked_faq", scope.View)
	assert.Equal(t, "record_id", mapping.ID)
	assert.Equal(t, "KB_MYSQL_DSN", envName)

	for _, mutate := range []func(*knowledge_model.DataSource){
		func(value *knowledge_model.DataSource) { value.ReadScope = `{"view":"customer"}` },
		func(value *knowledge_model.DataSource) { value.SecretRef = "secret-ref:env/DB_PASSWORD" },
		func(value *knowledge_model.DataSource) {
			value.FieldMappingJSON = `{"id":"id","title":"title","content":"content","cursor":"updated-at"}`
		},
	} {
		copy := *source
		mutate(&copy)
		_, _, _, err := ParseMySQLConfiguration(&copy)
		assert.Error(t, err)
	}
}
