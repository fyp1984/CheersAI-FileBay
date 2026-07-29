#!/usr/bin/env bash
set -euo pipefail

service="${1:-api}"
app_dir="/var/lib/cheersai-ragflow/app"
venv="/var/lib/cheersai-ragflow/venv"

set -a
source /etc/cheersai-ragflow/ragflow.env
set +a

export PYTHONPATH="${PYTHONPATH:-$app_dir}"
export NLTK_DATA="${NLTK_DATA:-$app_dir/nltk_data}"
export LD_LIBRARY_PATH="${LD_LIBRARY_PATH:-/usr/lib/x86_64-linux-gnu/}"

cd "$app_dir"
mkdir -p conf
cp /etc/cheersai-ragflow/service_conf.yaml conf/service_conf.yaml

ensure_db() {
  "$venv/bin/python" -c "from api.db.db_models import init_database_tables as init_web_db; init_web_db()"
  "$venv/bin/python" tools/scripts/mysql_migration.py \
    --stages tenant_model_provider,tenant_model_instance,tenant_model,model_id_config \
    --config conf/service_conf.yaml \
    --execute \
    --database-version "v0.26.1" \
    --mark-database-version-on-success
}

case "$service" in
  api)
    ensure_db
    exec "$venv/bin/python" api/ragflow_server.py
    ;;
  worker)
    if command -v pkg-config >/dev/null 2>&1 && pkg-config --exists jemalloc; then
      export LD_PRELOAD="$(pkg-config --variable=libdir jemalloc)/libjemalloc.so"
    fi
    exec "$venv/bin/python" rag/svr/task_executor.py -i native_0
    ;;
  *)
    echo "Usage: ragflow-native-run [api|worker]" >&2
    exit 2
    ;;
esac
