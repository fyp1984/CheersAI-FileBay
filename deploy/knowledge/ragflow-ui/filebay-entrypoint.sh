#!/usr/bin/env bash
set -euo pipefail

# Keep RAGFlow itself unmodified. The official image serves a prebuilt SPA;
# this deployment-only wrapper adds a small FileBay navigation shell at start.
index_file=/ragflow/web/dist/index.html
if [[ -f "${index_file}" ]]; then
	if ! grep -q 'filebay-shell.js' "${index_file}"; then
		sed -i 's#</head>#<link rel="stylesheet" href="/ragflow/filebay-shell.css?v=20260721"><script src="/ragflow/filebay-shell.js?v=20260721"></script></head>#' "${index_file}"
	else
		# The SPA bundle is kept in a Docker volume. Refresh only the deployment
		# adapter URLs when that volume is reused across a service recreation.
		sed -i 's#filebay-shell\.css[^" ]*#filebay-shell.css?v=20260721#g; s#filebay-shell\.js[^" ]*#filebay-shell.js?v=20260721#g' "${index_file}"
	fi
fi

exec /ragflow/entrypoint.sh "$@"
