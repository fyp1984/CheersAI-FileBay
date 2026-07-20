#!/bin/sh
# Local trial-only secret bridge. Credentials remain in the ignored runtime
# directory and are never copied into the image, repository, or app.ini.
set -eu

binding_dir=/run/filebay-ragflow

if [ -s "$binding_dir/api-key" ]; then
	export GITEA__knowledge__RAGFLOW_API_KEY="$(cat "$binding_dir/api-key")"
fi

if [ -s "$binding_dir/dataset-id" ]; then
	export GITEA__knowledge__RAGFLOW_DATASET_ID="$(cat "$binding_dir/dataset-id")"
fi

exec /usr/bin/dumb-init -- /usr/local/bin/docker-entrypoint.sh "$@"
