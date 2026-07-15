# Knowledge RAGFlow PoC Acceptance Standard

## 1. Isolation

- `git diff main...HEAD --name-only` contains only paths below `contrib/knowledge-ragflow-poc/`.
- The parent `go.mod`, FileBay routes, models, migrations, templates, configuration, Docker files, and build targets remain unchanged.
- The PoC is a nested module and is not imported by the parent module.

## 2. Privacy and Network Safety

- With network disabled, 100% of adapter operations return `ErrNetworkDisabled` before the transport receives a request.
- Client construction rejects plain HTTP for every non-loopback host; remote endpoints require HTTPS.
- Invalid or unmasked artifacts cause zero HTTP requests.
- Tests prove that a Windows absolute path, POSIX absolute path, UNC path, traversal-like file name, and mismatched SHA-256 are rejected.
- Captured HTTP bodies contain the masked fixture and approved metadata, but contain zero occurrences of the sentinel local source path.
- Automated tests use only loopback `httptest.Server` endpoints.
- No API key, client file, fixture containing personal data, or generated log is committed.

## 3. Contract Behavior

- Successful publish performs exactly three ordered calls: upload, metadata update, parse.
- Upload response document ID is used by both metadata and parse requests.
- Metadata includes masked attestation, masked SHA-256, mask policy version, publication ID, and generation.
- Retrieval filters on both publication ID and generation and caps `page_size` to 100.
- Delete always sends explicit document IDs and never sends `delete_all=true`.
- A nonzero RAGFlow code fails even when HTTP status is 2xx.
- Metadata or parse failure triggers exactly one best-effort cleanup delete.
- Response bodies larger than 1 MiB fail closed.

## 4. Resource and Error Boundaries

- Empty artifacts and artifacts larger than 32 MiB are rejected without network access.
- Default HTTP timeout is no more than 10 seconds.
- Errors do not contain bearer tokens or artifact bytes.
- Unit and contract tests complete within 10 seconds on the development machine.

## 5. Required Evidence

Run from `contrib/knowledge-ragflow-poc`:

```powershell
$goFiles = Get-ChildItem -Recurse -Filter *.go | ForEach-Object { $_.FullName }
C:\Go\bin\gofmt.exe -w $goFiles
C:\Go\bin\go.exe test ./... -count=1
C:\Go\bin\go.exe test ./... -race -count=1
C:\Go\bin\go.exe vet ./...
```

Run from the repository root:

```powershell
git diff --check
git diff main...HEAD --name-only
```

Acceptance requires all commands to pass, no unexpected file outside the PoC subtree, and an independent review confirming that tests do not contact a real RAGFlow service.
