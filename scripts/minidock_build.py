#!/usr/bin/env python3
"""Build supported frontend source projects into MiniDock rootfs archives."""

import json
import os
import re
import shutil
import subprocess
import sys
import zipfile
import tarfile
import tempfile
from pathlib import PurePosixPath
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
        write_image_metadata(rootfs,
                             ["/usr/bin/minidock-web", "--root", "/usr/share/minidock/www",
                              "--listen", "0.0.0.0:8080"], 8080)
        with tarfile.open(archive, "w:gz", format=tarfile.PAX_FORMAT) as bundle:
            bundle.add(rootfs, arcname=".")


def clean_project_environment():
    env = os.environ.copy()
    env.pop("OPENROUTER_API_KEY", None)
    env.pop("OPENROUTER_MODEL", None)
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


def copy_runtime_libraries(runtime, rootfs):
    ldd = shutil.which("ldd")
    if ldd is None:
        fail("ldd is required to bundle the Java runtime's Linux shared libraries")
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
            fail(f"cannot bundle Java runtime dependency for {path.relative_to(runtime)}: {missing}")
        for line in output.splitlines():
            match = re.search(r"=>\s+(/\S+)", line) or re.match(r"\s*(/\S+)\s+\(", line)
            if match:
                libraries.add(Path(match.group(1)))
    for library in libraries:
        try:
            resolved = library.resolve(strict=True)
        except OSError:
            fail(f"could not resolve Java runtime library {library}")
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
        write_image_metadata(rootfs, ["/usr/lib/minidock-jre/bin/java", "-jar", "/opt/minidock/app/app.jar"], 8080)
        write_rootfs_archive(rootfs, archive)
    print("Run with: bin/minidock run -d IMAGE /usr/lib/minidock-jre/bin/java -jar /opt/minidock/app/app.jar")


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

    if vite_project:
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
    elif package_file.is_file():
        if mode == "--from-jar":
            fail("--from-jar only applies to Maven Java projects")
        build_vite(project, archive, repo, mode == "--from-dist")
    else:
        fail("no supported source project detected; supported adapters are Vite, Go main packages, and Maven Java projects")
    print(f"MiniDock image archive created: {archive}")


if __name__ == "__main__":
    main()
