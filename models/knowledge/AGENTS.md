# Knowledge Domain Guardrails

- This directory is the authoritative FileBay knowledge-governance model. It must not import RAGFlow or HTTP packages.
- Keep revision, approval, publication, outbox, index-job, and audit writes transactional where specified by the technical spec.
- Revisions and audit events are append-only. Do not add update helpers for immutable content.
- Explicit deny, revocation generation, and publication generation checks fail closed.
- Never store file bytes, RAGFlow API keys, raw local paths, or unmasked content in model records or errors.
- New Go files require a 2026 copyright header and tests must use existing Gitea database test helpers.
