#!/usr/bin/env python3
"""Build supported frontend source projects into MiniDock rootfs archives."""

import json
import os
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
from pathlib import Path


def fail(message):
    print(f"minidock build: {message}", file=sys.stderr)
    raise SystemExit(1)


def run(command, cwd, env=None):
    print("+ " + " ".join(str(part) for part in command), flush=True)
    try:
        subprocess.run(command, cwd=cwd, env=env, check=True)
    except FileNotFoundError:
        fail(f"required command {command[0]!r} was not found on PATH")
    except subprocess.CalledProcessError as error:
        fail(f"command failed with exit code {error.returncode}: {command[0]}")


def node_version():
    binary = shutil.which("node")
    if binary is None:
        fail("Vite builds require Node.js 20.19+ or 22.12+; install a supported Node.js version")
    try:
        output = subprocess.check_output([binary, "--version"], text=True, stderr=subprocess.DEVNULL).strip()
    except subprocess.CalledProcessError:
        fail("could not read the installed Node.js version")
    match = re.fullmatch(r"v(\d+)\.(\d+)\.(\d+)", output)
    if match is None:
        fail(f"could not parse Node.js version {output!r}")
    major, minor, patch = map(int, match.groups())
    if not (major == 20 and (minor, patch) >= (19, 0) or major == 22 and (minor, patch) >= (12, 0) or major > 22):
        fail(f"installed Node.js is {output}; Vite 8 requires 20.19+ or 22.12+")


def reject_symlinks(directory):
    for path in directory.rglob("*"):
        if path.is_symlink():
            fail(f"built output contains a symlink, which this small image builder does not package: {path.relative_to(directory)}")


def build_vite(project, archive, repo, package_existing_dist=False):
    package_file = project / "package.json"
    if not package_file.is_file():
        fail(f"{project} is not a supported source project (package.json was not found)")
    try:
        package = json.loads(package_file.read_text(encoding="utf-8"))
    except (OSError, ValueError) as error:
        fail(f"could not read package.json: {error}")
    dependencies = {**package.get("dependencies", {}), **package.get("devDependencies", {})}
    if "vite" not in dependencies or not package.get("scripts", {}).get("build"):
        fail("source builds currently support Vite projects with a package.json build script")

    if not package_existing_dist:
        node_version()
        npm = shutil.which("npm")
        if npm is None:
            fail("npm was not found on PATH")
        build_env = os.environ.copy()
        build_env.pop("OPENROUTER_API_KEY", None)
        build_env.pop("OPENROUTER_MODEL", None)
        if not (project / "node_modules").is_dir():
            if (project / "package-lock.json").is_file():
                run([npm, "ci"], project, build_env)
            else:
                run([npm, "install"], project, build_env)
        run([npm, "run", "build"], project, build_env)

    dist = project / "dist"
    if not dist.is_dir() or not (dist / "index.html").is_file():
        fail(f"Vite build did not produce {dist}/index.html")
    reject_symlinks(dist)

    go = shutil.which("go")
    if go is None:
        fail("Go is required to build MiniDock's small static frontend server")
    with tempfile.TemporaryDirectory(prefix="minidock-source-build-") as temporary:
        work = Path(temporary)
        webserver = work / "minidock-web"
        env = os.environ.copy()
        env.update({"CGO_ENABLED": "0", "GOOS": "linux"})
        env.setdefault("GOCACHE", "/tmp/minidock-go-cache")
        run([go, "build", "-trimpath", "-ldflags=-s -w", "-o", str(webserver), "./cmd/minidock-web"], repo / "go-runtime", env)

        rootfs = work / "rootfs"
        server_dir = rootfs / "usr" / "bin"
        web_dir = rootfs / "usr" / "share" / "minidock" / "www"
        server_dir.mkdir(parents=True)
        web_dir.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(webserver, server_dir / "minidock-web")
        shutil.copytree(dist, web_dir)
        with tarfile.open(archive, "w:gz", format=tarfile.PAX_FORMAT) as bundle:
            bundle.add(rootfs, arcname=".")


def main():
    package_existing_dist = len(sys.argv) == 4 and sys.argv[3] == "--from-dist"
    if len(sys.argv) not in (3, 4) or (len(sys.argv) == 4 and not package_existing_dist):
        fail("usage: minidock_build.py PROJECT_DIR OUTPUT.tar.gz [--from-dist]")
    project = Path(sys.argv[1]).expanduser().resolve()
    archive = Path(sys.argv[2]).expanduser().resolve()
    repo = Path(__file__).resolve().parent.parent
    if not project.is_dir():
        fail(f"project directory does not exist: {project}")
    if not archive.parent.is_dir():
        fail(f"output directory does not exist: {archive.parent}")
    build_vite(project, archive, repo, package_existing_dist)
    print(f"MiniDock Vite image archive created: {archive}")


if __name__ == "__main__":
    main()
