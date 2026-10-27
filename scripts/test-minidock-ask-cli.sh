#!/usr/bin/env bash
set -euo pipefail
export PYTHONDONTWRITEBYTECODE=1

repo=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT

cat > "$scratch/curl" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
payload=
url=
while (($#)); do
  case "$1" in
    -d) payload=$2; shift 2 ;;
    *) url=$1; shift ;;
  esac
done
if [[ $url == */api/v1/images ]]; then
  printf '["alpine-local"]'
elif [[ $url == */api/v1/containers ]]; then
  printf '%s' "$payload" > "$MINIDOCK_TEST_CAPTURE"
  printf '{"id":"mock-container","status":"RUNNING"}'
else
  echo "unexpected test URL" >&2
  exit 1
fi
SH
chmod +x "$scratch/curl"

cat > "$scratch/sitecustomize.py" <<'PY'
import json
import urllib.request

class FakeResponse:
    def __init__(self, payload):
        self.payload = json.dumps(payload).encode()
    def __enter__(self):
        return self
    def __exit__(self, *_):
        return False
    def read(self, _limit):
        return self.payload

def mocked_open(request, timeout=0):
    body = json.loads(request.data)
    user = json.loads(body["messages"][1]["content"])
    spec = {
        "image": user["staged_images"][0],
        "command": ["/bin/sh", "-c", "echo from-ai"],
        "memory_limit": "64M",
        "cpu_limit": None,
        "network": False,
    }
    return FakeResponse({"choices": [{"message": {"content": json.dumps(spec)}}]})

urllib.request.urlopen = mocked_open
PY

printf 'yes\n' | env -u OPENROUTER_API_KEY \
  OPENROUTER_API_KEY=dummy-local-test-key \
  PYTHONPATH="$scratch" \
  MINIDOCK_TEST_CAPTURE="$scratch/request.json" \
  MINIDOCK_API_URL=http://minidock-test \
  PATH="$scratch:$PATH" \
  "$repo/bin/minidock" ask 'run a small shell with 64 MB memory'

python3 - "$scratch/request.json" <<'PY'
import json
import sys
with open(sys.argv[1], encoding="utf-8") as stream:
    request = json.load(stream)
assert request == {
    "image": "alpine-local",
    "command": ["/bin/sh", "-c", "echo from-ai"],
    "memory_limit": "64M",
    "cpu_limit": None,
    "network": False,
    "attach": False,
    "tty": False,
}, request
print("mocked ask-to-container API flow passed")
PY

set +e
missing_key=$(env -u OPENROUTER_API_KEY -u OPENROUTER_MODEL \
  PYTHONDONTWRITEBYTECODE=1 MINIDOCK_API_URL=http://minidock-test \
  PATH="$scratch:$PATH" "$repo/bin/minidock" ask 'run a local image' 2>&1)
missing_key_status=$?
set -e
[[ $missing_key_status -eq 1 ]]
[[ $missing_key == *'OPENROUTER_API_KEY is missing'* ]]
echo 'missing-key diagnostic passed'
