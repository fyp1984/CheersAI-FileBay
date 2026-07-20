# CheersAI-FileBay

Privacy-first, user-isolated, desensitized file vault with private cloud sync,
built on top of Gitea.

[繁體中文](./README.zh-tw.md) | [简体中文](./README.zh-cn.md)

## Background

In the Desktop product, LLM agents and knowledge-base features need controlled
access to user files. CheersAI-FileBay provides an automated mechanism to sync
desensitized local files to a private cloud space, so agents can securely use
them without accessing raw local data.

## Goals

- User private: per-user isolated storage and access control
- Cloud sync: automated device-to-cloud synchronization of desensitized files
- Security isolation: strict tenant isolation, auditable access, least privilege
- Server capabilities: private Git service plus APIs as the platform foundation

## What This Repo Is

- A productized fork of Gitea, adapted into CheersAI-FileBay
- A backend and web UI for private Git hosting and extended APIs for secure file management
- A single-repository enterprise knowledge-base delivery: FileBay is the
  governance and business UI, while the pinned RAGFlow source under
  `third_party/ragflow/` runs as an independent indexing and retrieval service

## Enterprise Knowledge Base Deployment

One checkout contains both FileBay and the pinned RAGFlow source. They remain
separate services at runtime: FileBay owns desensitized files, permissions,
versions, approval and audit; RAGFlow only processes published, desensitized
snapshots for parsing and retrieval. Follow the Chinese deployment guide at
[deploy/knowledge/README.md](deploy/knowledge/README.md). Local runtime
configuration and credentials stay outside version control.

## Build

From the root of the source tree, run:

    TAGS="bindata" make build

Or if SQLite support is required:

    TAGS="bindata sqlite sqlite_unlock_notify" make build

The `build` target is split into two sub-targets:

- `make backend` requires Go Stable, the required version is defined in [go.mod](/go.mod).
- `make frontend` requires Node.js LTS (or greater) and pnpm.

Internet connectivity is required to download Go and npm modules. When building
from source tarballs that include pre-built frontend files, the `frontend`
target will not be triggered, making it possible to build without Node.js.

## Run

After building, the default server binary name is currently `gitea` (upstream naming). Run it with:

    ./gitea web

## Security

- Deployment should be private-network-first, with TLS enabled end-to-end
- Avoid exposing administrative endpoints to the public Internet
- Define a vulnerability disclosure process in [SECURITY.md](SECURITY.md)

## Roadmap (High Level)

- Branding: replace UI strings, assets, and docs from upstream naming to CheersAI-FileBay
- APIs: add secure, auditable desensitized file sync and access endpoints
- Isolation: strengthen per-user storage boundaries and authorization model
- Compliance: add configurable audit retention, data minimization, and export/delete workflows

## License and Upstream

- This repository includes upstream work derived from Gitea and follows the MIT
  License in [LICENSE](LICENSE).
- Upstream project: Gitea (Git with a cup of tea).

## Trademarks

Gitea and Gogs are trademarks of their respective owners. CheersAI-FileBay is
not affiliated with or endorsed by upstream trademark owners.
