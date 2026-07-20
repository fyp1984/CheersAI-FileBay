# Knowledge RAGFlow PoC Data Flow

## Safe Path

```text
Local original file
  -> client-side masking (outside this PoC)
  -> Local Sandbox masked bytes + manifest
  -> artifact validation (no network)
  -> explicit network opt-in
  -> RAGFlow upload
  -> FileBay publication metadata update
  -> RAGFlow parse request
  -> document ID returned to the caller
```

## Retrieval Path

```text
question + publication ID + generation
  -> validation
  -> dataset-scoped retrieval request
  -> metadata filter for publication ID and generation
  -> untrusted RAGFlow chunks returned to the caller
```

The production Knowledge Gateway will later apply FileBay authorization and content-integrity verification. This PoC intentionally stops before model invocation.

## Failure Path

```text
upload succeeds
  -> metadata or parse fails
  -> one best-effort delete of the uploaded document
  -> return original failure and cleanup result
```

No automated test may contact a non-loopback service. No path may contain or transmit a local absolute source path.
