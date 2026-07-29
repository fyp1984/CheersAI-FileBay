#!/usr/bin/env bash
set -euo pipefail

ts="$(date +%Y%m%d-%H%M%S)"
out="${1:-/var/tmp/cheersai-audit-${ts}}"
mkdir -p "$out"

run() {
  local name="$1"
  shift
  {
    echo "\$ $*"
    "$@" 2>&1 || true
  } > "${out}/${name}.txt"
}

run shell uname -a
run os sh -c 'cat /etc/os-release 2>/dev/null || lsb_release -a 2>/dev/null || true'
run cpu sh -c 'nproc; lscpu 2>/dev/null | sed -n "1,40p"'
run memory free -h
run disk df -hT
run ports ss -ltnp
run services systemctl list-units --type=service --state=running
run failed-services systemctl --failed
run nginx-version nginx -v
run nginx-config sh -c 'nginx -T 2>&1 | sed -n "1,260p"'
run process sh -c 'ps -eo pid,ppid,user,comm,args --sort=comm | grep -Ei "filebay|gitea|ragflow|nginx|mysql|redis|valkey|elastic|minio|python" | grep -v grep'
run app-dirs sh -c 'ls -la /opt /etc /var/lib /var/log 2>/dev/null | sed -n "1,240p"'
run filebay-version sh -c '/opt/cheersai-filebay/current/bin/filebay --version 2>/dev/null || /usr/local/bin/gitea --version 2>/dev/null || true'
run journals sh -c 'journalctl -u filebay -u ragflow-api -u ragflow-worker -u nginx -n 200 --no-pager 2>/dev/null || true'
run http-local sh -c 'curl -sS -I http://127.0.0.1:13080/knowledge 2>/dev/null; curl -sS -I http://127.0.0.1:13080/ragflow/ 2>/dev/null; curl -sS http://127.0.0.1:13080/api/healthz 2>/dev/null || true'

cat > "${out}/SUMMARY.txt" <<EOF
CheersAI FileBay + RAGFlow server audit
Time: ${ts}
Output: ${out}

This is a read-only audit. It does not stop services, overwrite files, or expose secrets.
EOF

echo "$out"
