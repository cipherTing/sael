#!/bin/sh
set -eu

check_listener() {
  addr=$1
  host=${addr%:*}
  port=${addr##*:}
  case "$host" in
    ''|'0.0.0.0'|'[::]') host=127.0.0.1 ;;
  esac
  wget -qO- "http://$host:$port/healthz" >/dev/null
}

check_listener "${ADMIN_LISTEN_ADDR:-:8080}"
check_listener "${INGRESS_LISTEN_ADDR:-:8081}"
