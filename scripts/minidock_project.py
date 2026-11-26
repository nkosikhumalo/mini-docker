"""Small shared project-detection helpers for MiniDock's fixed adapters."""

import json
import re
import xml.etree.ElementTree as ET
from pathlib import Path


def maven_http_port(project: Path) -> int | None:
    """Return a confidently known Spring Boot HTTP port, otherwise None.

    Generic executable JARs are often command-line apps, so do not advertise a
    port for every Maven project. Spring Boot's default is 8080 only when no
    explicit port configuration is found.
    """
    try:
        pom = (project / "pom.xml").read_text(encoding="utf-8")
    except OSError:
        return None
    if not re.search(r"<artifactId>\s*spring-boot-maven-plugin\s*</artifactId>", pom):
        return None
    if not re.search(r"<artifactId>\s*spring-boot-starter-(?:web|webflux)\s*</artifactId>", pom):
        return None

    configured: list[int] = []
    resources = project / "src" / "main" / "resources"
    property_pattern = re.compile(r"^\s*server\.port\s*[=:]\s*(.*?)\s*(?:#.*)?$")
    for file in resources.glob("application*.properties"):
        try:
            lines = file.read_text(encoding="utf-8").splitlines()
        except (OSError, UnicodeError):
            return None
        for line in lines:
            if line.lstrip().startswith(("#", "!")):
                continue
            match = property_pattern.match(line)
            if match:
                value = match.group(1).strip()
                if not value.isdecimal() or not 1 <= int(value) <= 65535:
                    return None
                configured.append(int(value))

    yaml_pattern = re.compile(
        r"(?ms)^\s*server\s*:\s*\n(?:(?:[ \t]+[^\n]*\n)|\n)*?"
        r"[ \t]+port\s*:\s*([0-9]+)\s*(?:#.*)?$"
    )
    for file in (*resources.glob("application*.yml"), *resources.glob("application*.yaml")):
        try:
            content = file.read_text(encoding="utf-8")
        except (OSError, UnicodeError):
            return None
        for match in yaml_pattern.finditer(content):
            value = int(match.group(1))
            if not 1 <= value <= 65535:
                return None
            configured.append(value)
        if re.search(r"(?m)^\s*server\s*:\s*$", content) and not yaml_pattern.search(content):
            return None

    if not configured:
        return 8080
    unique = set(configured)
    return unique.pop() if len(unique) == 1 else None


def angular_application(project: Path) -> dict:
    """Read the one Angular application build target from angular.json."""
    try:
        workspace = json.loads((project / "angular.json").read_text(encoding="utf-8"))
    except (OSError, ValueError) as error:
        raise ValueError(f"could not read angular.json: {error}") from None
    projects = workspace.get("projects") if isinstance(workspace, dict) else None
    if not isinstance(projects, dict):
        raise ValueError("angular.json must contain a projects object")

    default_name = workspace.get("defaultProject")
    if isinstance(default_name, str) and default_name in projects:
        candidates = [(default_name, projects[default_name])]
    else:
        candidates = [(name, config) for name, config in projects.items()
                      if isinstance(config, dict) and config.get("projectType") == "application"]
        if len(candidates) != 1:
            raise ValueError("Angular workspace has multiple apps; select a workspace with one app or set defaultProject in angular.json")

    name, config = candidates[0]
    targets = config.get("architect", config.get("targets", {}))
    target = targets.get("build") if isinstance(targets, dict) else None
    if not isinstance(target, dict):
        raise ValueError(f"Angular project {name!r} has no build target")
    builder = target.get("builder", "")
    if builder.endswith(":library"):
        raise ValueError("Angular libraries are not runnable apps; choose an Angular application project")
    if not any(builder.endswith(suffix) for suffix in (":application", ":browser", ":browser-esbuild")):
        raise ValueError(f"Angular builder {builder!r} is not supported by MiniDock's static web adapter")
    options = target.get("options", {})
    options = options if isinstance(options, dict) else {}
    output = options.get("outputPath", f"dist/{name}")
    browser_subdir = "browser"
    if isinstance(output, dict):
        browser_subdir = output.get("browser", "browser")
        output = output.get("base", f"dist/{name}")
    if not isinstance(output, str) or not output or not isinstance(browser_subdir, str):
        raise ValueError(f"Angular project {name!r} has an invalid outputPath")
    browser_path = Path(browser_subdir)
    if browser_path.is_absolute() or ".." in browser_path.parts:
        raise ValueError(f"Angular project {name!r} has an unsafe browser output path")
    configurations = target.get("configurations", {})
    configurations = configurations if isinstance(configurations, dict) else {}
    production = configurations.get("production", {})
    output_mode = production.get("outputMode", options.get("outputMode", "static")) if isinstance(production, dict) else options.get("outputMode", "static")
    if output_mode == "server":
        raise ValueError("Angular server-rendered output needs a Node server adapter; MiniDock currently supports static/browser output")
    return {"name": name, "builder": builder, "output_path": output, "browser_subdir": browser_subdir}


def dotnet_project_file(project: Path) -> Path:
    """Find an unambiguous C# project file without walking generated folders."""
    direct = sorted(project.glob("*.csproj"))
    if not direct:
        ignored = {".git", "bin", "obj", "node_modules", "target", "vendor"}
        direct = sorted(path for path in project.rglob("*.csproj")
                        if not any(part in ignored or part.startswith(".") for part in path.relative_to(project).parts[:-1]))
    if len(direct) != 1:
        if not direct:
            raise ValueError("no .csproj file was found")
        raise ValueError("multiple .csproj files were found; point MiniDock at the folder containing the app you want to build")
    return direct[0]


def dotnet_project_info(project: Path) -> dict:
    path = dotnet_project_file(project)
    try:
        root = ET.parse(path).getroot()
    except (ET.ParseError, OSError) as error:
        raise ValueError(f"could not read {path.name}: {error}") from None
    sdk = root.attrib.get("Sdk", "")
    if not sdk:
        sdk_element = next((node for node in list(root) if node.tag.rsplit("}", 1)[-1] == "Sdk"), None)
        sdk = sdk_element.attrib.get("Name", "") if sdk_element is not None else ""
    properties = [node for node in root.iter() if node.tag.rsplit("}", 1)[-1] == "PropertyGroup"]
    output_type = next(((node.text or "").strip() for group in properties for node in list(group)
                        if node.tag.rsplit("}", 1)[-1] == "OutputType"), "")
    is_test = any((node.text or "").strip().casefold() == "true" for group in properties for node in list(group)
                  if node.tag.rsplit("}", 1)[-1] == "IsTestProject")
    assembly = next(((node.text or "").strip() for group in properties for node in list(group)
                     if node.tag.rsplit("}", 1)[-1] == "AssemblyName"), path.stem)
    is_web = "Microsoft.NET.Sdk.Web" in sdk
    if is_test:
        raise ValueError("MiniDock builds C# applications, not test projects")
    if not is_web and output_type.casefold() not in {"exe", "winexe"}:
        raise ValueError("MiniDock builds runnable C# projects; set OutputType to Exe or use Microsoft.NET.Sdk.Web")
    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]{0,99}", assembly):
        raise ValueError("C# project AssemblyName contains unsupported characters")
    return {"path": path, "assembly_name": assembly, "is_web": is_web}


def python_project_info(project: Path) -> dict:
    """Find a simple Python script app or validate an optional minidock.json entrypoint."""
    entrypoint = None
    args = []
    port = None
    config_path = project / "minidock.json"
    if config_path.is_file():
        try:
            config = json.loads(config_path.read_text(encoding="utf-8"))
        except (OSError, ValueError) as error:
            raise ValueError(f"could not read minidock.json: {error}") from None
        python_config = config.get("python") if isinstance(config, dict) else None
        if python_config is not None:
            if not isinstance(python_config, dict):
                raise ValueError("minidock.json python must be an object")
            entrypoint = python_config.get("entrypoint")
            args = python_config.get("args", [])
            port = python_config.get("container_port")
    if entrypoint is None:
        candidates = [project / name for name in ("main.py", "app.py") if (project / name).is_file()]
        if len(candidates) != 1:
            raise ValueError("Python apps need one main.py or app.py entrypoint; use minidock.json to choose an entrypoint when ambiguous")
        entrypoint = candidates[0].name
    if not isinstance(entrypoint, str) or not entrypoint.endswith(".py"):
        raise ValueError("Python entrypoint must be a relative .py file")
    relative = Path(entrypoint)
    resolved = (project / relative).resolve()
    if relative.is_absolute() or ".." in relative.parts or not resolved.is_relative_to(project.resolve()) or not resolved.is_file():
        raise ValueError("Python entrypoint must name a .py file inside the project folder")
    if not isinstance(args, list) or any(not isinstance(arg, str) or "\0" in arg for arg in args):
        raise ValueError("Python args must be a list of strings")
    if port is not None and (not isinstance(port, int) or isinstance(port, bool) or not 1 <= port <= 65535):
        raise ValueError("Python container_port must be between 1 and 65535")
    return {"entrypoint": relative.as_posix(), "args": args, "container_port": port}
