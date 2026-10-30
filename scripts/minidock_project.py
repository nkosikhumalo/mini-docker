"""Small shared project-detection helpers for MiniDock's fixed adapters."""

import re
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
