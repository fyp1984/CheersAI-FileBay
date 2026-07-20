# Knowledge RAGFlow PoC Instructions

## Goal

Build an isolated, test-only proof of concept for publishing client-masked FileBay artifacts to RAGFlow without changing the FileBay runtime.

## Repository Structure

- `spec/`: binding technical and data-flow specifications.
- `docs/development/`: quantitative acceptance standards and evidence requirements.
- `test/`: automated contract and security tests.
- `cmd/poc/`: optional manual PoC command; it is never loaded by FileBay.
- Root Go files in this directory: standalone adapter library for the nested Go module.

## Constraints

- This directory is a nested standalone Go module. The parent FileBay module must not import it.
- Do not modify FileBay routes, models, migrations, templates, configuration defaults, or build targets.
- Network access is disabled by default and requires an explicit PoC opt-in.
- Accept only client-masked bytes with a valid manifest and matching SHA-256 hash.
- Never accept, transmit, log, or persist a local absolute source path or unmasked original bytes.
- Tests must use `httptest` or an equivalent local fake; automated tests must not call a real RAGFlow service.
- Do not add third-party dependencies or commit secrets, API keys, generated data, logs, or real client files.
- Follow the contracts in `spec/` and the gates in `docs/development/acceptance-standard.md`.
