#!/usr/bin/env python3
"""Show an arrow-key menu for locally defined MiniDock build recipes."""

import curses
import json
import sys
from pathlib import Path


def read_plan(path):
    try:
        plan = json.loads(Path(path).read_text(encoding="utf-8"))
        options = plan["options"]
        if not options or any(set(option) != {"id", "title", "description", "recipe"} for option in options):
            raise ValueError
        return plan
    except (OSError, ValueError, KeyError, TypeError):
        print("minidock ask-build: invalid local build menu", file=sys.stderr)
        raise SystemExit(1)


def select_with_curses(stdscr, plan):
    options = plan["options"]
    selected = 0
    curses.curs_set(0)
    while True:
        stdscr.erase()
        stdscr.addstr(0, 0, "AI project assessment")
        summary = plan["summary"].replace("\n", " ").replace("\t", " ")
        stdscr.addstr(2, 0, summary[: max(1, curses.COLS - 1)])
        stdscr.addstr(4, 0, "Choose a local build recipe (↑/↓, Enter; q cancels):")
        for index, option in enumerate(options):
            marker = ">" if index == selected else " "
            recommendation = " [AI recommendation]" if index == 0 else ""
            title = f"{marker} {option['title']}{recommendation}"
            description = option["description"]
            if index == selected:
                stdscr.addstr(6 + index * 3, 0, title[: max(1, curses.COLS - 1)], curses.A_REVERSE)
            else:
                stdscr.addstr(6 + index * 3, 0, title[: max(1, curses.COLS - 1)])
            stdscr.addstr(7 + index * 3, 2, description[: max(1, curses.COLS - 3)])
        stdscr.refresh()
        key = stdscr.getch()
        if key in (curses.KEY_UP, ord("k")):
            selected = (selected - 1) % len(options)
        elif key in (curses.KEY_DOWN, ord("j")):
            selected = (selected + 1) % len(options)
        elif key in (curses.KEY_ENTER, 10, 13):
            return options[selected]["id"]
        elif key in (ord("q"), 27):
            return ""


def select_numbered(plan):
    print("AI project assessment")
    print(plan["summary"])
    for index, option in enumerate(plan["options"], start=1):
        recommendation = " (AI recommendation)" if index == 1 else ""
        print(f"{index}. {option['title']}{recommendation}\n   {option['description']}")
    default = plan["recommended_order"][0]
    prompt = f"Choose a recipe [1-{len(plan['options'])}, Enter for AI recommendation, q to cancel]: "
    while True:
        try:
            answer = input(prompt).strip()
        except EOFError:
            return ""
        if answer.lower() == "q":
            return ""
        if answer == "":
            return default
        if answer.isdigit() and 1 <= int(answer) <= len(plan["options"]):
            return plan["options"][int(answer) - 1]["id"]
        print("Enter one of the listed numbers, or q to cancel.")


def main():
    if len(sys.argv) != 3:
        print("usage: minidock_select_build.py PLAN.json SELECTION.txt", file=sys.stderr)
        raise SystemExit(2)
    plan = read_plan(sys.argv[1])
    if sys.stdin.isatty() and sys.stdout.isatty():
        try:
            choice = curses.wrapper(select_with_curses, plan)
        except curses.error:
            choice = select_numbered(plan)
    else:
        choice = select_numbered(plan)
    Path(sys.argv[2]).write_text(choice, encoding="utf-8")


if __name__ == "__main__":
    main()
