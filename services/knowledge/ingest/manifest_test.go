// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package ingest_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"code.gitea.io/gitea/services/knowledge/ingest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const validSHA256 = "5bb1b064554ad5b0c665e1a437b62d85a1ce28e5e54b593f4f874b3b84d6e60b"

func TestValidateUploadManifestAcceptsOnlyExplicitMaskedSandboxArtifact(t *testing.T) {
	t.Parallel()

	manifest := validManifest()
	facts := validFacts()
	require.NoError(t, ingest.ValidateUploadManifest(manifest, facts, ingest.DefaultUploadPolicy()))
}

func TestValidateUploadManifestRejectsPrivacyIntegrityAndTypeViolations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*ingest.UploadManifest, *ingest.UploadFacts, *ingest.UploadPolicy)
	}{
		{
			name: "client did not declare masking",
			mutate: func(manifest *ingest.UploadManifest, _ *ingest.UploadFacts, _ *ingest.UploadPolicy) {
				manifest.Masked = false
			},
		},
		{
			name: "missing mask policy version",
			mutate: func(manifest *ingest.UploadManifest, _ *ingest.UploadFacts, _ *ingest.UploadPolicy) {
				manifest.MaskPolicyVersion = ""
			},
		},
		{
			name: "missing source authorization",
			mutate: func(manifest *ingest.UploadManifest, _ *ingest.UploadFacts, _ *ingest.UploadPolicy) {
				manifest.SourceAuthorization = ""
			},
		},
		{
			name: "malformed declared sha256",
			mutate: func(manifest *ingest.UploadManifest, _ *ingest.UploadFacts, _ *ingest.UploadPolicy) {
				manifest.ContentSHA256 = "not-a-sha"
			},
		},
		{
			name: "sha256 mismatch",
			mutate: func(_ *ingest.UploadManifest, facts *ingest.UploadFacts, _ *ingest.UploadPolicy) {
				facts.ContentSHA256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			},
		},
		{
			name: "declared size mismatch",
			mutate: func(_ *ingest.UploadManifest, facts *ingest.UploadFacts, _ *ingest.UploadPolicy) {
				facts.Size++
			},
		},
		{
			name: "empty artifact",
			mutate: func(manifest *ingest.UploadManifest, facts *ingest.UploadFacts, _ *ingest.UploadPolicy) {
				manifest.Size = 0
				facts.Size = 0
			},
		},
		{
			name: "file exceeds configured maximum",
			mutate: func(manifest *ingest.UploadManifest, facts *ingest.UploadFacts, policy *ingest.UploadPolicy) {
				manifest.Size = policy.MaxFileBytes + 1
				facts.Size = manifest.Size
			},
		},
		{
			name: "unsupported extension",
			mutate: func(manifest *ingest.UploadManifest, _ *ingest.UploadFacts, _ *ingest.UploadPolicy) {
				manifest.FileName = "masked-policy.exe"
				manifest.MIMEType = "application/x-msdownload"
			},
		},
		{
			name: "detected type contradicts declaration",
			mutate: func(_ *ingest.UploadManifest, facts *ingest.UploadFacts, _ *ingest.UploadPolicy) {
				facts.DetectedMIMEType = "application/x-msdownload"
			},
		},
		{
			name: "invalid manifest json",
			mutate: func(manifest *ingest.UploadManifest, _ *ingest.UploadFacts, _ *ingest.UploadPolicy) {
				manifest.MaskManifestJSON = json.RawMessage(`{"masked":`)
			},
		},
		{
			name: "local path key at root",
			mutate: func(manifest *ingest.UploadManifest, _ *ingest.UploadFacts, _ *ingest.UploadPolicy) {
				manifest.MaskManifestJSON = json.RawMessage(`{"masked":true,"local_path":"C:\\Users\\Alice\\secret.md"}`)
			},
		},
		{
			name: "source path key nested in array",
			mutate: func(manifest *ingest.UploadManifest, _ *ingest.UploadFacts, _ *ingest.UploadPolicy) {
				manifest.MaskManifestJSON = json.RawMessage(`{"masked":true,"items":[{"source_path":"/Users/alice/secret.md"}]}`)
			},
		},
		{
			name: "absolute windows path hidden under generic key",
			mutate: func(manifest *ingest.UploadManifest, _ *ingest.UploadFacts, _ *ingest.UploadPolicy) {
				manifest.MaskManifestJSON = json.RawMessage(`{"masked":true,"source":"D:\\private\\secret.md"}`)
			},
		},
		{
			name: "absolute unix path hidden under generic key",
			mutate: func(manifest *ingest.UploadManifest, _ *ingest.UploadFacts, _ *ingest.UploadPolicy) {
				manifest.MaskManifestJSON = json.RawMessage(`{"masked":true,"source":"/home/alice/private/secret.md"}`)
			},
		},
		{
			name: "unsupported schema version",
			mutate: func(manifest *ingest.UploadManifest, _ *ingest.UploadFacts, _ *ingest.UploadPolicy) {
				manifest.MaskManifestJSON = json.RawMessage(`{"schema_version":2,"masked":true,"findings_count":0,"categories":[]}`)
			},
		},
		{
			name: "manifest masked flag contradicts declaration",
			mutate: func(manifest *ingest.UploadManifest, _ *ingest.UploadFacts, _ *ingest.UploadPolicy) {
				manifest.MaskManifestJSON = json.RawMessage(`{"schema_version":1,"masked":false,"findings_count":0,"categories":[]}`)
			},
		},
		{
			name: "unknown manifest field",
			mutate: func(manifest *ingest.UploadManifest, _ *ingest.UploadFacts, _ *ingest.UploadPolicy) {
				manifest.MaskManifestJSON = json.RawMessage(`{"schema_version":1,"masked":true,"findings_count":0,"categories":[],"client_note":"unexpected"}`)
			},
		},
		{
			name: "negative findings count",
			mutate: func(manifest *ingest.UploadManifest, _ *ingest.UploadFacts, _ *ingest.UploadPolicy) {
				manifest.MaskManifestJSON = json.RawMessage(`{"schema_version":1,"masked":true,"findings_count":-1,"categories":[]}`)
			},
		},
		{
			name: "fractional findings count",
			mutate: func(manifest *ingest.UploadManifest, _ *ingest.UploadFacts, _ *ingest.UploadPolicy) {
				manifest.MaskManifestJSON = json.RawMessage(`{"schema_version":1,"masked":true,"findings_count":1.5,"categories":[]}`)
			},
		},
		{
			name: "unknown category enum",
			mutate: func(manifest *ingest.UploadManifest, _ *ingest.UploadFacts, _ *ingest.UploadPolicy) {
				manifest.MaskManifestJSON = json.RawMessage(`{"schema_version":1,"masked":true,"findings_count":1,"categories":["raw_document_body"]}`)
			},
		},
		{
			name: "overlong manifest string",
			mutate: func(manifest *ingest.UploadManifest, _ *ingest.UploadFacts, _ *ingest.UploadPolicy) {
				manifest.MaskManifestJSON = json.RawMessage(fmt.Sprintf(`{"schema_version":1,"masked":true,"findings_count":1,"categories":[%q]}`, strings.Repeat("x", 1025)))
			},
		},
		{
			name: "body-like field",
			mutate: func(manifest *ingest.UploadManifest, _ *ingest.UploadFacts, _ *ingest.UploadPolicy) {
				manifest.MaskManifestJSON = json.RawMessage(`{"schema_version":1,"masked":true,"findings_count":0,"categories":[],"content":"masked or original body"}`)
			},
		},
		{
			name: "api key field",
			mutate: func(manifest *ingest.UploadManifest, _ *ingest.UploadFacts, _ *ingest.UploadPolicy) {
				manifest.MaskManifestJSON = json.RawMessage(`{"schema_version":1,"masked":true,"findings_count":0,"categories":[],"api_key":"secret"}`)
			},
		},
		{
			name: "source authorization contains local path",
			mutate: func(manifest *ingest.UploadManifest, _ *ingest.UploadFacts, _ *ingest.UploadPolicy) {
				manifest.SourceAuthorization = `C:\Users\Alice\authorization.json`
			},
		},
		{
			name: "source authorization is unbounded",
			mutate: func(manifest *ingest.UploadManifest, _ *ingest.UploadFacts, _ *ingest.UploadPolicy) {
				manifest.SourceAuthorization = strings.Repeat("a", 1025)
			},
		},
		{
			name: "source authorization carries an api key",
			mutate: func(manifest *ingest.UploadManifest, _ *ingest.UploadFacts, _ *ingest.UploadPolicy) {
				manifest.SourceAuthorization = "api_key:ragflowProductionSecret"
			},
		},
		{
			name: "source authorization prefix is not approved",
			mutate: func(manifest *ingest.UploadManifest, _ *ingest.UploadFacts, _ *ingest.UploadPolicy) {
				manifest.SourceAuthorization = "repo-write-grant:2"
			},
		},
		{
			name: "mask policy does not use the versioned policy prefix",
			mutate: func(manifest *ingest.UploadManifest, _ *ingest.UploadFacts, _ *ingest.UploadPolicy) {
				manifest.MaskPolicyVersion = "api-key-secret"
			},
		},
		{
			name: "mask policy contains a local path",
			mutate: func(manifest *ingest.UploadManifest, _ *ingest.UploadFacts, _ *ingest.UploadPolicy) {
				manifest.MaskPolicyVersion = `C:\Users\Alice\mask-policy.json`
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest := validManifest()
			facts := validFacts()
			policy := ingest.DefaultUploadPolicy()
			tt.mutate(&manifest, &facts, &policy)
			assert.Error(t, ingest.ValidateUploadManifest(manifest, facts, policy))
		})
	}
}

func TestDetectArtifactMIMEAndInspectOOXML(t *testing.T) {
	t.Parallel()

	tests := []struct {
		fileName string
		kind     string
		wantMIME string
	}{
		{"masked.docx", "word", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
		{"masked.pptx", "ppt", "application/vnd.openxmlformats-officedocument.presentationml.presentation"},
		{"masked.xlsx", "xl", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"},
	}

	for _, tt := range tests {
		t.Run(tt.fileName, func(t *testing.T) {
			artifact := makeMinimalOOXML(t, tt.kind, 0, 0)
			mimeType, err := ingest.DetectArtifactMIME(tt.fileName, bytes.NewReader(artifact), int64(len(artifact)))
			require.NoError(t, err)
			assert.Equal(t, tt.wantMIME, mimeType)

			inspection, err := ingest.InspectOOXML(tt.fileName, bytes.NewReader(artifact), int64(len(artifact)), ingest.DefaultOOXMLPolicy())
			require.NoError(t, err)
			assert.Equal(t, tt.wantMIME, inspection.MIMEType)
			assert.Positive(t, inspection.EntryCount)
			assert.Positive(t, inspection.UncompressedBytes)
		})
	}
}

func TestInspectOOXMLRejectsSpoofingAndArchiveResourceAbuse(t *testing.T) {
	t.Parallel()

	t.Run("forged zip signature", func(t *testing.T) {
		forged := []byte("PK\x03\x04not-a-valid-central-directory")
		_, err := ingest.InspectOOXML("forged.docx", bytes.NewReader(forged), int64(len(forged)), ingest.DefaultOOXMLPolicy())
		assert.Error(t, err)
	})

	t.Run("extension contradicts OOXML package", func(t *testing.T) {
		pptx := makeMinimalOOXML(t, "ppt", 0, 0)
		_, err := ingest.InspectOOXML("forged.docx", bytes.NewReader(pptx), int64(len(pptx)), ingest.DefaultOOXMLPolicy())
		assert.Error(t, err)
	})

	t.Run("entry count exceeds policy", func(t *testing.T) {
		artifact := makeMinimalOOXML(t, "word", 5, 0)
		policy := ingest.DefaultOOXMLPolicy()
		policy.MaxEntries = 3
		_, err := ingest.InspectOOXML("many.docx", bytes.NewReader(artifact), int64(len(artifact)), policy)
		assert.Error(t, err)
	})

	t.Run("uncompressed size exceeds policy", func(t *testing.T) {
		artifact := makeMinimalOOXML(t, "word", 0, 4096)
		policy := ingest.DefaultOOXMLPolicy()
		policy.MaxUncompressedBytes = 1024
		_, err := ingest.InspectOOXML("large.docx", bytes.NewReader(artifact), int64(len(artifact)), policy)
		assert.Error(t, err)
	})

	t.Run("path traversal entry", func(t *testing.T) {
		artifact := makeOOXMLPackage(t, "word", map[string]string{"../escape.xml": "<escape/>"}, "", "", 0)
		_, err := ingest.InspectOOXML("traversal.docx", bytes.NewReader(artifact), int64(len(artifact)), ingest.DefaultOOXMLPolicy())
		assert.Error(t, err)
	})

	t.Run("single entry exceeds policy", func(t *testing.T) {
		artifact := makeMinimalOOXML(t, "word", 0, 4096)
		policy := ingest.DefaultOOXMLPolicy()
		policy.MaxEntryUncompressedBytes = 1024
		_, err := ingest.InspectOOXML("entry-large.docx", bytes.NewReader(artifact), int64(len(artifact)), policy)
		assert.Error(t, err)
	})

	t.Run("fractional compression ratio above boundary", func(t *testing.T) {
		artifact := makeOOXMLPackage(t, "word", map[string]string{"word/styles.xml": strings.Repeat("x", 128*1024+3)}, "", "", 0)
		archive, err := zip.NewReader(bytes.NewReader(artifact), int64(len(artifact)))
		require.NoError(t, err)
		var maxFloor uint64
		var exceedsFloor bool
		for _, entry := range archive.File {
			if entry.UncompressedSize64 == 0 || entry.CompressedSize64 == 0 {
				continue
			}
			floor := entry.UncompressedSize64 / entry.CompressedSize64
			if floor > maxFloor {
				maxFloor = floor
				exceedsFloor = entry.UncompressedSize64%entry.CompressedSize64 != 0
			} else if floor == maxFloor && entry.UncompressedSize64%entry.CompressedSize64 != 0 {
				exceedsFloor = true
			}
		}
		require.Positive(t, maxFloor)
		require.True(t, exceedsFloor, "fixture must exceed the integer ratio boundary")
		policy := ingest.DefaultOOXMLPolicy()
		policy.MaxCompressionRatio = maxFloor
		_, err = ingest.InspectOOXML("ratio.docx", bytes.NewReader(artifact), int64(len(artifact)), policy)
		assert.Error(t, err, "an exact ratio above the configured bound must not pass through integer truncation")
	})
}

func TestInspectOOXMLRequiresAuthenticPackageDeclarationsAndRejectsActiveContent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		extraEntries map[string]string
		contentTypes string
		relations    string
	}{
		{
			name:         "missing content type override",
			contentTypes: `<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"></Types>`,
		},
		{
			name:      "wrong office document relationship root",
			relations: `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="https://attacker.example/payload" TargetMode="External"/></Relationships>`,
		},
		{name: "macro project", extraEntries: map[string]string{"word/vbaProject.bin": "macro"}},
		{name: "active x control", extraEntries: map[string]string{"word/activeX/activeX1.bin": "active-x"}},
		{name: "embedded ole object", extraEntries: map[string]string{"word/embeddings/oleObject1.bin": "ole"}},
		{
			name:      "external relationship",
			relations: `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink" Target="https://attacker.example/track" TargetMode="External"/></Relationships>`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			artifact := makeOOXMLPackage(t, "word", tt.extraEntries, tt.contentTypes, tt.relations, 0)
			_, err := ingest.InspectOOXML("unsafe.docx", bytes.NewReader(artifact), int64(len(artifact)), ingest.DefaultOOXMLPolicy())
			assert.Error(t, err)
		})
	}
}

func makeMinimalOOXML(t *testing.T, root string, extraEntries, bodyBytes int) []byte {
	t.Helper()
	extra := make(map[string]string, extraEntries+1)
	for index := range extraEntries {
		extra[fmt.Sprintf("custom/extra-%d.xml", index)] = "<extra/>"
	}
	return makeOOXMLPackage(t, root, extra, "", "", bodyBytes)
}

func makeOOXMLPackage(t *testing.T, root string, extraEntries map[string]string, contentTypes, relationships string, bodyBytes int) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	entries := make(map[string]string, len(extraEntries)+3)
	var mainPart, mainContentType string
	switch root {
	case "word":
		mainPart = "word/document.xml"
		mainContentType = "application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"
		entries[mainPart] = `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"/>`
	case "ppt":
		mainPart = "ppt/presentation.xml"
		mainContentType = "application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"
		entries[mainPart] = `<p:presentation xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"/>`
	case "xl":
		mainPart = "xl/workbook.xml"
		mainContentType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"
		entries[mainPart] = `<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"/>`
	default:
		t.Fatalf("unknown OOXML root %q", root)
	}
	if contentTypes == "" {
		contentTypes = fmt.Sprintf(`<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Override PartName="/%s" ContentType="%s"/></Types>`, mainPart, mainContentType)
	}
	if relationships == "" {
		relationships = fmt.Sprintf(`<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="%s"/></Relationships>`, mainPart)
	}
	entries["[Content_Types].xml"] = contentTypes
	entries["_rels/.rels"] = relationships
	for name, content := range extraEntries {
		entries[name] = content
	}
	for name, content := range entries {
		entry, err := writer.Create(name)
		require.NoError(t, err)
		_, err = entry.Write([]byte(content))
		require.NoError(t, err)
	}
	if bodyBytes > 0 {
		entry, err := writer.Create(root + "/large.bin")
		require.NoError(t, err)
		_, err = entry.Write(bytes.Repeat([]byte("x"), bodyBytes))
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	return buffer.Bytes()
}

func TestValidateUploadManifestRejectsFileNamePathsAndTraversal(t *testing.T) {
	t.Parallel()

	for _, fileName := range []string{
		"../masked-policy.md",
		"knowledge/masked-policy.md",
		`knowledge\masked-policy.md`,
		`C:\Users\Alice\masked-policy.md`,
		"/home/alice/masked-policy.md",
		".\x00.md",
		"",
	} {
		t.Run(fileName, func(t *testing.T) {
			manifest := validManifest()
			manifest.FileName = fileName
			assert.Error(t, ingest.ValidateUploadManifest(manifest, validFacts(), ingest.DefaultUploadPolicy()))
		})
	}
}

func validManifest() ingest.UploadManifest {
	return ingest.UploadManifest{
		Masked:              true,
		FileName:            "masked-policy.md",
		ContentSHA256:       validSHA256,
		Size:                29,
		MIMEType:            "text/markdown",
		MaskPolicyVersion:   "mask-policy-2026-01",
		MaskManifestJSON:    json.RawMessage(`{"schema_version":1,"masked":true,"findings_count":2,"categories":["person_name"]}`),
		SourceAuthorization: "grant-id:grant_2",
	}
}

func validFacts() ingest.UploadFacts {
	return ingest.UploadFacts{
		ContentSHA256:    validSHA256,
		Size:             29,
		DetectedMIMEType: "text/markdown",
	}
}
