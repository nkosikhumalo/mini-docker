#!/usr/bin/env bash
set -euo pipefail

repo=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
api_port=${MINIDOCK_API_PORT:-8080}
web_port=${MINIDOCK_WEB_PORT:-4173}
container_port=${MINIDOCK_CONTAINER_PORT:-8080}
image_name=${MINIDOCK_FRONTEND_IMAGE:-frontend-app}
socket_path=${MINIDOCK_SOCKET:-/tmp/minidock-run-$(id -u).sock}
data_root=${MINIDOCK_DATA_DIR:-$HOME/.local/share/minidock}
containers_dir=${MINIDOCK_CONTAINERS_DIR:-$data_root/containers}
images_dir=${MINIDOCK_IMAGES_DIR:-$data_root/images}
state_file=${MINIDOCK_STATE_FILE:-$data_root/state.json}
maven_cache=${MINIDOCK_MAVEN_CACHE:-/tmp/minidock-maven-cache}
runtime_log=$(mktemp /tmp/minidock-run-go.XXXXXX.log)
java_log=$(mktemp /tmp/minidock-run-java.XXXXXX.log)
proxy_log=$(mktemp /tmp/minidock-run-proxy.XXXXXX.log)
go_pid=
java_pid=
proxy_pid=
container_id=

cleanup() {
  rc=$?
  trap - EXIT INT TERM
  if [[ -n $container_id ]]; then
    MINIDOCK_API_URL="http://127.0.0.1:$api_port" "$repo/bin/minidock" stop "$container_id" >/dev/null 2>&1 || true
  fi
  [[ -z $proxy_pid ]] || { kill "$proxy_pid" 2>/dev/null || true; wait "$proxy_pid" 2>/dev/null || true; }
  [[ -z $java_pid ]] || { kill "$java_pid" 2>/dev/null || true; wait "$java_pid" 2>/dev/null || true; }
  if [[ -n $go_pid ]]; then
    sudo -n pkill -TERM -P "$go_pid" 2>/dev/null || true
    kill "$go_pid" 2>/dev/null || true
    wait "$go_pid" 2>/dev/null || true
  fi
  if ((rc != 0)); then
    echo "MiniDock startup failed. Recent service output:" >&2
    tail -n 40 "$runtime_log" "$java_log" "$proxy_log" >&2 2>/dev/null || true
  fi
  rm -f "$runtime_log" "$java_log" "$proxy_log"
  exit "$rc"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

if [[ $(uname -s) != Linux ]]; then
  echo "MiniDock requires Linux." >&2
  exit 1
fi
if [[ ! -d $repo/frontend ]]; then
  echo "Expected frontend project at $repo/frontend." >&2
  exit 1
fi
for tool in node npm go java python3 curl make; do
  command -v "$tool" >/dev/null || { echo "Required command '$tool' was not found on PATH." >&2; exit 1; }
done
node_version=$(node --version)
if ! python3 -c 'import re,sys; m=re.fullmatch(r"v(\d+)\.(\d+)\.(\d+)", sys.argv[1]); sys.exit(0 if m and ((int(m[1]) == 20 and (int(m[2]),int(m[3])) >= (19,0)) or (int(m[1]) == 22 and (int(m[2]),int(m[3])) >= (12,0)) or int(m[1]) > 22) else 1)' "$node_version"; then
  echo "Vite 8 requires Node.js 20.19+ or 22.12+; found $node_version." >&2
  exit 1
fi
if ! command -v sudo >/dev/null || ! sudo -v; then
  echo "sudo access is required to start MiniDock's network-enabled runtime." >&2
  exit 1
fi

export MINIDOCK_SOCKET=$socket_path
export MINIDOCK_SOCKET_GID=$(id -g)
export MINIDOCK_CONTAINERS_DIR=$containers_dir
export MINIDOCK_IMAGES_DIR=$images_dir
export MINIDOCK_STATE_FILE=$state_file
export MINIDOCK_API_URL="http://127.0.0.1:$api_port"
mkdir -p "$data_root" "$containers_dir" "$images_dir" "$(dirname "$state_file")"

echo "[1/4] Building MiniDock services..."
make -C "$repo" build MAVEN_ARGS="-Dmaven.repo.local=$maven_cache"

echo "[2/4] Starting Go runtime and Java API..."
sudo -E env MINIDOCK_SOCKET="$socket_path" MINIDOCK_SOCKET_GID="$(id -g)" \
  MINIDOCK_CONTAINERS_DIR="$containers_dir" "$repo/bin/minidockd" >"$runtime_log" 2>&1 &
go_pid=$!
for _ in $(seq 1 80); do [[ -S $socket_path ]] && break; sleep 0.25; done
[[ -S $socket_path ]] || { echo "Go runtime did not create $socket_path" >&2; exit 1; }
java -Duser.home="$HOME" -jar "$repo/java-control-plane/target/java-control-plane-0.1.0-SNAPSHOT.jar" \
  --server.port="$api_port" >"$java_log" 2>&1 &
java_pid=$!
ready=false
for _ in $(seq 1 120); do
  if curl --noproxy '*' -fsS "$MINIDOCK_API_URL/api/v1/images" >/dev/null 2>&1; then ready=true; break; fi
  kill -0 "$java_pid" 2>/dev/null || break
  sleep 0.25
done
[[ $ready == true ]] || { echo "Java API did not become ready at $MINIDOCK_API_URL" >&2; exit 1; }

echo "[3/4] Building and staging $image_name from frontend/..."
"$repo/bin/minidock" build -t "$image_name" "$repo/frontend"
run_response=$("$repo/bin/minidock" run -d "$image_name" /usr/bin/minidock-web \
  --root /usr/share/minidock/www --listen "0.0.0.0:$container_port")
container_id=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])' <<<"$run_response")
container_ip=$(python3 -c 'import json,sys; print(json.load(sys.stdin).get("networkIp", ""))' <<<"$run_response")
[[ -n $container_id && -n $container_ip ]] || { echo "MiniDock did not return a container ID and network IP" >&2; exit 1; }

echo "[4/4] Opening localhost port $web_port..."
python3 "$repo/scripts/tcp_forward.py" --port "$web_port" --target "$container_ip" \
  --target-port "$container_port" >"$proxy_log" 2>&1 &
proxy_pid=$!
for _ in $(seq 1 40); do
  if curl --noproxy '*' -fsS "http://127.0.0.1:$web_port/" >/dev/null 2>&1; then break; fi
  kill -0 "$proxy_pid" 2>/dev/null || { echo "Port forwarder failed to start" >&2; exit 1; }
  sleep 0.25
done
curl --noproxy '*' -fsS "http://127.0.0.1:$web_port/" >/dev/null

echo
echo "MiniDock is serving the frontend at: http://127.0.0.1:$web_port"
echo "Container port: $container_port | Container IP: $container_ip | Container ID: $container_id"
echo "Press Ctrl+C to stop the frontend container and both MiniDock services."
wait "$proxy_pid"
