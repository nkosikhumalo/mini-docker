#!/usr/bin/env bash
set -euo pipefail

repo=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
api_port=${MINIDOCK_API_PORT:-8080}
socket_path=${MINIDOCK_SOCKET:-/tmp/minidock-runtime.sock}
data_root=${MINIDOCK_DATA_DIR:-$HOME/.local/share/minidock}
images_dir=${MINIDOCK_IMAGES_DIR:-$data_root/images}
containers_dir=${MINIDOCK_CONTAINERS_DIR:-$data_root/containers}
state_file=${MINIDOCK_STATE_FILE:-$data_root/state.json}
maven_cache=${MINIDOCK_MAVEN_CACHE:-/tmp/minidock-maven-cache}
api_url="http://127.0.0.1:$api_port"
go_log=$(mktemp /tmp/minidock-go.XXXXXX.log)
java_log=$(mktemp /tmp/minidock-java.XXXXXX.log)
go_sudo_pid=
java_pid=

cleanup() {
  rc=$?
  trap - EXIT INT TERM
  [[ -z $java_pid ]] || { kill "$java_pid" 2>/dev/null || true; wait "$java_pid" 2>/dev/null || true; }
  if [[ -n $go_sudo_pid ]]; then
    sudo -n pkill -TERM -P "$go_sudo_pid" 2>/dev/null || true
    kill "$go_sudo_pid" 2>/dev/null || true
    wait "$go_sudo_pid" 2>/dev/null || true
  fi
  if ((rc != 0)); then
    echo "MiniDock stopped unexpectedly. Recent service output:" >&2
    tail -n 30 "$go_log" "$java_log" >&2 2>/dev/null || true
  fi
  rm -f "$go_log" "$java_log"
  exit "$rc"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

if [[ $(uname -s) != Linux ]]; then
  echo "MiniDock requires Linux." >&2
  exit 1
fi
for tool in go java curl make sudo; do
  command -v "$tool" >/dev/null || { echo "Required command '$tool' was not found on PATH." >&2; exit 1; }
done
if [[ -S $socket_path ]]; then
  echo "Runtime socket already exists at $socket_path; stop the other daemon or remove the stale socket first." >&2
  exit 1
fi
if curl --noproxy '*' --max-time 1 -fsS "$api_url/api/v1/images" >/dev/null 2>&1; then
  echo "A service is already responding at $api_url; choose another MINIDOCK_API_PORT." >&2
  exit 1
fi
sudo -v

export MINIDOCK_SOCKET=$socket_path
export MINIDOCK_API_URL=$api_url
export MINIDOCK_IMAGES_DIR=$images_dir
export MINIDOCK_CONTAINERS_DIR=$containers_dir
export MINIDOCK_STATE_FILE=$state_file
mkdir -p "$images_dir" "$containers_dir" "$(dirname "$state_file")"

echo "[1/2] Building MiniDock..."
make -C "$repo" build MAVEN_ARGS="-Dmaven.repo.local=$maven_cache"

echo "[2/2] Starting MiniDock services..."
sudo -E env MINIDOCK_SOCKET="$socket_path" MINIDOCK_SOCKET_GID="$(id -g)" \
  MINIDOCK_HOST_UID="$(id -u)" MINIDOCK_HOST_GID="$(id -g)" \
  MINIDOCK_CONTAINERS_DIR="$containers_dir" \
  MINIDOCK_CGROUP_ROOT="${MINIDOCK_CGROUP_ROOT:-}" \
  "$repo/bin/minidockd" >"$go_log" 2>&1 &
go_sudo_pid=$!
for _ in $(seq 1 80); do [[ -S $socket_path ]] && break; sleep 0.25; done
[[ -S $socket_path ]] || { echo "Go runtime did not create $socket_path" >&2; exit 1; }

(cd "$repo/java-control-plane" && ./mvnw -Dmaven.repo.local="$maven_cache" \
  spring-boot:run "-Dspring-boot.run.arguments=--server.port=$api_port") >"$java_log" 2>&1 &
java_pid=$!
ready=false
for _ in $(seq 1 120); do
  if curl --noproxy '*' -fsS "$api_url/api/v1/images" >/dev/null 2>&1; then ready=true; break; fi
  kill -0 "$java_pid" 2>/dev/null || break
  sleep 0.25
done
[[ $ready == true ]] || { echo "Java API did not become ready at $api_url" >&2; exit 1; }

echo
echo "MiniDock services are ready at $api_url."
if [[ -t 0 && -t 1 ]]; then
  echo "Opening the MiniDock dashboard; closing it will stop these services."
  "$repo/bin/minidock-ui" app "$api_url"
  exit 0
fi
echo "No interactive terminal detected; services are running without the dashboard."

while kill -0 "$java_pid" 2>/dev/null && kill -0 "$go_sudo_pid" 2>/dev/null; do
  sleep 1
done
echo "A MiniDock service exited; shutting down the remaining service." >&2
exit 1
