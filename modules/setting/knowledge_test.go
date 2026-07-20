// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package setting

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKnowledgeSettingsDefaultOff(t *testing.T) {
	cfg, err := NewConfigProviderFromData("")
	require.NoError(t, err)

	loadKnowledgeFrom(cfg)

	assert.False(t, Knowledge.Enabled)
	assert.EqualValues(t, 32<<20, Knowledge.MaxFileSize)
	assert.Equal(t, 10*time.Second, Knowledge.HTTPTimeout)
	assert.Equal(t, "ragflow-v0.26.4-profile-1", Knowledge.EngineProfileVersion)
	assert.Equal(t, 10, Knowledge.RetrieveMaxTopK)
	assert.Equal(t, 2048, Knowledge.RetrieveQueryMaxByte)
	assert.NoError(t, ValidateKnowledgeSettings(), "default-off installation must remain loadable without RAGFlow secrets")
}

func TestKnowledgeSettingsLoadExplicitProductionConfig(t *testing.T) {
	bindingDir := writeKnowledgeBinding(t, "ragflow-secret", "tenant-dataset")
	cfg, err := NewConfigProviderFromData(`
[knowledge]
ENABLED = true
RAGFLOW_BASE_URL = https://ragflow.internal.example
RAGFLOW_BINDING_DIR = ` + bindingDir + `
ENGINE_PROFILE_VERSION = profile-2026-07
ALLOW_LOOPBACK_HTTP = false
ALLOWED_EXTENSIONS = .pdf,.md
MAX_FILE_SIZE_MIB = 16
HTTP_TIMEOUT = 8s
POLL_INTERVAL = 7s
MAX_ATTEMPTS = 8
RETRIEVE_MAX_TOP_K = 6
RETRIEVE_QUERY_MAX_BYTES = 1024
`)
	require.NoError(t, err)

	loadKnowledgeFrom(cfg)

	assert.True(t, Knowledge.Enabled)
	assert.Equal(t, "https://ragflow.internal.example", Knowledge.RAGFlowBaseURL)
	assert.Equal(t, "ragflow-secret", Knowledge.RAGFlowAPIKey)
	assert.Equal(t, "tenant-dataset", Knowledge.RAGFlowDatasetID)
	assert.Equal(t, "profile-2026-07", Knowledge.EngineProfileVersion)
	assert.False(t, Knowledge.AllowLoopbackHTTP)
	assert.Equal(t, ".pdf,.md", Knowledge.AllowedExtensions)
	assert.EqualValues(t, 16<<20, Knowledge.MaxFileSize)
	assert.Equal(t, 8*time.Second, Knowledge.HTTPTimeout)
	assert.Equal(t, 7*time.Second, Knowledge.PollInterval)
	assert.Equal(t, 8, Knowledge.MaxAttempts)
	assert.Equal(t, 6, Knowledge.RetrieveMaxTopK)
	assert.Equal(t, 1024, Knowledge.RetrieveQueryMaxByte)
	assert.NoError(t, ValidateKnowledgeSettings())
}

func TestKnowledgeSettingsIgnoreLegacyInlineCredentials(t *testing.T) {
	cfg, err := NewConfigProviderFromData(`
[knowledge]
ENABLED = true
RAGFLOW_BASE_URL = https://ragflow.internal.example
RAGFLOW_API_KEY = legacy-secret
RAGFLOW_DATASET_ID = legacy-dataset
ENGINE_PROFILE_VERSION = profile-2026-07
`)
	require.NoError(t, err)

	loadKnowledgeFrom(cfg)

	assert.Empty(t, Knowledge.RAGFlowAPIKey)
	assert.Empty(t, Knowledge.RAGFlowDatasetID)
	assert.Error(t, ValidateKnowledgeSettings())
}

func TestEnabledKnowledgeSettingsRequireEveryRAGFlowBinding(t *testing.T) {
	base := map[string]string{
		"RAGFLOW_BASE_URL":       "https://ragflow.internal.example",
		"ENGINE_PROFILE_VERSION": "profile-2026-07",
	}

	for _, missing := range []string{"RAGFLOW_BASE_URL", "ENGINE_PROFILE_VERSION"} {
		t.Run(missing, func(t *testing.T) {
			values := make(map[string]string, len(base))
			for key, value := range base {
				if key != missing {
					values[key] = value
				}
			}
			loadKnowledgeTestConfig(t, knowledgeConfig(values, ""), true, false)
			assert.Error(t, ValidateKnowledgeSettings())
		})
	}

	t.Run("api key binding", func(t *testing.T) {
		bindingDir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(bindingDir, knowledgeBindingDatasetIDFile), []byte("tenant-dataset\n"), 0o600))
		loadKnowledgeTestConfig(t, knowledgeConfig(base, "RAGFLOW_BINDING_DIR = "+bindingDir), true, false)
		assert.Error(t, ValidateKnowledgeSettings())
	})
}

func TestKnowledgeSettingsTransportPolicyDependsOnRuntimeMode(t *testing.T) {
	tests := []struct {
		name      string
		baseURL   string
		allowHTTP bool
		isProd    bool
		testing   bool
		wantError bool
	}{
		{"production https", "https://ragflow.internal.example", false, true, false, false},
		{"production loopback http forbidden even when opted in", "http://127.0.0.1:9380", true, true, false, true},
		{"production remote http forbidden", "http://ragflow.internal.example", true, true, false, true},
		{"development loopback requires explicit opt in", "http://127.0.0.1:9380", false, false, false, true},
		{"development loopback allowed", "http://127.0.0.1:9380", true, false, false, false},
		{"testing localhost allowed", "http://localhost:9380", true, true, true, false},
		{"testing remote http forbidden", "http://ragflow.internal.example", true, true, true, true},
		{"production internal docker hostname explicitly allowed", "http://ragflow:9380", false, true, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			extra := "ALLOW_LOOPBACK_HTTP = false"
			if tt.allowHTTP {
				extra = "ALLOW_LOOPBACK_HTTP = true"
			}
			if tt.name == "production internal docker hostname explicitly allowed" {
				extra = "ALLOW_INTERNAL_HTTP = true\nINTERNAL_HTTP_HOST = ragflow"
			}
			loadKnowledgeTestConfig(t, knowledgeConfig(map[string]string{
				"RAGFLOW_BASE_URL":       tt.baseURL,
				"ENGINE_PROFILE_VERSION": "profile-2026-07",
			}, extra), tt.isProd, tt.testing)
			err := ValidateKnowledgeSettings()
			if tt.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestKnowledgeSettingsRejectUnsafeNumericBoundsWithoutOverflow(t *testing.T) {
	tests := []struct {
		name  string
		extra string
	}{
		{"max file size overflows int64 bytes", "MAX_FILE_SIZE_MIB = 8796093022208"},
		{"max attempts zero", "MAX_ATTEMPTS = 0"},
		{"max attempts negative", "MAX_ATTEMPTS = -1"},
		{"max attempts unreasonable", "MAX_ATTEMPTS = 1001"},
		{"timeout zero", "HTTP_TIMEOUT = 0s"},
		{"timeout negative", "HTTP_TIMEOUT = -1s"},
		{"timeout exceeds contract", "HTTP_TIMEOUT = 11s"},
		{"retrieval top k exceeds policy", "RETRIEVE_MAX_TOP_K = 51"},
		{"retrieval question limit too small", "RETRIEVE_QUERY_MAX_BYTES = 31"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loadKnowledgeTestConfig(t, knowledgeConfig(map[string]string{
				"RAGFLOW_BASE_URL":       "https://ragflow.internal.example",
				"ENGINE_PROFILE_VERSION": "profile-2026-07",
			}, tt.extra), true, false)
			assert.Error(t, ValidateKnowledgeSettings())
			assert.Greater(t, Knowledge.MaxFileSize, int64(0), "unsafe MiB input must never wrap to zero or negative bytes")
		})
	}
}

func loadKnowledgeTestConfig(t *testing.T, data string, isProd, isTesting bool) {
	t.Helper()
	previousKnowledge, previousProd, previousTesting := Knowledge, IsProd, IsInTesting
	t.Cleanup(func() {
		Knowledge, IsProd, IsInTesting = previousKnowledge, previousProd, previousTesting
	})
	if !strings.Contains(data, "RAGFLOW_BINDING_DIR") {
		data += "RAGFLOW_BINDING_DIR = " + writeKnowledgeBinding(t, "ragflow-secret", "tenant-dataset") + "\n"
	}
	cfg, err := NewConfigProviderFromData(data)
	require.NoError(t, err)
	IsProd, IsInTesting = isProd, isTesting
	loadKnowledgeFrom(cfg)
}

func writeKnowledgeBinding(t *testing.T, apiKey, datasetID string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, knowledgeBindingAPIKeyFile), []byte(apiKey+"\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, knowledgeBindingDatasetIDFile), []byte(datasetID+"\n"), 0o600))
	return dir
}

func knowledgeConfig(values map[string]string, extra string) string {
	data := "[knowledge]\nENABLED = true\n"
	for _, key := range []string{"RAGFLOW_BASE_URL", "ENGINE_PROFILE_VERSION"} {
		if value, ok := values[key]; ok {
			data += key + " = " + value + "\n"
		}
	}
	if extra != "" {
		data += extra + "\n"
	}
	return data
}
