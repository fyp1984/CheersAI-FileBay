# Knowledge RAGFlow PoC Technical Specification

## 1. Scope

This PoC validates the smallest safe FileBay-to-RAGFlow integration loop without registering code in the FileBay application:

1. Validate a client-masked artifact and its manifest.
2. Upload the masked bytes to an existing RAGFlow dataset.
3. Attach FileBay publication metadata to the RAGFlow document.
4. Start RAGFlow's built-in chunking pipeline.
5. Retrieve chunks with an explicit metadata constraint.
6. Delete only the document created by the PoC.

The PoC does not implement FileBay database models, routes, migrations, UI, authentication, background jobs, production retries, or a real masking algorithm.

## 2. Isolation Mechanism

The PoC lives in `contrib/knowledge-ragflow-poc` as a nested Go module. No parent module package imports it, and no parent build target executes it. The optional CLI exits before creating an HTTP client unless `CHEERSAI_POC_ALLOW_NETWORK=1` is explicitly present.

All automated tests use a loopback `httptest.Server`. A real RAGFlow URL and API key are manual-only inputs and must never be committed.

Remote RAGFlow endpoints must use HTTPS. Plain HTTP is accepted only when the URL host is exactly `localhost` or a loopback IP address, so local contract tests remain possible without exposing bearer credentials or masked artifacts over an unencrypted remote connection.

## 3. Artifact Contract

`MaskedArtifact` contains only:

- masked file bytes;
- a safe base file name;
- an opaque source ID that is not a file path;
- SHA-256 of the masked bytes;
- mask policy version;
- FileBay publication ID;
- positive publication generation;
- `Masked=true` attestation.

Validation fails before any request when:

- `Masked` is false;
- data is empty or larger than 32 MiB;
- file name is empty, contains a directory, or is `.`/`..`;
- source ID is empty, absolute, drive-qualified, UNC-like, or contains `/` or `\`;
- SHA-256 is not 64 lowercase hexadecimal characters or does not match the bytes;
- mask policy version or publication ID is empty;
- generation is less than 1.

The PoC does not prove that masking is semantically sufficient. It enforces the transport contract and keeps masking client-side.

## 4. RAGFlow Contract

The contract was checked against the official RAGFlow HTTP API reference on 2026-07-15. Production work must pin a stable RAGFlow version and image digest before relying on it.

| Operation | Method and path |
| --- | --- |
| Upload | `POST /api/v1/datasets/{dataset_id}/documents` |
| Set metadata | `PUT /api/v1/datasets/{dataset_id}/documents/{document_id}` |
| Parse | `POST /api/v1/datasets/{dataset_id}/chunks` |
| Retrieve | `POST /api/v1/retrieval` |
| Delete | `DELETE /api/v1/datasets/{dataset_id}/documents` |

All requests use `Authorization: Bearer <API_KEY>`. A response succeeds only when HTTP status is 2xx and the RAGFlow JSON `code` is `0`.

The adapter must not automatically retry mutating requests. If metadata or parse fails after upload, it performs one best-effort delete for the created document and returns the original error plus cleanup outcome.

## 5. Retrieval Contract

Retrieval requires a non-empty question and publication ID. The request contains:

- the configured dataset ID;
- the question;
- bounded `page_size`;
- a `metadata_condition` that matches both `filebay_publication_id` and `filebay_publication_generation`.

The PoC returns RAGFlow chunk IDs, document IDs, content, and similarity values. It does not send chunks to an LLM and does not treat RAGFlow results as authorized FileBay citations.

## 6. Error and Exit Behavior

- Network-disabled calls return `ErrNetworkDisabled` before transport execution.
- Non-loopback plain HTTP base URLs are rejected during client construction, before file reading or transport execution.
- Invalid artifacts return `ErrInvalidArtifact` before transport execution.
- Non-2xx responses and nonzero RAGFlow codes include operation context but never include the API key or artifact bytes.
- Response bodies are size-limited to 1 MiB.
- Each request uses the caller context and a client timeout of at most 10 seconds by default.
- Manual CLI output includes only opaque IDs and statuses.
