#!/usr/bin/env python3
"""Small, dependency-free terminal renderer for MiniDock CLI responses."""

import json
import os
import re
import shutil
import sys


RESET = "\033[0m"
PALETTE = {
    "cyan": "\033[38;2;59;220;255m",
    "blue": "\033[38;2;104;147;255m",
    "purple": "\033[38;2;194;120;255m",
    "green": "\033[38;2;74;222;128m",
    "yellow": "\033[38;2;250;204;88m",
    "red": "\033[38;2;255;102;120m",
    "muted": "\033[38;2;148;163;184m",
    "white": "\033[38;2;235;245;255m",
}


def enabled():
    return sys.stdout.isatty() and "NO_COLOR" not in os.environ and os.environ.get("TERM") != "dumb"


def safe(value):
    text = str(value if value is not None else "—")
    return re.sub(r"[\x00-\x1f\x7f-\x9f]", " ", text)


def color(text, name):
    text = safe(text)
    return f"{PALETTE[name]}{text}{RESET}" if enabled() else text


def heading(title, detail=""):
    print(f"{color('◆', 'cyan')} {color('MINIDOCK', 'purple')}  {color(title, 'white')}")
    if detail:
        print(f"  {color(detail, 'muted')}")


def line(label, value, tone="white"):
    print(f"  {color(label, 'muted')}  {color(value, tone)}")


def render_build(data):
    heading("IMAGE BUILT", "Your image is staged and ready to use")
    line("Image", data.get("name", "unknown"), "green")
    line("Archive", data.get("archive", "—"), "cyan")
    if data.get("path"):
        line("Location", data["path"], "white")


def render_images(data):
    images = data if isinstance(data, list) else []
    heading("IMAGES", f"{len(images)} image(s) in your local store")
    if not images:
        print(f"  {color('No images yet. Build one with `bin/minidock build`.', 'muted')}")
        return
    for image in images:
        print(f"  {color('●', 'cyan')} {color(image, 'white')}")


def status_color(status):
    return {"RUNNING": "green", "PREPARING": "yellow", "EXITED": "muted", "FAILED": "red", "STOPPING": "yellow"}.get(str(status), "white")


def render_containers(data):
    containers = data if isinstance(data, list) else []
    heading("CONTAINERS", f"{len(containers)} active container(s)")
    if not containers:
        print(f"  {color('Nothing running. Start one with `bin/minidock run`.', 'muted')}")
        return
    headers = ["ID", "IMAGE", "STATUS", "PID", "IP", "COMMAND"]
    rows = []
    for item in containers:
        command = " ".join(safe(part) for part in (item.get("command") or []))
        rows.append([
            safe(item.get("id")), safe(item.get("image")), safe(item.get("status")),
            safe(item.get("pid")), safe(item.get("networkIp")), command,
        ])
    widths = [12, 20, 11, 8, 15]
    available = max(20, shutil.get_terminal_size((120, 24)).columns - sum(widths) - 7)
    widths.append(available)

    def truncate(text, width):
        return text if len(text) <= width else text[: max(1, width - 1)] + "…"

    def cells(values, header=False):
        styled = []
        for index, value in enumerate(values):
            display = truncate(value, widths[index]).ljust(widths[index])
            if header:
                styled.append(color(display, "cyan"))
            elif index == 2:
                styled.append(color(display, status_color(value)))
            else:
                styled.append(color(display, "white"))
        print("  " + " ".join(styled))

    cells(headers, True)
    print("  " + color("─" * min(sum(widths) + len(widths) - 1, shutil.get_terminal_size((120, 24)).columns - 2), "muted"))
    for row in rows:
        cells(row)


def render_container(data, title="CONTAINER"):
    status = safe(data.get("status", "UNKNOWN"))
    heading(title, f"{data.get('image', 'image')} · {status}")
    line("ID", data.get("id"), "cyan")
    line("PID", data.get("pid"), "white")
    if data.get("networkIp"):
        line("Network", data["networkIp"], "blue")
    line("Command", " ".join(safe(arg) for arg in (data.get("command") or [])), "white")
    if data.get("error"):
        line("Error", data["error"], "red")


def render_plan(data):
    heading("AI PROPOSAL", "Review the generated plan before it is sent to MiniDock")
    for key, value in data.items():
        if isinstance(value, list):
            value = " ".join(safe(item) for item in value)
        line(key.replace("_", " ").title(), value, "cyan" if key in ("image", "builder") else "white")


def main():
    if len(sys.argv) != 2:
        raise SystemExit("usage: minidock_ui.py build|images|containers|container|plan")
    raw = sys.stdin.read()
    try:
        data = json.loads(raw)
    except json.JSONDecodeError:
        sys.stdout.write(raw)
        return
    if not enabled():
        print(json.dumps(data, separators=(",", ":"), ensure_ascii=False))
        return
    kind = sys.argv[1]
    if kind == "build" and isinstance(data, dict):
        render_build(data)
    elif kind == "images":
        render_images(data)
    elif kind == "containers":
        render_containers(data)
    elif kind == "container" and isinstance(data, dict):
        render_container(data)
    elif kind == "stopped" and isinstance(data, dict):
        render_container(data, "CONTAINER STOPPED")
    elif kind == "plan" and isinstance(data, dict):
        render_plan(data)
    else:
        print(json.dumps(data, indent=2, ensure_ascii=False))


if __name__ == "__main__":
    main()
