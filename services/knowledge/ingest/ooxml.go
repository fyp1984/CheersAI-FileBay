// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package ingest

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"path"
	"path/filepath"
	"strings"
)

const sampleBytes = 512

// OOXMLPolicy bounds ZIP parsing before a masked Office artifact is accepted.
type OOXMLPolicy struct {
	MaxArchiveBytes           int64
	MaxEntries                int
	MaxEntryUncompressedBytes int64
	MaxUncompressedBytes      int64
	MaxCompressionRatio       uint64
}

// OOXMLInspection contains only bounded structural facts, never document body.
type OOXMLInspection struct {
	MIMEType          string
	EntryCount        int
	UncompressedBytes int64
}

// DefaultOOXMLPolicy returns the production MVP archive resource limits.
func DefaultOOXMLPolicy() OOXMLPolicy {
	return OOXMLPolicy{
		MaxArchiveBytes:           defaultMaxFileBytes,
		MaxEntries:                4096,
		MaxEntryUncompressedBytes: 64 << 20,
		MaxUncompressedBytes:      256 << 20,
		MaxCompressionRatio:       200,
	}
}

// DetectArtifactMIME validates the artifact signature and returns the MIME type
// used by manifest comparison. OOXML is identified by package structure, not by
// its generic ZIP signature or file extension alone.
func DetectArtifactMIME(fileName string, reader io.ReaderAt, size int64) (string, error) {
	if reader == nil || size <= 0 || !isSafeBaseName(fileName) {
		return "", ErrUnsupportedFileType
	}
	extension := strings.ToLower(filepath.Ext(fileName))
	switch extension {
	case ".docx", ".pptx", ".xlsx":
		inspection, err := InspectOOXML(fileName, reader, size, DefaultOOXMLPolicy())
		if err != nil {
			return "", err
		}
		return inspection.MIMEType, nil
	case ".pdf":
		header := make([]byte, 5)
		if _, err := reader.ReadAt(header, 0); err != nil || string(header) != "%PDF-" {
			return "", ErrUnsupportedFileType
		}
		return "application/pdf", nil
	case ".md", ".txt":
		length := size
		if length > sampleBytes {
			length = sampleBytes
		}
		sample := make([]byte, length)
		read, err := reader.ReadAt(sample, 0)
		if err != nil && !errors.Is(err, io.EOF) {
			return "", ErrUnsupportedFileType
		}
		detected := http.DetectContentType(sample[:read])
		if !strings.HasPrefix(detected, "text/plain") && detected != "application/octet-stream" {
			return "", ErrUnsupportedFileType
		}
		if extension == ".md" {
			return "text/markdown", nil
		}
		return "text/plain", nil
	default:
		return "", ErrUnsupportedFileType
	}
}

// InspectOOXML validates central-directory integrity, safe entry names, package
// kind, decompressed sizes, entry count, and compression ratio.
func InspectOOXML(fileName string, reader io.ReaderAt, size int64, policy OOXMLPolicy) (*OOXMLInspection, error) {
	if reader == nil || size <= 0 || !isSafeBaseName(fileName) || !validOOXMLPolicy(policy) || size > policy.MaxArchiveBytes {
		return nil, ErrUnsupportedFileType
	}
	extension := strings.ToLower(filepath.Ext(fileName))
	wantMIME, allowed := map[string]string{
		".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
		".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	}[extension]
	if !allowed {
		return nil, ErrUnsupportedFileType
	}

	archive, err := zip.NewReader(reader, size)
	if err != nil || len(archive.File) == 0 || len(archive.File) > policy.MaxEntries {
		return nil, ErrUnsupportedFileType
	}
	var (
		contentTypesXML  []byte
		relationshipsXML []byte
		packageMIME      string
		mainPart         string
		totalBytes       int64
	)
	for _, entry := range archive.File {
		if !safeArchiveEntry(entry.Name) || entry.Flags&0x1 != 0 {
			return nil, ErrUnsupportedFileType
		}
		if entry.FileInfo().IsDir() {
			continue
		}
		uncompressed := entry.UncompressedSize64
		compressed := entry.CompressedSize64
		if uncompressed > uint64(policy.MaxEntryUncompressedBytes) ||
			uncompressed > uint64(policy.MaxUncompressedBytes)-uint64(totalBytes) {
			return nil, ErrUnsupportedFileType
		}
		if uncompressed > 0 && compressionRatioExceeded(uncompressed, compressed, policy.MaxCompressionRatio) {
			return nil, ErrUnsupportedFileType
		}
		if forbiddenOOXMLEntry(entry.Name) {
			return nil, ErrUnsupportedFileType
		}

		entryReader, err := entry.Open()
		if err != nil {
			return nil, ErrUnsupportedFileType
		}
		var buffer bytes.Buffer
		writer := io.Writer(io.Discard)
		if shouldInspectOOXMLPart(entry.Name) {
			writer = &buffer
		}
		actual, copyErr := io.Copy(writer, io.LimitReader(entryReader, policy.MaxEntryUncompressedBytes+1))
		closeErr := entryReader.Close()
		if copyErr != nil || closeErr != nil || actual < 0 || uint64(actual) != uncompressed {
			return nil, ErrUnsupportedFileType
		}
		if strings.HasSuffix(strings.ToLower(entry.Name), ".rels") && unsafeRelationships(buffer.Bytes()) {
			return nil, ErrUnsupportedFileType
		}
		totalBytes += actual
		if totalBytes > policy.MaxUncompressedBytes {
			return nil, ErrUnsupportedFileType
		}

		switch entry.Name {
		case "[Content_Types].xml":
			contentTypesXML = buffer.Bytes()
		case "_rels/.rels":
			relationshipsXML = buffer.Bytes()
		case "word/document.xml":
			if packageMIME != "" {
				return nil, ErrUnsupportedFileType
			}
			packageMIME = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
			mainPart = entry.Name
		case "ppt/presentation.xml":
			if packageMIME != "" {
				return nil, ErrUnsupportedFileType
			}
			packageMIME = "application/vnd.openxmlformats-officedocument.presentationml.presentation"
			mainPart = entry.Name
		case "xl/workbook.xml":
			if packageMIME != "" {
				return nil, ErrUnsupportedFileType
			}
			packageMIME = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
			mainPart = entry.Name
		}
	}
	if len(contentTypesXML) == 0 || len(relationshipsXML) == 0 || packageMIME == "" || packageMIME != wantMIME || totalBytes <= 0 {
		return nil, ErrUnsupportedFileType
	}
	if !validContentTypesOverride(contentTypesXML, mainPart) || !validRootRelationship(relationshipsXML, mainPart) {
		return nil, ErrUnsupportedFileType
	}
	return &OOXMLInspection{
		MIMEType:          packageMIME,
		EntryCount:        len(archive.File),
		UncompressedBytes: totalBytes,
	}, nil
}

func validOOXMLPolicy(policy OOXMLPolicy) bool {
	return policy.MaxArchiveBytes > 0 && policy.MaxEntries > 0 && policy.MaxEntryUncompressedBytes > 0 &&
		policy.MaxUncompressedBytes > 0 && policy.MaxEntryUncompressedBytes <= policy.MaxUncompressedBytes &&
		policy.MaxCompressionRatio > 0
}

func safeArchiveEntry(name string) bool {
	if name == "" || len(name) > 512 || strings.ContainsAny(name, "\\:\x00") || strings.HasPrefix(name, "/") {
		return false
	}
	trimmed := strings.TrimSuffix(name, "/")
	cleaned := path.Clean(trimmed)
	return cleaned == trimmed && cleaned != "." && cleaned != ".." && !strings.HasPrefix(cleaned, "../")
}

func compressionRatioExceeded(uncompressed, compressed uint64, max uint64) bool {
	if compressed == 0 || max == 0 {
		return true
	}
	quotient := uncompressed / compressed
	remainder := uncompressed % compressed
	return quotient > max || (quotient == max && remainder > 0)
}

func forbiddenOOXMLEntry(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, "/vbaproject.bin") ||
		strings.Contains(lower, "/activex/") ||
		strings.Contains(lower, "/embeddings/")
}

func shouldInspectOOXMLPart(name string) bool {
	return name == "[Content_Types].xml" || name == "_rels/.rels" || strings.HasSuffix(strings.ToLower(name), ".rels")
}

func validContentTypesOverride(raw []byte, mainPart string) bool {
	type override struct {
		PartName    string `xml:"PartName,attr"`
		ContentType string `xml:"ContentType,attr"`
	}
	type types struct {
		Overrides []override `xml:"Override"`
	}
	var parsed types
	if err := xml.NewDecoder(bytes.NewReader(raw)).Decode(&parsed); err != nil {
		return false
	}
	target := "/" + mainPart
	for _, value := range parsed.Overrides {
		if value.PartName == target && strings.HasSuffix(value.ContentType, ".main+xml") {
			return true
		}
	}
	return false
}

func validRootRelationship(raw []byte, mainPart string) bool {
	parsed, ok := parseRelationships(raw)
	if !ok {
		return false
	}
	foundMain := false
	for _, value := range parsed.Relationships {
		if value.Type == "http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" {
			if value.Target != mainPart {
				return false
			}
			foundMain = true
		}
	}
	return foundMain
}

func unsafeRelationships(raw []byte) bool {
	parsed, ok := parseRelationships(raw)
	if !ok {
		return true
	}
	for _, value := range parsed.Relationships {
		if strings.EqualFold(value.TargetMode, "External") {
			return true
		}
	}
	return false
}

func parseRelationships(raw []byte) (relationships, bool) {
	var parsed relationships
	if err := xml.NewDecoder(bytes.NewReader(raw)).Decode(&parsed); err != nil {
		return relationships{}, false
	}
	return parsed, true
}

type relationship struct {
	Type       string `xml:"Type,attr"`
	Target     string `xml:"Target,attr"`
	TargetMode string `xml:"TargetMode,attr"`
}

type relationships struct {
	Relationships []relationship `xml:"Relationship"`
}
