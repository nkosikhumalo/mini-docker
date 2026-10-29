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
build_pid=
build_log=$(mktemp /tmp/minidock-build.XXXXXX.log)
services_started=false

if [[ -t 1 && -z ${NO_COLOR:-} && ${TERM:-dumb} != dumb ]]; then
  purple=$'\033[1;35m'
  cyan=$'\033[1;36m'
  green=$'\033[1;32m'
  yellow=$'\033[1;33m'
  red=$'\033[1;31m'
  muted=$'\033[0;90m'
  reset=$'\033[0m'
else
  purple= cyan= green= yellow= red= muted= reset=
fi

show_banner() {
  printf '\n%s╭──────────────────────────────────────────────╮%s\n' "$purple" "$reset"
  printf '%s│%s  %s◆ MINIDOCK%s  %sLightweight Linux containers%s       %s│%s\n' "$purple" "$reset" "$purple" "$reset" "$muted" "$reset" "$purple" "$reset"
  printf '%s╰──────────────────────────────────────────────╯%s\n\n' "$purple" "$reset"
}

show_failure_log() {
  if [[ -s $build_log ]]; then
    printf '\n%sBuild output:%s\n' "$muted" "$reset" >&2
    cat "$build_log" >&2
  fi
}

cleanup() {
  rc=$?
  trap - EXIT INT TERM
  [[ -z $java_pid ]] || { kill "$java_pid" 2>/dev/null || true; wait "$java_pid" 2>/dev/null || true; }
  if [[ -n $go_sudo_pid ]]; then
    sudo -n pkill -TERM -P "$go_sudo_pid" 2>/dev/null || true
    kill "$go_sudo_pid" 2>/dev/null || true
    wait "$go_sudo_pid" 2>/dev/null || true
  fi
  if ((rc != 0 && rc != 130 && rc != 143)) && [[ $services_started == true ]]; then
    echo "MiniDock stopped unexpectedly. Recent service output:" >&2
    tail -n 30 "$go_log" "$java_log" >&2 2>/dev/null || true
  fi
  if [[ -n $build_pid ]]; then
    kill "$build_pid" 2>/dev/null || true
    wait "$build_pid" 2>/dev/null || true
  fi
  rm -f "$go_log" "$java_log" "$build_log"
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

show_banner
printf '%s  1/2%s  Building MiniDock\n' "$cyan" "$reset"
if [[ -t 1 ]]; then
  make -s -C "$repo" build MAVEN_ARGS="-Dmaven.repo.local=$maven_cache" >"$build_log" 2>&1 &
  build_pid=$!
  spinner='|/-'
  spin_index=0
  while kill -0 "$build_pid" 2>/dev/null; do
    printf '\r%s  %s%s  Compiling Go runtime and Java control plane…%s' \
      "$cyan" "${spinner:spin_index:1}" "$cyan" "$reset"
    spin_index=$(((spin_index + 1) % 3))
    sleep 0.12
  done
  if wait "$build_pid"; then
    build_pid=
    printf '\r%s  ✓%s  Build complete.                                      \n' "$green" "$reset"
  else
    build_status=$?
    build_pid=
    printf '\r%s  ✗%s  Build failed.                                        \n' "$red" "$reset" >&2
    show_failure_log
    exit "$build_status"
  fi
else
  if make -s -C "$repo" build MAVEN_ARGS="-Dmaven.repo.local=$maven_cache" >"$build_log" 2>&1; then
    printf '%s  ✓%s  Build complete.\n' "$green" "$reset"
  else
    build_status=$?
    printf '%s  ✗%s  Build failed.\n' "$red" "$reset" >&2
    show_failure_log
    exit "$build_status"
  fi
fi

printf '\n%s  2/2%s  Starting MiniDock services\n' "$cyan" "$reset"
printf '%s  ·%s  Go runtime ' "$yellow" "$reset"
sudo -E env MINIDOCK_SOCKET="$socket_path" MINIDOCK_SOCKET_GID="$(id -g)" \
  MINIDOCK_HOST_UID="$(id -u)" MINIDOCK_HOST_GID="$(id -g)" \
  MINIDOCK_CONTAINERS_DIR="$containers_dir" \
  MINIDOCK_CGROUP_ROOT="${MINIDOCK_CGROUP_ROOT:-}" \
  "$repo/bin/minidockd" >"$go_log" 2>&1 &
go_sudo_pid=$!
services_started=true
for _ in $(seq 1 80); do [[ -S $socket_path ]] && break; sleep 0.25; done
[[ -S $socket_path ]] || { printf '%sfailed%s\n' "$red" "$reset"; echo "Go runtime did not create $socket_path" >&2; exit 1; }
printf '%sready%s\n' "$green" "$reset"

printf '%s  ·%s  Java control plane ' "$yellow" "$reset"
(cd "$repo/java-control-plane" && ./mvnw -Dmaven.repo.local="$maven_cache" \
  spring-boot:run "-Dspring-boot.run.arguments=--server.port=$api_port") >"$java_log" 2>&1 &
java_pid=$!
ready=false
for _ in $(seq 1 120); do
  if curl --noproxy '*' -fsS "$api_url/api/v1/images" >/dev/null 2>&1; then ready=true; break; fi
  kill -0 "$java_pid" 2>/dev/null || break
  sleep 0.25
done
[[ $ready == true ]] || { printf '%sfailed%s\n' "$red" "$reset"; echo "Java API did not become ready at $api_url" >&2; exit 1; }
printf '%sready%s\n' "$green" "$reset"

echo
printf '%s  ✓ MiniDock is ready%s\n' "$green" "$reset"
printf '  API     %s\n' "$api_url"
if [[ -t 0 && -t 1 ]]; then
  printf '  %sOpening dashboard… close it to stop MiniDock.%s\n\n' "$muted" "$reset"
  MINIDOCK_REPO="$repo" "$repo/bin/minidock-ui" app "$api_url"
  exit 0
fi
printf '  %sNo interactive terminal; services are running without the dashboard.%s\n' "$muted" "$reset"

while kill -0 "$java_pid" 2>/dev/null && kill -0 "$go_sudo_pid" 2>/dev/null; do
  sleep 1
done
echo "A MiniDock service exited; shutting down the remaining service." >&2
exit 1
