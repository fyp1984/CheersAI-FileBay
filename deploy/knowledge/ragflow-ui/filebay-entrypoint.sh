#!/usr/bin/env bash
set -euo pipefail

# Keep RAGFlow itself unmodified. The official image serves a prebuilt SPA;
# this deployment-only wrapper adds a small FileBay navigation shell at start.
index_file=/ragflow/web/dist/index.html
if [[ -f "${index_file}" ]] && ! grep -q 'filebay-shell.js' "${index_file}"; then
  sed -i 's#</head>#<link rel="stylesheet" href="/ragflow/filebay-shell.css"><script src="/ragflow/filebay-shell.js"></script></head>#' "${index_file}"
fi

exec /ragflow/entrypoint.sh "$@"
