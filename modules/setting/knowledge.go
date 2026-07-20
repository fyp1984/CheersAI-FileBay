// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package setting

import (
	"errors"
	"math"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const defaultKnowledgeAllowedExtensions = ".pdf,.docx,.pptx,.xlsx,.md,.txt"

// KnowledgeSetting controls the production knowledge-governance integration.
// It is disabled by default so existing FileBay behavior is unchanged.
type KnowledgeSetting struct {
	Enabled              bool
	RAGFlowBaseURL       string
	RAGFlowAPIKey        string
	RAGFlowDatasetID     string
	EngineProfileVersion string
	AllowLoopbackHTTP    bool
	AllowInternalHTTP    bool
	InternalHTTPHost     string
	AllowedExtensions    string
	MaxFileSize          int64
	HTTPTimeout          time.Duration
	PollInterval         time.Duration
	MaxAttempts          int
	RetrieveMaxTopK      int
	RetrieveQueryMaxByte int

	engineProfileConfigured bool
	invalidConfiguration    bool
}

// Knowledge is the configured knowledge-base integration.
var Knowledge KnowledgeSetting

func loadKnowledgeFrom(rootCfg ConfigProvider) {
	Knowledge = KnowledgeSetting{
		Enabled:              false,
		EngineProfileVersion: "ragflow-v0.26.4-profile-1",
		AllowedExtensions:    defaultKnowledgeAllowedExtensions,
		MaxFileSize:          32 << 20,
		HTTPTimeout:          10 * time.Second,
		PollInterval:         5 * time.Second,
		MaxAttempts:          5,
		RetrieveMaxTopK:      10,
		RetrieveQueryMaxByte: 2048,
	}

	sec, _ := rootCfg.GetSection("knowledge")
	if sec == nil {
		return
	}

	Knowledge.Enabled = sec.Key("ENABLED").MustBool(Knowledge.Enabled)
	Knowledge.RAGFlowBaseURL = sec.Key("RAGFLOW_BASE_URL").String()
	Knowledge.RAGFlowAPIKey = sec.Key("RAGFLOW_API_KEY").String()
	Knowledge.RAGFlowDatasetID = sec.Key("RAGFLOW_DATASET_ID").String()
	Knowledge.engineProfileConfigured = sec.HasKey("ENGINE_PROFILE_VERSION") && strings.TrimSpace(sec.Key("ENGINE_PROFILE_VERSION").String()) != ""
	Knowledge.EngineProfileVersion = sec.Key("ENGINE_PROFILE_VERSION").MustString(Knowledge.EngineProfileVersion)
	Knowledge.AllowLoopbackHTTP = sec.Key("ALLOW_LOOPBACK_HTTP").MustBool(false)
	Knowledge.AllowInternalHTTP = sec.Key("ALLOW_INTERNAL_HTTP").MustBool(false)
	Knowledge.InternalHTTPHost = strings.TrimSpace(sec.Key("INTERNAL_HTTP_HOST").String())
	Knowledge.AllowedExtensions = sec.Key("ALLOWED_EXTENSIONS").MustString(Knowledge.AllowedExtensions)
	if sec.HasKey("MAX_FILE_SIZE_MIB") {
		maxFileSizeMiB, err := strconv.ParseInt(strings.TrimSpace(sec.Key("MAX_FILE_SIZE_MIB").String()), 10, 64)
		if err != nil || maxFileSizeMiB <= 0 || maxFileSizeMiB > math.MaxInt64>>20 {
			Knowledge.invalidConfiguration = true
		} else {
			Knowledge.MaxFileSize = maxFileSizeMiB << 20
		}
	}
	if sec.HasKey("HTTP_TIMEOUT") {
		value, err := time.ParseDuration(strings.TrimSpace(sec.Key("HTTP_TIMEOUT").String()))
		if err != nil {
			Knowledge.invalidConfiguration = true
		} else {
			Knowledge.HTTPTimeout = value
		}
	}
	if sec.HasKey("POLL_INTERVAL") {
		value, err := time.ParseDuration(strings.TrimSpace(sec.Key("POLL_INTERVAL").String()))
		if err != nil {
			Knowledge.invalidConfiguration = true
		} else {
			Knowledge.PollInterval = value
		}
	}
	if sec.HasKey("MAX_ATTEMPTS") {
		value, err := strconv.ParseInt(strings.TrimSpace(sec.Key("MAX_ATTEMPTS").String()), 10, 32)
		if err != nil {
			Knowledge.invalidConfiguration = true
		} else {
			Knowledge.MaxAttempts = int(value)
		}
	}
	if sec.HasKey("RETRIEVE_MAX_TOP_K") {
		Knowledge.RetrieveMaxTopK = sec.Key("RETRIEVE_MAX_TOP_K").MustInt(Knowledge.RetrieveMaxTopK)
	}
	if sec.HasKey("RETRIEVE_QUERY_MAX_BYTES") {
		Knowledge.RetrieveQueryMaxByte = sec.Key("RETRIEVE_QUERY_MAX_BYTES").MustInt(Knowledge.RetrieveQueryMaxByte)
	}
}

// ValidateKnowledgeSettings validates the enabled integration without ever
// including configured secret values in returned errors.
func ValidateKnowledgeSettings() error {
	if !Knowledge.Enabled {
		return nil
	}
	if Knowledge.invalidConfiguration {
		return errors.New("invalid knowledge numeric configuration")
	}
	if strings.TrimSpace(Knowledge.RAGFlowBaseURL) == "" || strings.TrimSpace(Knowledge.RAGFlowAPIKey) == "" ||
		strings.TrimSpace(Knowledge.RAGFlowDatasetID) == "" || !Knowledge.engineProfileConfigured ||
		strings.TrimSpace(Knowledge.EngineProfileVersion) == "" {
		return errors.New("incomplete knowledge RAGFlow binding")
	}
	if Knowledge.MaxFileSize <= 0 || Knowledge.HTTPTimeout <= 0 || Knowledge.HTTPTimeout > 10*time.Second ||
		Knowledge.PollInterval <= 0 || Knowledge.MaxAttempts <= 0 || Knowledge.MaxAttempts > 1000 {
		return errors.New("knowledge numeric configuration is outside policy")
	}
	if Knowledge.RetrieveMaxTopK <= 0 || Knowledge.RetrieveMaxTopK > 50 || Knowledge.RetrieveQueryMaxByte < 32 || Knowledge.RetrieveQueryMaxByte > 16*1024 {
		return errors.New("knowledge retrieval limits are outside policy")
	}
	if strings.TrimSpace(Knowledge.AllowedExtensions) == "" {
		return errors.New("knowledge file type policy is empty")
	}

	parsed, err := url.Parse(strings.TrimSpace(Knowledge.RAGFlowBaseURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" ||
		parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return errors.New("invalid knowledge RAGFlow URL")
	}
	switch parsed.Scheme {
	case "https":
		return nil
	case "http":
		if Knowledge.AllowLoopbackHTTP && isKnowledgeLoopbackHost(parsed.Hostname()) && (!IsProd || IsInTesting) {
			return nil
		}
		if Knowledge.AllowInternalHTTP && isKnowledgeInternalDockerHost(parsed.Hostname(), Knowledge.InternalHTTPHost) {
			return nil
		}
		return errors.New("knowledge RAGFlow transport policy rejected")
	default:
		return errors.New("knowledge RAGFlow transport policy rejected")
	}
}

func isKnowledgeInternalDockerHost(host, configuredHost string) bool {
	configuredHost = strings.TrimSpace(configuredHost)
	return configuredHost != "" && strings.EqualFold(host, configuredHost) &&
		!strings.ContainsAny(configuredHost, ".:/\\") && !strings.EqualFold(configuredHost, "localhost")
}

func isKnowledgeLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
