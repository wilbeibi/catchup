#!/usr/bin/env python3
"""Codex turn-end quota reminder, wired once via `notify` in ~/.codex/config.toml:

    notify = ["python3", "/home/YOU/.local/bin/quota-reminder.py"]

Codex passes the turn-complete event JSON as the last argument. When the newest
quota snapshot in the session log shows 25% or less left, sends one desktop
notification per quota window. Marker files under $XDG_STATE_HOME/catchup-quota
remember what was already sent — delete them to re-test.
"""
import json
import os
import subprocess
import sys
import time
from pathlib import Path

STATE = Path(os.environ.get("XDG_STATE_HOME", str(Path.home() / ".local/state"))) / "catchup-quota"


def main():
    event = json.loads(sys.argv[1])
    if event.get("type") != "agent-turn-complete":
        return
    root = Path(os.environ.get("CODEX_HOME", str(Path.home() / ".codex")))
    session = event["thread-id"]
    log = max((root / "sessions").rglob(f"rollout-*-{session}.jsonl"),
              key=lambda path: path.stat().st_mtime, default=None)
    if log is None:
        return
    with log.open("rb") as stream:  # quota sits in the tail; logs grow large
        stream.seek(max(0, log.stat().st_size - 262144))
        lines = stream.read().splitlines()
    limits = None
    for line in reversed(lines):
        try:
            payload = json.loads(line).get("payload")
        except ValueError:
            continue
        if not isinstance(payload, dict) or payload.get("type") != "token_count":
            continue
        rate = payload.get("rate_limits")
        # Skip snapshots without windows (e.g. transient `premium` rows).
        if isinstance(rate, dict) and any(isinstance(rate.get(name), dict)
                                          for name in ("primary", "secondary")):
            limits = rate
            break
    if limits is None:
        return
    parts, fresh = [], []
    for name in ("primary", "secondary"):
        window = limits.get(name)
        if not isinstance(window, dict):
            continue
        used, reset = window.get("used_percent"), window.get("resets_at")
        if not isinstance(used, (int, float)) or not isinstance(reset, (int, float)):
            continue
        if used < 75 or reset < time.time():
            continue
        marker = STATE / f"{name}-{reset}"
        if marker.exists():
            continue  # already reminded for this window
        label = "5h" if window.get("window_minutes") == 300 else "weekly"
        parts.append(f"{label}: {round(100 - used):g}% left")
        fresh.append(marker)
    if not parts:
        return
    STATE.mkdir(parents=True, exist_ok=True)
    for marker in fresh:
        marker.touch()
    message = " · ".join(parts) + " · consider: catchup fork codex --into claude"
    subprocess.run(["notify-send", "Catchup handoff reminder", message],
                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=5)


if __name__ == "__main__":
    try:
        main()
    except Exception:
        pass  # an optional reminder must never disrupt the agent
