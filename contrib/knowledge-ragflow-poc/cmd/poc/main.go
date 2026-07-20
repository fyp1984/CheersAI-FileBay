// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	poc "code.gitea.io/gitea/contrib/knowledge-ragflow-poc"
)

const (
	networkOptInEnvironment = "CHEERSAI_POC_ALLOW_NETWORK"
	apiKeyEnvironment       = "RAGFLOW_API_KEY"
)

func main() {
	documentID, err := run(context.Background(), os.Args[1:])
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_, _ = fmt.Fprintf(os.Stdout, "published document_id=%s\n", documentID)
}

func run(ctx context.Context, arguments []string) (string, error) {
	if os.Getenv(networkOptInEnvironment) != "1" {
		return "", poc.ErrNetworkDisabled
	}

	flags := flag.NewFlagSet("knowledge-ragflow-poc", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	baseURL := flags.String("base-url", "", "RAGFlow base URL")
	datasetID := flags.String("dataset-id", "", "existing RAGFlow dataset ID")
	maskedFile := flags.String("masked-file", "", "explicitly selected, already-masked local file")
	sourceID := flags.String("source-id", "", "opaque source ID, never a path")
	digest := flags.String("sha256", "", "expected SHA-256 of the masked file")
	maskPolicy := flags.String("mask-policy-version", "", "mask policy version")
	publicationID := flags.String("publication-id", "", "FileBay publication ID")
	generationText := flags.String("publication-generation", "", "positive FileBay publication generation")
	if err := flags.Parse(arguments); err != nil || flags.NArg() != 0 {
		return "", errors.New("invalid PoC command arguments")
	}

	generation, err := strconv.Atoi(*generationText)
	if err != nil || generation < 1 {
		return "", errors.New("publication generation must be a positive integer")
	}
	maskedBytes, err := readMaskedFile(*maskedFile)
	if err != nil {
		return "", err
	}
	client, err := poc.NewClient(poc.Config{
		BaseURL:        *baseURL,
		APIKey:         os.Getenv(apiKeyEnvironment),
		DatasetID:      *datasetID,
		NetworkEnabled: true,
	})
	if err != nil {
		return "", err
	}
	result, err := client.Publish(ctx, poc.MaskedArtifact{
		Bytes:                 maskedBytes,
		FileName:              filepath.Base(*maskedFile),
		SourceID:              *sourceID,
		SHA256:                *digest,
		MaskPolicyVersion:     *maskPolicy,
		PublicationID:         *publicationID,
		PublicationGeneration: generation,
		Masked:                true,
	})
	if err != nil {
		return "", err
	}
	return result.DocumentID, nil
}

func readMaskedFile(path string) ([]byte, error) {
	if path == "" {
		return nil, errors.New("an explicitly selected masked file is required")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("could not open the explicitly selected masked file")
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, poc.MaxArtifactBytes+1))
	if err != nil {
		return nil, errors.New("could not read the explicitly selected masked file")
	}
	if len(data) == 0 || len(data) > poc.MaxArtifactBytes {
		return nil, errors.New("masked file size is outside the allowed range")
	}
	return data, nil
}
