#!/usr/bin/env python3
"""Build supported source projects into MiniDock Linux rootfs archives."""

import glob
import hashlib
import json
import os
import platform
import re
import shutil
import subprocess
import sys
import tarfile
import zipfile
import tempfile
import urllib.error
import urllib.request
from pathlib import PurePosixPath
from pathlib import Path
from minidock_project import angular_application, dotnet_project_info, maven_http_port, python_project_info


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


NODE_DIST = "https://nodejs.org/dist"


def read_node_version(binary):
    try:
        output = subprocess.check_output([binary, "--version"], text=True, stderr=subprocess.DEVNULL).strip()
    except (OSError, subprocess.CalledProcessError):
        return None
    match = re.fullmatch(r"v(\d+)\.(\d+)\.(\d+)", output)
    return tuple(map(int, match.groups())) if match else None


def version_satisfies(version, engine_range):
    """Evaluate the common npm semver ranges used in package engine fields."""
    def partial(value):
        match = re.fullmatch(r"v?(\d+|[xX*])(?:\.(\d+|[xX*]))?(?:\.(\d+|[xX*]))?", value)
        if not match:
            return None
        raw = match.groups()
        numbers = []
        wildcard_at = 3
        for index, item in enumerate(raw):
            if item is None or item in ("x", "X", "*"):
                wildcard_at = min(wildcard_at, index)
                numbers.append(0)
            elif wildcard_at < 3:
                return None
            else:
                numbers.append(int(item))
        return tuple(numbers), wildcard_at

    def upper_partial(nums, precision):
        if precision == 0:
            return None
        if precision == 1:
            return nums[0] + 1, 0, 0
        return nums[0], nums[1] + 1, 0

    for alternative in engine_range.strip().split("||"):
        alternative = alternative.strip()
        hyphen = re.fullmatch(r"(\S+)\s+-\s+(\S+)", alternative)
        if hyphen:
            right = partial(hyphen.group(2))
            if right is None:
                continue
            right_floor, right_precision = right
            right_upper = upper_partial(right_floor, right_precision)
            right_bound = f"<{'{}.{}.{}'.format(*right_upper)}" if right_upper else f"<={hyphen.group(2)}"
            parts = [f">={hyphen.group(1)}", right_bound]
        else:
            parts = alternative.split()
        valid = bool(parts)
        for part in parts:
            match = re.fullmatch(r"(>=|>|<=|<|=|\^|~)?(.*)", part)
            operator, value = match.groups()
            parsed = partial(value)
            if parsed is None:
                valid = False
                break
            floor, precision = parsed
            if precision == 0:
                if operator in (None, "=", "^"):
                    continue
                if operator == "~":
                    continue
                continue
            if operator in (None, "="):
                upper = upper_partial(floor, precision)
                if version < floor or upper is not None and version >= upper:
                    valid = False
            elif operator == ">=" and version < floor:
                valid = False
            elif operator == ">":
                boundary = upper_partial(floor, precision) if precision < 3 else None
                if boundary is not None and version < boundary or boundary is None and version <= floor:
                    valid = False
            elif operator == "<=":
                boundary = upper_partial(floor, precision) if precision < 3 else None
                if boundary is not None and version >= boundary or boundary is None and version > floor:
                    valid = False
            elif operator == "<" and version >= floor:
                valid = False
            elif operator == "^":
                upper = (floor[0] + 1, 0, 0) if floor[0] or precision == 1 else (0, floor[1] + 1, 0) if floor[1] or precision == 2 else (0, 0, floor[2] + 1)
                if version < floor or version >= upper:
                    valid = False
            elif operator == "~":
                upper = (floor[0] + 1, 0, 0) if precision == 1 else (floor[0], floor[1] + 1, 0)
                if version < floor or version >= upper:
                    valid = False
            if not valid:
                break
        if valid:
            return True
    return False


def node_engine_requirements(project, package, package_names):
    requirements = []
    lock = {}
    try:
        lock = json.loads((project / "package-lock.json").read_text(encoding="utf-8"))
    except (OSError, ValueError):
        pass
    locked_packages = lock.get("packages", {}) if isinstance(lock, dict) else {}
    for name in package_names:
        metadata = project / "node_modules" / name / "package.json"
        package_metadata = {}
        if metadata.is_file():
            try:
                package_metadata = json.loads(metadata.read_text(encoding="utf-8"))
            except (OSError, ValueError):
                package_metadata = {}
        if not package_metadata and isinstance(locked_packages, dict):
            package_metadata = locked_packages.get("node_modules/" + name, {})
        engines_data = package_metadata.get("engines") if isinstance(package_metadata, dict) else None
        engines = engines_data.get("node") if isinstance(engines_data, dict) else None
        if isinstance(engines, str) and engines:
            requirements.append(engines)

    project_engines = package.get("engines")
    project_engine = project_engines.get("node") if isinstance(project_engines, dict) else None
    if isinstance(project_engine, str) and project_engine:
        requirements.append(project_engine)
    for pin_file in (".nvmrc", ".node-version"):
        try:
            pinned = (project / pin_file).read_text(encoding="utf-8").strip().removeprefix("v")
        except OSError:
            continue
        if pinned and re.fullmatch(r"\d+(?:\.\d+){0,2}", pinned):
            precision = len(pinned.split("."))
            requirements.append(f"^{pinned}.0.0" if precision == 1 else f"~{pinned}.0" if precision == 2 else f"={pinned}")
    # If no dependency or app declares an engine, any published stable LTS
    # release is a reasonable build runtime. The locked dependency requirements
    # above remain the authority whenever they are available.
    return requirements or ["*"]


def node_cache_root():
    configured = os.environ.get("MINIDOCK_NODE_CACHE")
    if configured:
        return Path(configured).expanduser()
    cache_home = Path(os.environ.get("XDG_CACHE_HOME", Path.home() / ".cache")).expanduser()
    return cache_home / "minidock" / "node"


def node_candidates():
    configured = os.environ.get("MINIDOCK_NODE_BIN")
    if configured:
        yield Path(configured).expanduser()
    on_path = shutil.which("node")
    if on_path:
        yield Path(on_path)

    home = Path.home()
    patterns = (
        home / ".nvm/versions/node/*/bin/node",
        home / ".local/share/mise/installs/node/*/bin/node",
        home / ".asdf/installs/nodejs/*/bin/node",
        home / ".volta/tools/image/node/*/bin/node",
        Path("/tmp/node-v*/bin/node"),
        Path("/usr/local/lib/nodejs/node-*/bin/node"),
        Path("/opt/node*/bin/node"),
        node_cache_root() / "*/bin/node",
    )
    for pattern in patterns:
        yield from (Path(candidate) for candidate in sorted(glob.glob(str(pattern)), reverse=True))


def cached_compatible_node(requirements):
    seen = set()
    for candidate in node_candidates():
        resolved = str(candidate.resolve())
        if resolved in seen:
            continue
        seen.add(resolved)
        if not candidate.is_file() or not os.access(candidate, os.X_OK):
            continue
        version = read_node_version(str(candidate))
        if version and all(version_satisfies(version, requirement) for requirement in requirements):
            return str(candidate), version
    return None, None


def linux_node_architecture():
    architecture = platform.machine().lower()
    mapping = {"x86_64": "x64", "amd64": "x64", "aarch64": "arm64", "arm64": "arm64"}
    if architecture not in mapping:
        fail(f"MiniDock cannot bootstrap Node.js for Linux architecture {architecture!r}; install a compatible Node.js and set MINIDOCK_NODE_BIN")
    return mapping[architecture]


def download_node(requirements):
    cache_root = node_cache_root()
    try:
        cache_root.mkdir(parents=True, exist_ok=True)
        architecture = linux_node_architecture()
        print(f"No compatible local Node.js found; selecting a verified runtime for the project's Node.js requirements into {cache_root}", flush=True)
        with urllib.request.urlopen(f"{NODE_DIST}/index.json", timeout=30) as response:
            releases = json.load(response)
        releases = [release for release in releases
                    if re.fullmatch(r"v\d+\.\d+\.\d+", release.get("version", ""))
                    and f"linux-{architecture}" in release.get("files", [])]
        compatible = [release for release in releases
                      if all(version_satisfies(tuple(map(int, release["version"][1:].split("."))), requirement)
                             for requirement in requirements)]
        # Prefer the newest compatible LTS, then the newest compatible stable
        # release for projects whose declared range excludes every LTS line.
        compatible.sort(key=lambda release: tuple(map(int, release["version"][1:].split("."))), reverse=True)
        selected = next((release for release in compatible if release.get("lts")), None)
        if selected is None and compatible:
            selected = compatible[0]
        if selected is None:
            fail(f"no official Linux Node.js release satisfies engine requirements {requirements!r}; install a compatible Node.js and set MINIDOCK_NODE_BIN")
        version = selected["version"]
        archive_name = f"node-{version}-linux-{architecture}.tar.xz"
        release_url = f"{NODE_DIST}/{version}"
        print(f"Selected Node.js {version}; verifying its official SHA-256 before use", flush=True)
        with urllib.request.urlopen(f"{release_url}/SHASUMS256.txt", timeout=30) as response:
            checksums = response.read(1 << 20).decode("ascii")
        checksum = next((line.split()[0] for line in checksums.splitlines()
                         if len(line.split()) == 2 and line.split()[1].lstrip("*") == archive_name), None)
        if checksum is None or not re.fullmatch(r"[a-fA-F0-9]{64}", checksum):
            fail(f"official Node.js release did not provide a valid SHA-256 for {archive_name}")

        with tempfile.TemporaryDirectory(prefix="minidock-node-", dir=cache_root) as temporary:
            archive_path = Path(temporary) / archive_name
            digest = hashlib.sha256()
            with urllib.request.urlopen(f"{release_url}/{archive_name}", timeout=60) as response, archive_path.open("wb") as output:
                total = 0
                reported = 0
                while chunk := response.read(1 << 20):
                    total += len(chunk)
                    if total > 200 << 20:
                        fail("Node.js download exceeded the 200 MiB safety limit")
                    digest.update(chunk)
                    output.write(chunk)
                    if total >= reported + (10 << 20):
                        reported = total
                        print(f"Downloaded {total // (1 << 20)} MiB of the Node.js runtime", flush=True)
            if digest.hexdigest().lower() != checksum.lower():
                fail("downloaded Node.js archive failed its official SHA-256 check")

            archive_prefix = f"node-{version}-linux-{architecture}/"
            node_member = archive_prefix + "bin/node"
            with tarfile.open(archive_path, "r:xz") as bundle:
                try:
                    member = bundle.getmember(node_member)
                except KeyError:
                    fail("verified Node.js archive did not contain its expected node executable")
                if not member.isfile() or member.size > 256 << 20:
                    fail(f"verified Node.js archive contained an invalid or unexpectedly large node executable ({member.size // (1 << 20)} MiB)")
                source = bundle.extractfile(member)
                if source is None:
                    fail("could not read the node executable from the verified archive")
                extracted = Path(temporary) / "node"
                with source, extracted.open("wb") as output:
                    shutil.copyfileobj(source, output)
                npm_prefix = archive_prefix + "lib/node_modules/npm/"
                for entry in bundle.getmembers():
                    if not entry.name.startswith(npm_prefix):
                        continue
                    relative = PurePosixPath(entry.name[len(archive_prefix):])
                    if relative.is_absolute() or ".." in relative.parts or entry.issym() or entry.islnk():
                        fail("Node.js archive contains an unsafe npm package entry")
                    destination = Path(temporary) / "runtime" / Path(*relative.parts)
                    if entry.isdir():
                        destination.mkdir(parents=True, exist_ok=True)
                    elif entry.isfile():
                        if entry.size > 20 << 20:
                            fail("Node.js archive contains an unexpectedly large npm package file")
                        destination.parent.mkdir(parents=True, exist_ok=True)
                        content = bundle.extractfile(entry)
                        if content is None:
                            fail("could not read npm from the verified Node.js archive")
                        with content, destination.open("wb") as output:
                            shutil.copyfileobj(content, output)
            extracted.chmod(0o755)
            final_dir = cache_root / version / "bin"
            final_dir.mkdir(parents=True, exist_ok=True)
            final_node = final_dir / "node"
            os.replace(extracted, final_node)
            runtime_npm = Path(temporary) / "runtime" / "lib/node_modules/npm"
            if runtime_npm.is_dir():
                final_runtime = cache_root / version / "lib/node_modules/npm"
                if final_runtime.exists():
                    shutil.rmtree(final_runtime)
                final_runtime.parent.mkdir(parents=True, exist_ok=True)
                shutil.copytree(runtime_npm, final_runtime)
        return str(final_node), tuple(map(int, version[1:].split(".")))
    except (OSError, urllib.error.URLError, json.JSONDecodeError, tarfile.TarError) as error:
        fail(f"no compatible Node.js is installed and MiniDock could not download a verified project-compatible runtime: {error}. Install one of the project's supported Node.js versions or set MINIDOCK_NODE_BIN")


def compatible_node(project, package, package_names):
    requirements = node_engine_requirements(project, package, package_names)
    binary, version = cached_compatible_node(requirements)
    if binary is None:
        binary, version = download_node(requirements)
    print(f"Using Node.js v{version[0]}.{version[1]}.{version[2]} for this frontend build", flush=True)
    return binary


def npm_command(node):
    node_dir = Path(node).parent
    bundled_cli = node_dir.parent / "lib/node_modules/npm/bin/npm-cli.js"
    if bundled_cli.is_file():
        return [node, str(bundled_cli)]
    matching_npm = node_dir / "npm"
    if matching_npm.is_file() and os.access(matching_npm, os.X_OK):
        return [str(matching_npm)]
    system_npm = shutil.which("npm")
    if system_npm is None:
        fail("npm was not found beside the selected Node.js runtime or on PATH")
    return [system_npm]


def reject_symlinks(directory):
    for path in directory.rglob("*"):
        if path.is_symlink():
            fail(f"built output contains a symlink, which this small image builder does not package: {path.relative_to(directory)}")


def prepare_npm_project(project, package, package_names):
    if not (project / "package-lock.json").is_file() and any((project / lock).is_file() for lock in ("pnpm-lock.yaml", "yarn.lock")):
        fail("this adapter currently uses npm; include a package-lock.json or install dependencies with npm before building")
    node = compatible_node(project, package, package_names)
    npm = npm_command(node)
    env = clean_project_environment()
    env["PATH"] = str(Path(node).parent) + os.pathsep + env.get("PATH", "")
    if not (project / "node_modules").is_dir():
        if (project / "package-lock.json").is_file():
            run([*npm, "ci"], project, env)
        else:
            run([*npm, "install"], project, env)
    return npm, env


def package_static_web(dist, archive, repo, command_hint):
    if not dist.is_dir() or not (dist / "index.html").is_file():
        fail(f"static frontend build did not produce {dist}/index.html")
    reject_symlinks(dist)
    go = shutil.which("go")
    if go is None:
        fail("Go is required to build MiniDock's small static frontend server")
    with tempfile.TemporaryDirectory(prefix="minidock-source-build-") as temporary:
        work = Path(temporary)
        webserver = work / "minidock-web"
        env = clean_project_environment()
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
        command = ["/usr/bin/minidock-web", "--root", "/usr/share/minidock/www", "--listen", "0.0.0.0:8080"]
        write_image_metadata(rootfs, command, 8080)
        write_rootfs_archive(rootfs, archive)
    print(command_hint)


def build_vite(project, archive, repo, package_existing_dist=False):
    package_file = project / "package.json"
    if not package_file.is_file():
        fail(f"{project} is not a supported source project (package.json was not found)")
    try:
        package = json.loads(package_file.read_text(encoding="utf-8"))
    except (OSError, ValueError) as error:
        fail(f"could not read package.json: {error}")
    dependencies = package.get("dependencies", {})
    dev_dependencies = package.get("devDependencies", {})
    if not isinstance(dependencies, dict) or not isinstance(dev_dependencies, dict):
        fail("package.json dependencies and devDependencies must be objects")
    dependencies = {**dependencies, **dev_dependencies}
    if "vite" not in dependencies or not package.get("scripts", {}).get("build"):
        fail("source builds currently support Vite projects with a package.json build script")

    if not package_existing_dist:
        npm, build_env = prepare_npm_project(project, package, ("vite",))
        run([*npm, "run", "build"], project, build_env)
    dist = project / "dist"
    package_static_web(dist, archive, repo, "Run with: bin/minidock run -d IMAGE /usr/bin/minidock-web --root /usr/share/minidock/www --listen 0.0.0.0:8080")


def build_angular(project, archive, repo):
    package_file = project / "package.json"
    try:
        package = json.loads(package_file.read_text(encoding="utf-8"))
    except (OSError, ValueError) as error:
        fail(f"could not read package.json: {error}")
    dependencies = package.get("dependencies", {})
    dev_dependencies = package.get("devDependencies", {})
    if not isinstance(dependencies, dict) or not isinstance(dev_dependencies, dict):
        fail("package.json dependencies and devDependencies must be objects")
    dependencies = {**dependencies, **dev_dependencies}
    if "@angular/core" not in dependencies or "@angular/cli" not in dependencies:
        fail("Angular builds need @angular/core and @angular/cli in package.json")
    settings = angular_application(project)
    npm, build_env = prepare_npm_project(project, package, ("@angular/core", "@angular/cli", "@angular/build", "@angular-devkit/build-angular"))
    if not package.get("scripts", {}).get("build"):
        fail("Angular package.json needs a build script (usually ng build)")
    run([*npm, "run", "build"], project, build_env)
    dist = (project / settings["output_path"]).resolve()
    if not dist.is_relative_to(project.resolve()):
        fail("Angular outputPath must stay inside the project folder")
    if settings["browser_subdir"] and (dist / settings["browser_subdir"] / "index.html").is_file():
        dist /= settings["browser_subdir"]
    package_static_web(dist, archive, repo, "Run with: bin/minidock run -d IMAGE /usr/bin/minidock-web --root /usr/share/minidock/www --listen 0.0.0.0:8080")


def clean_project_environment():
    env = os.environ.copy()
    for name in ("OPENROUTER_API_KEY", "OPENROUTER_MODEL", "OPENROUTER_FAST_MODEL",
                 "OPENROUTER_HEAVY_MODEL", "OPENROUTER_MAX_TOKENS"):
        env.pop(name, None)
    return env


def write_rootfs_archive(rootfs, archive):
    with tarfile.open(archive, "w:gz", format=tarfile.PAX_FORMAT) as bundle:
        bundle.add(rootfs, arcname=".")


def write_image_metadata(rootfs, command, container_port=None):
    metadata = rootfs / "etc" / "minidock" / "image.json"
    metadata.parent.mkdir(parents=True, exist_ok=True)
    metadata.write_text(json.dumps({
        "command": command,
        "container_port": container_port,
    }, separators=(",", ":")) + "\n", encoding="utf-8")


def go_module_and_package(project):
    module_root = next((path for path in (project, *project.parents) if (path / "go.mod").is_file()), None)
    if module_root is None:
        fail("go.mod was not found in this directory or its parents")
    package_dir = project
    if not any(path.suffix == ".go" for path in package_dir.glob("*.go")):
        candidates = [path.parent for path in package_dir.glob("cmd/*/main.go")]
        if len(candidates) == 1:
            package_dir = candidates[0]
        elif len(candidates) > 1:
            fail("multiple Go command packages found; pass the directory of the command to build")
    main_file = package_dir / "main.go"
    try:
        source = main_file.read_text(encoding="utf-8")
    except OSError:
        fail("Go builds need a main.go package; pass the directory of the Go command to build")
    if not re.search(r"(?m)^\s*package\s+main\b", source):
        fail("Go builds need a main.go package; pass the directory of the Go command to build")
    package = "." if package_dir == module_root else "./" + package_dir.relative_to(module_root).as_posix()
    name = package_dir.name if package_dir != module_root else module_root.name
    return module_root, package, name


def build_go(project, archive):
    module_root, package, binary_name = go_module_and_package(project)
    go = shutil.which("go")
    if go is None:
        fail("Go is required to compile this project")
    safe_name = re.sub(r"[^A-Za-z0-9._-]", "-", binary_name)[:100] or "app"
    with tempfile.TemporaryDirectory(prefix="minidock-go-build-") as temporary:
        work = Path(temporary)
        binary = work / safe_name
        env = clean_project_environment()
        env.update({"CGO_ENABLED": "0", "GOOS": "linux"})
        env.setdefault("GOCACHE", "/tmp/minidock-go-cache")
        run([go, "build", "-trimpath", "-ldflags=-s -w", "-o", str(binary), package], module_root, env)
        rootfs = work / "rootfs"
        executable = rootfs / "usr" / "bin" / safe_name
        executable.parent.mkdir(parents=True)
        shutil.copy2(binary, executable)
        package_dir = module_root if package == "." else module_root / package[2:]
        source = (package_dir / "main.go").read_text(encoding="utf-8", errors="replace")
        port_match = re.search(r'(?s)port\s*:=\s*os\.Getenv\("PORT"\).*?if\s+port\s*==\s*""\s*\{\s*port\s*=\s*"([0-9]{1,5})"', source)
        default_port = int(port_match.group(1)) if port_match else None
        if default_port is not None and not 1 <= default_port <= 65535:
            default_port = None
        write_image_metadata(rootfs, ["/usr/bin/" + safe_name], default_port)
        write_rootfs_archive(rootfs, archive)
    print(f"Run with: bin/minidock run -d IMAGE /usr/bin/{safe_name}")


def maven_runner(project):
    wrapper = project / "mvnw"
    if wrapper.is_file():
        return ["sh", str(wrapper)]
    maven = shutil.which("mvn")
    if maven is None:
        fail("Maven was not found; include the Maven wrapper or install Maven")
    return [maven]


def executable_jar(path):
    try:
        with zipfile.ZipFile(path) as jar:
            manifest = jar.read("META-INF/MANIFEST.MF").decode("utf-8", errors="replace")
    except (OSError, KeyError, zipfile.BadZipFile):
        return False
    unfolded = re.sub(r"\r?\n[ \t]", "", manifest)
    return re.search(r"(?im)^Main-Class:\s*\S+", unfolded) is not None


def find_executable_jar(project):
    candidates = [path for path in (project / "target").glob("*.jar") if not path.name.endswith(".original")]
    candidates = [path for path in candidates if executable_jar(path)]
    if len(candidates) != 1:
        fail("expected exactly one executable target/*.jar; configure Maven to produce a runnable JAR")
    return candidates[0]


def copy_runtime_libraries(runtime, rootfs, runtime_name="application"):
    ldd = shutil.which("ldd")
    if ldd is None:
        fail(f"ldd is required to bundle {runtime_name}'s Linux shared libraries")
    libraries = set()
    for path in runtime.rglob("*"):
        if path.is_symlink() or not path.is_file():
            continue
        try:
            with path.open("rb") as source:
                if source.read(4) != b"\x7fELF":
                    continue
        except OSError:
            continue
        runtime_env = os.environ.copy()
        library_paths = [runtime / "lib" / "server", runtime / "lib"]
        if runtime_env.get("LD_LIBRARY_PATH"):
            library_paths.append(Path(runtime_env["LD_LIBRARY_PATH"]))
        runtime_env["LD_LIBRARY_PATH"] = ":".join(str(path) for path in library_paths)
        result = subprocess.run([ldd, str(path)], text=True, capture_output=True, env=runtime_env)
        output = result.stdout + result.stderr
        if "not found" in output:
            missing = next((line.strip() for line in output.splitlines() if "not found" in line), "unknown library")
            fail(f"cannot bundle {runtime_name} dependency for {path.relative_to(runtime)}: {missing}")
        for line in output.splitlines():
            match = re.search(r"=>\s+(/\S+)", line) or re.match(r"\s*(/\S+)\s+\(", line)
            if match:
                libraries.add(Path(match.group(1)))
    for library in libraries:
        try:
            resolved = library.resolve(strict=True)
        except OSError:
            fail(f"could not resolve {runtime_name} library {library}")
        destination = rootfs / library.relative_to("/")
        destination.parent.mkdir(parents=True, exist_ok=True)
        if library.is_symlink():
            if not destination.exists() and not destination.is_symlink():
                destination.symlink_to(os.readlink(library))
        elif not destination.exists():
            shutil.copy2(library, destination)
        real_destination = rootfs / resolved.relative_to("/")
        if not real_destination.exists():
            real_destination.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(resolved, real_destination)


def java_tools():
    java = shutil.which("java")
    if java is None:
        fail("a JDK is required to build a self-contained Java image")
    jdk_bin = Path(java).resolve().parent
    jlink = jdk_bin / "jlink"
    jdeps = jdk_bin / "jdeps"
    if not jlink.is_file() or not jdeps.is_file():
        fail("a JDK with jlink is required to bundle a Java runtime; a JRE alone is not enough")
    return jlink, jdeps


def java_modules(jdeps, jar, work):
    classes = work / "app-classes"
    dependencies = work / "app-dependencies"
    classes.mkdir()
    dependencies.mkdir()
    nested_jars = []
    try:
        with zipfile.ZipFile(jar) as archive:
            for index, info in enumerate(archive.infolist()):
                name = PurePosixPath(info.filename)
                if name.is_absolute() or ".." in name.parts:
                    fail("executable JAR contains an unsafe archive path")
                if info.is_dir():
                    continue
                if info.filename.startswith("BOOT-INF/classes/"):
                    relative = PurePosixPath(*name.parts[2:])
                    destination = classes.joinpath(*relative.parts)
                    destination.parent.mkdir(parents=True, exist_ok=True)
                    destination.write_bytes(archive.read(info))
                elif info.filename.startswith("BOOT-INF/lib/") and info.filename.endswith(".jar"):
                    destination = dependencies / f"dependency-{index}.jar"
                    destination.write_bytes(archive.read(info))
                    nested_jars.append(destination)
    except (OSError, zipfile.BadZipFile) as error:
        fail(f"could not inspect executable JAR dependencies: {error}")
    module_path = os.pathsep.join(str(path) for path in nested_jars)
    command = [str(jdeps), "--multi-release", "base", "--ignore-missing-deps", "--recursive", "--print-module-deps"]
    if module_path:
        command.extend(["--class-path", module_path])
    command.append(str(classes if any(classes.iterdir()) else jar))
    try:
        result = subprocess.run(command, check=True, text=True, capture_output=True)
    except subprocess.CalledProcessError as error:
        detail = (error.stderr or error.stdout or "unknown jdeps error").strip()
        fail(f"jdeps could not analyze the Java app: {detail[:500]}")
    modules = set(filter(None, result.stdout.strip().split(",")))
    modules.update({"java.base", "java.logging", "java.management", "java.naming", "java.sql", "java.xml", "jdk.crypto.ec", "jdk.unsupported"})
    if not modules:
        fail("jdeps did not find Java modules required by the app")
    return ",".join(sorted(modules))


def build_java(project, archive, package_existing_jar=False):
    if sys.platform != "linux":
        fail("Java images currently need to be built on Linux so MiniDock can bundle Linux runtime libraries")
    if not (project / "pom.xml").is_file():
        fail("Maven pom.xml was not found")
    env = clean_project_environment()
    if not package_existing_jar:
        repository = env.get("MINIDOCK_MAVEN_REPO", "/tmp/minidock-maven-cache")
        command = maven_runner(project) + [f"-Dmaven.repo.local={repository}", "-Dmaven.test.skip=true", "package"]
        pom = (project / "pom.xml").read_text(encoding="utf-8")
        if re.search(r"<artifactId>\s*spring-boot-maven-plugin\s*</artifactId>", pom):
            command.append("spring-boot:repackage")
        run(command, project, env)
    jar = find_executable_jar(project)
    jlink, jdeps = java_tools()
    with tempfile.TemporaryDirectory(prefix="minidock-java-build-") as temporary:
        work = Path(temporary)
        rootfs = work / "rootfs"
        runtime = rootfs / "usr" / "lib" / "minidock-jre"
        runtime.parent.mkdir(parents=True)
        modules = java_modules(jdeps, jar, work)
        run([str(jlink), "--add-modules", modules, "--strip-debug", "--no-man-pages", "--no-header-files", "--compress=2", "--output", str(runtime)], project, env)
        app_dir = rootfs / "opt" / "minidock" / "app"
        app_dir.mkdir(parents=True)
        shutil.copy2(jar, app_dir / "app.jar")
        cacerts = runtime / "lib" / "security" / "cacerts"
        if cacerts.is_symlink():
            resolved_cacerts = cacerts.resolve(strict=True)
            cacerts.unlink()
            shutil.copy2(resolved_cacerts, cacerts)
        copy_runtime_libraries(runtime, rootfs)
        write_image_metadata(rootfs, ["/usr/lib/minidock-jre/bin/java", "-jar", "/opt/minidock/app/app.jar"],
                             maven_http_port(project))
        write_rootfs_archive(rootfs, archive)
    print("Run with: bin/minidock run -d IMAGE /usr/lib/minidock-jre/bin/java -jar /opt/minidock/app/app.jar")


def linux_dotnet_rid():
    architecture = platform.machine().lower()
    if architecture in {"x86_64", "amd64"}:
        return "linux-x64"
    if architecture in {"aarch64", "arm64"}:
        return "linux-arm64"
    fail(f"C# images are not configured for Linux architecture {architecture!r}")


def build_dotnet(project, archive):
    if sys.platform != "linux":
        fail("C# MiniDock images must be built on Linux for the current host CPU architecture")
    dotnet = shutil.which("dotnet")
    if dotnet is None:
        fail("the .NET SDK is required to build C# projects; install it on the host and retry")
    try:
        info = dotnet_project_info(project)
    except ValueError as error:
        fail(str(error))
    env = clean_project_environment()
    with tempfile.TemporaryDirectory(prefix="minidock-dotnet-build-") as temporary:
        work = Path(temporary)
        publish = work / "publish"
        run([dotnet, "publish", str(info["path"]), "-c", "Release", "-r", linux_dotnet_rid(),
             "--self-contained", "true", "-p:UseAppHost=true", "-p:PublishSingleFile=false",
             "-o", str(publish)], project, env)
        apphost = publish / info["assembly_name"]
        if not apphost.is_file():
            fail(f"dotnet publish did not produce the expected Linux apphost {apphost.name}")
        rootfs = work / "rootfs"
        app_dir = rootfs / "opt" / "minidock" / "app"
        app_dir.parent.mkdir(parents=True)
        shutil.copytree(publish, app_dir)
        copy_runtime_libraries(publish, rootfs, "C# application")
        command = ["/opt/minidock/app/" + info["assembly_name"]]
        port = None
        if info["is_web"]:
            command.extend(["--urls", "http://0.0.0.0:8080", "--contentRoot", "/opt/minidock/app"])
            port = 8080
        write_image_metadata(rootfs, command, port)
        write_rootfs_archive(rootfs, archive)
    print(f"Run with: bin/minidock run -d IMAGE {command[0]}" + (" --urls http://0.0.0.0:8080" if port else ""))


def python_source_copy(project, destination):
    ignored_names = {".git", ".venv", "venv", "__pycache__", "node_modules",
                     ".pytest_cache", ".mypy_cache", ".ruff_cache", ".tox"}
    for path in project.rglob("*"):
        if path.is_symlink():
            fail(f"Python source builds do not package symlinks: {path.relative_to(project)}")
    def ignore(directory, names):
        return {name for name in names if name in ignored_names or name == ".env" or name.startswith(".env.")}
    shutil.copytree(project, destination, ignore=ignore, symlinks=False)


def build_python(project, archive):
    if sys.platform != "linux":
        fail("Python MiniDock images must be built on Linux for the current host CPU architecture")
    python = shutil.which("python3")
    if python is None:
        fail("Python 3 is required to build a Python project")
    try:
        info = python_project_info(project)
    except ValueError as error:
        fail(str(error))
    env = clean_project_environment()
    with tempfile.TemporaryDirectory(prefix="minidock-python-build-") as temporary:
        work = Path(temporary)
        source = work / "project"
        python_source_copy(project, source)
        venv = work / "venv"
        run([python, "-m", "venv", str(venv)], source, env)
        venv_python = venv / "bin" / "python"
        pip = [str(venv_python), "-m", "pip", "install", "--disable-pip-version-check"]
        run([*pip, "pyinstaller"], source, env)
        if (source / "pyproject.toml").is_file():
            run([*pip, "."], source, env)
        if (source / "requirements.txt").is_file():
            run([*pip, "-r", "requirements.txt"], source, env)
        output = work / "dist"
        spec = work / "spec"
        build_temp = work / "pyinstaller-work"
        entrypoint = source / info["entrypoint"]
        name = re.sub(r"[^A-Za-z0-9._-]+", "-", Path(info["entrypoint"]).stem).strip(".-_") or "python-app"
        command = [str(venv / "bin" / "pyinstaller"), "--noconfirm", "--clean", "--onedir",
                   "--name", name, "--distpath", str(output), "--specpath", str(spec),
                   "--workpath", str(build_temp), str(entrypoint)]
        run(command, source, env)
        bundle = output / name
        executable = bundle / name
        if not executable.is_file():
            fail("PyInstaller did not produce the expected Python app executable")
        rootfs = work / "rootfs"
        app_dir = rootfs / "opt" / "minidock" / "app"
        app_dir.parent.mkdir(parents=True)
        shutil.copytree(bundle, app_dir)
        copy_runtime_libraries(bundle, rootfs, "Python application")
        metadata_command = ["/opt/minidock/app/" + name, *info["args"]]
        write_image_metadata(rootfs, metadata_command, info["container_port"])
        write_rootfs_archive(rootfs, archive)
    print(f"Run with: bin/minidock run -d IMAGE {metadata_command[0]}")


def main():
    mode = sys.argv[3] if len(sys.argv) == 4 else "build"
    if len(sys.argv) not in (3, 4) or mode not in ("build", "--from-dist", "--from-jar"):
        fail("usage: minidock_build.py PROJECT_DIR OUTPUT.tar.gz [--from-dist|--from-jar]")
    project = Path(sys.argv[1]).expanduser().resolve()
    archive = Path(sys.argv[2]).expanduser().resolve()
    repo = Path(__file__).resolve().parent.parent
    if not project.is_dir():
        fail(f"project directory does not exist: {project}")
    if not archive.parent.is_dir():
        fail(f"output directory does not exist: {archive.parent}")
    package_file = project / "package.json"
    is_angular = (project / "angular.json").is_file()
    vite_project = False
    if package_file.is_file():
        try:
            package = json.loads(package_file.read_text(encoding="utf-8"))
        except (OSError, ValueError):
            package = None
        if isinstance(package, dict):
            dependencies = package.get("dependencies", {})
            dev_dependencies = package.get("devDependencies", {})
            scripts = package.get("scripts", {})
            vite_project = (
                isinstance(dependencies, dict)
                and isinstance(dev_dependencies, dict)
                and "vite" in {**dependencies, **dev_dependencies}
                and isinstance(scripts, dict)
                and bool(scripts.get("build"))
            )

    if is_angular:
        if mode != "build":
            fail("--from-dist/--from-jar modes are not available for Angular projects yet")
        build_angular(project, archive, repo)
    elif vite_project:
        if mode == "--from-jar":
            fail("--from-jar only applies to Maven Java projects")
        build_vite(project, archive, repo, mode == "--from-dist")
    elif (project / "pom.xml").is_file():
        if mode == "--from-dist":
            fail("--from-dist only applies to Vite projects")
        build_java(project, archive, mode == "--from-jar")
    elif any((path / "go.mod").is_file() for path in (project, *project.parents)):
        if mode != "build":
            fail("existing-binary options are not available for Go projects yet")
        build_go(project, archive)
    elif list(project.glob("*.csproj")) or list(project.glob("*.sln")) or list(project.glob("*.slnx")):
        if mode != "build":
            fail("existing-artifact options are not available for C# projects yet")
        build_dotnet(project, archive)
    elif ((project / "requirements.txt").is_file() or (project / "pyproject.toml").is_file()
          or (project / "minidock.json").is_file() or (project / "main.py").is_file() or (project / "app.py").is_file()):
        if mode != "build":
            fail("existing-artifact options are not available for Python projects yet")
        build_python(project, archive)
    elif package_file.is_file():
        if mode == "--from-jar":
            fail("--from-jar only applies to Maven Java projects")
        build_vite(project, archive, repo, mode == "--from-dist")
    else:
        fail("no supported source project detected; supported adapters are Vite, Angular, Go main packages, Maven Java projects, runnable C# projects, and Python script apps")
    print(f"MiniDock image archive created: {archive}")


if __name__ == "__main__":
    main()
