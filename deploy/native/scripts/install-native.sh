#!/usr/bin/env bash
set -euo pipefail

install_deps=0
release_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
release_name="$(basename "$release_root")"
install_root="/opt/cheersai-filebay/releases/${release_name}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --install-deps)
      install_deps=1
      shift
      ;;
    --install-root)
      install_root="$2"
      shift 2
      ;;
    *)
      echo "Unknown argument: $1" >&2
      exit 2
      ;;
  esac
done

if [[ "$(id -u)" -ne 0 ]]; then
  echo "Please run as root or with sudo." >&2
  exit 1
fi

detect_pkg_manager() {
  if command -v apt-get >/dev/null 2>&1; then
    echo apt
  elif command -v dnf >/dev/null 2>&1; then
    echo dnf
  elif command -v yum >/dev/null 2>&1; then
    echo yum
  else
    echo none
  fi
}

install_dependencies() {
  local manager
  manager="$(detect_pkg_manager)"
  case "$manager" in
    apt)
      apt-get update
      DEBIAN_FRONTEND=noninteractive apt-get install -y nginx curl ca-certificates git bash python3 python3-venv python3-pip mysql-server redis-server unzip tar openssl rsync pkg-config libjemalloc2
      ;;
    dnf)
      dnf install -y nginx curl ca-certificates git bash python3 python3-virtualenv python3-pip mysql-server redis unzip tar openssl rsync pkgconf jemalloc
      ;;
    yum)
      yum install -y nginx curl ca-certificates git bash python3 python3-virtualenv python3-pip mysql-server redis unzip tar openssl rsync pkgconfig jemalloc
      ;;
    *)
      echo "No supported package manager found. Install nginx, python3, mysql, redis/valkey, elasticsearch and minio manually." >&2
      ;;
  esac
}

ensure_user() {
  local user="$1"
  local home="$2"
  if ! id "$user" >/dev/null 2>&1; then
    useradd --system --home-dir "$home" --create-home --shell /usr/sbin/nologin "$user"
  fi
}

write_secret() {
  local file="$1"
  local owner="$2"
  if [[ ! -f "$file" ]]; then
    openssl rand -hex 32 > "$file"
  fi
  chown "$owner" "$file"
  chmod 0400 "$file"
}

escape_sed() {
  printf '%s' "$1" | sed -e 's/[\/&]/\\&/g'
}

render_filebay_config() {
  local secret
  secret="$(cat /etc/cheersai-filebay/secrets/internal-token)"
  sed "s/__FILEBAY_SECRET_KEY__/$(escape_sed "$secret")/g" \
    "$release_root/deploy/native/templates/filebay-app.ini.template" \
    > /etc/cheersai-filebay/app.ini
  chown filebay:filebay /etc/cheersai-filebay/app.ini
  chmod 0640 /etc/cheersai-filebay/app.ini
}

render_ragflow_config() {
  local mysql redis minio elastic
  mysql="$(cat /etc/cheersai-ragflow/secrets/mysql-password)"
  redis="$(cat /etc/cheersai-ragflow/secrets/redis-password)"
  minio="$(cat /etc/cheersai-ragflow/secrets/minio-password)"
  elastic="$(cat /etc/cheersai-ragflow/secrets/elastic-password)"
  sed \
    -e "s/\${RAGFLOW_MYSQL_PASSWORD}/$(escape_sed "$mysql")/g" \
    -e "s/\${RAGFLOW_REDIS_PASSWORD}/$(escape_sed "$redis")/g" \
    -e "s/\${RAGFLOW_MINIO_PASSWORD}/$(escape_sed "$minio")/g" \
    -e "s/\${RAGFLOW_ELASTIC_PASSWORD}/$(escape_sed "$elastic")/g" \
    "$release_root/deploy/native/templates/ragflow-service_conf.yaml.template" \
    > /etc/cheersai-ragflow/service_conf.yaml
  chown ragflow:ragflow /etc/cheersai-ragflow/service_conf.yaml
  chmod 0640 /etc/cheersai-ragflow/service_conf.yaml
}

install_ragflow_python_env() {
  if ! python3 - <<'PY'
import sys
raise SystemExit(0 if sys.version_info[:2] == (3, 13) else 1)
PY
  then
    echo "RAGFlow v0.26.4 requires Python 3.13.x. Install Python 3.13 before starting ragflow-api." >&2
    return 0
  fi

  python3 -m venv /var/lib/cheersai-ragflow/venv
  /var/lib/cheersai-ragflow/venv/bin/pip install --upgrade pip uv
  (
    cd /var/lib/cheersai-ragflow/app
    UV_PROJECT_ENVIRONMENT=/var/lib/cheersai-ragflow/venv /var/lib/cheersai-ragflow/venv/bin/uv sync --frozen --no-dev
  )
}

inject_ragflow_shell() {
  local index="/var/lib/cheersai-ragflow/web/dist/index.html"
  [[ -f "$index" ]] || return 0
  if ! grep -q 'filebay-shell.js' "$index"; then
    sed -i 's#</head>#<link rel="stylesheet" href="/ragflow/filebay-shell.css?v=20260727"><script src="/ragflow/filebay-shell.js?v=20260727"></script></head>#' "$index"
  fi
}

if [[ "$install_deps" -eq 1 ]]; then
  install_dependencies
fi

ensure_user filebay /var/lib/cheersai-filebay
ensure_user ragflow /var/lib/cheersai-ragflow

mkdir -p "$install_root"
mkdir -p /opt/cheersai-filebay
rsync -a --delete "$release_root/" "$install_root/"
ln -sfn "$install_root" /opt/cheersai-filebay/current

mkdir -p /etc/cheersai-filebay/secrets /etc/cheersai-ragflow/secrets
mkdir -p /var/lib/cheersai-filebay/{data,custom,log,tmp,git}
mkdir -p /var/lib/cheersai-ragflow/{conf,logs,data,web,venv}
mkdir -p /var/log/cheersai-filebay /var/log/cheersai-ragflow

write_secret /etc/cheersai-filebay/secrets/internal-token filebay:filebay
write_secret /etc/cheersai-ragflow/secrets/mysql-password ragflow:ragflow
write_secret /etc/cheersai-ragflow/secrets/redis-password ragflow:ragflow
write_secret /etc/cheersai-ragflow/secrets/minio-password ragflow:ragflow
write_secret /etc/cheersai-ragflow/secrets/elastic-password ragflow:ragflow

install -m 0640 -o ragflow -g ragflow "$release_root/deploy/native/templates/ragflow.env.template" /etc/cheersai-ragflow/ragflow.env

rsync -a --delete "$release_root/third_party/ragflow/" /var/lib/cheersai-ragflow/app/
if [[ -d "$release_root/third_party/ragflow/web/dist" ]]; then
  rsync -a --delete "$release_root/third_party/ragflow/web/dist/" /var/lib/cheersai-ragflow/web/dist/
fi
install -m 0644 "$release_root/deploy/knowledge/ragflow-ui/filebay-shell.css" /var/lib/cheersai-ragflow/web/dist/filebay-shell.css
install -m 0644 "$release_root/deploy/knowledge/ragflow-ui/filebay-shell.js" /var/lib/cheersai-ragflow/web/dist/filebay-shell.js
install -m 0755 "$release_root/deploy/native/scripts/ragflow-native-run.sh" /usr/local/bin/ragflow-native-run

render_filebay_config
render_ragflow_config
inject_ragflow_shell
install_ragflow_python_env

install -m 0644 "$release_root/deploy/native/systemd/filebay.service" /etc/systemd/system/filebay.service
install -m 0644 "$release_root/deploy/native/systemd/ragflow-api.service" /etc/systemd/system/ragflow-api.service
install -m 0644 "$release_root/deploy/native/systemd/ragflow-worker.service" /etc/systemd/system/ragflow-worker.service
install -m 0644 "$release_root/deploy/native/nginx/cheersai-filebay-native.conf" /etc/nginx/conf.d/cheersai-filebay-native.conf

chown -R filebay:filebay /var/lib/cheersai-filebay /var/log/cheersai-filebay
chown -R ragflow:ragflow /var/lib/cheersai-ragflow /var/log/cheersai-ragflow /etc/cheersai-ragflow
chmod 0750 /etc/cheersai-filebay/secrets /etc/cheersai-ragflow/secrets

systemctl daemon-reload
nginx -t

cat <<EOF
Native files installed.

Next steps:
1. Install and bind Elasticsearch and MinIO to loopback/private addresses.
2. Review /etc/cheersai-ragflow/service_conf.yaml and /etc/cheersai-ragflow/ragflow.env.
3. Start services:
   systemctl start filebay ragflow-api ragflow-worker nginx
4. Verify:
   curl -I http://127.0.0.1:13080/knowledge
   curl -I http://127.0.0.1:13080/ragflow/
EOF
