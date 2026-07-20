#!/bin/sh
# Local trial-only secret bridge. Credentials stay in the ignored runtime
# directory and are read by FileBay directly through its read-only mount.
# Older trial versions passed the values via GITEA__ variables, causing the
# upstream entrypoint to persist them in app.ini. Remove those legacy entries
# before startup; this script must never read or export the credential values.
set -eu

if [ -f "${GITEA_APP_INI:-/etc/gitea/app.ini}" ]; then
	sed -i '/^[[:space:]]*RAGFLOW_API_KEY[[:space:]]*=/d; /^[[:space:]]*RAGFLOW_DATASET_ID[[:space:]]*=/d' "${GITEA_APP_INI:-/etc/gitea/app.ini}"
fi

exec /usr/bin/dumb-init -- /usr/local/bin/docker-entrypoint.sh "$@"
