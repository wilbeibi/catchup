#!/usr/bin/env python3
"""Optional Unix quota reminder. No network requests or background process."""
import fcntl
import hashlib
import json
import math
import os
import shlex
from pathlib import Path
import subprocess
import sys
import time
import uuid


def codex_limits(event):
    if event.get('type') != 'agent-turn-complete':
        return None
    try:
        session = str(uuid.UUID(event['thread-id']))
    except (KeyError, ValueError):
        return None
    root = Path(os.environ.get('CODEX_HOME', str(Path.home() / '.codex')))
    paths = list((root / 'sessions').rglob(f'rollout-*-{session}.jsonl'))
    if not paths:
        return None
    paths.sort(key=lambda path: path.stat().st_mtime, reverse=True)
    # Bound per-turn I/O. A missing quota record means no reminder this turn.
    # Rows without windows (for example a `premium` limit snapshot with
    # null windows) are skipped, not terminal: the newest usable row wins.
    with paths[0].open('rb') as stream:
        offset = max(0, paths[0].stat().st_size - 262144)
        stream.seek(offset)
        if offset:
            stream.readline()
        lines = stream.read(262144).splitlines()
    for line in reversed(lines):
        try:
            row = json.loads(line)
        except ValueError:
            continue
        payload = row.get('payload', {})
        if row.get('type') == 'event_msg' and payload.get('type') == 'token_count':
            limits = payload.get('rate_limits')
            if isinstance(limits, dict) and (isinstance(limits.get('primary'), dict)
                                             or isinstance(limits.get('secondary'), dict)):
                return session, limits
    return None


def warning_windows(limits, agent, now):
    names = ('five_hour', 'seven_day', 'spend_limit') if agent == 'claude' else ('primary', 'secondary')
    percent_key = 'used_percentage' if agent == 'claude' else 'used_percent'
    result = {}
    for name in names:
        window = limits.get(name)
        if not isinstance(window, dict):
            continue
        used, reset = window.get(percent_key), window.get('resets_at')
        if not all(isinstance(v, (int, float)) and not isinstance(v, bool) and math.isfinite(v)
                   for v in (used, reset)):
            continue
        # Spend limits report above 100 once exceeded, so only floor the
        # threshold; percentage fields above 100 still mean "time to go".
        if used >= 75 and reset > now:
            label = {'five_hour': '5h', 'seven_day': 'weekly', 'spend_limit': 'spend'}.get(name, name)
            if agent == 'codex':
                label = {300: '5h', 10080: 'weekly'}.get(window.get('window_minutes'), name)
            result[name] = (int(reset), f'{label}: {100 - used:g}% left' if used <= 100
                            else f'{label}: exceeded')
    return result


def notify_once(agent, session, windows, message):
    base = Path(os.environ.get('XDG_STATE_HOME', str(Path.home() / '.local/state')))
    state_dir = base / 'catchup-quota'
    state_dir.mkdir(parents=True, exist_ok=True, mode=0o700)
    key = hashlib.sha256(f'{agent}:{session}'.encode()).hexdigest()
    fd = os.open(state_dir / f'{key}.json', os.O_RDWR | os.O_CREAT, 0o600)
    with os.fdopen(fd, 'r+') as stream:
        fcntl.flock(stream, fcntl.LOCK_EX)
        stream.seek(0)
        try:
            sent = json.load(stream)
        except ValueError:
            sent = {}
        if not isinstance(sent, dict):
            sent = {}
        if all(sent.get(name) == reset for name, (reset, _) in windows.items()):
            return
        # Override the notifier; the value is shell-word split, then the
        # title and body are appended as the final two arguments.
        command = shlex.split(os.environ.get('CATCHUP_QUOTA_NOTIFY', 'notify-send'))
        subprocess.run(command + ['Catchup handoff reminder', message], check=True,
                       stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=5)
        sent.update({name: reset for name, (reset, _) in windows.items()})
        stream.seek(0)
        stream.truncate()
        json.dump(sent, stream)


def main():
    agent = sys.argv[1]
    if agent == 'claude':
        event = json.load(sys.stdin)
        session, limits = event.get('session_id'), event.get('rate_limits')
    elif agent == 'codex':
        if len(sys.argv) < 3:
            return
        result = codex_limits(json.loads(sys.argv[2]))
        if result is None:
            return
        session, limits = result
    else:
        return
    if not session or not isinstance(limits, dict):
        return
    windows = warning_windows(limits, agent, time.time())
    if not windows:
        return
    target = 'codex' if agent == 'claude' else 'claude'
    message = ' · '.join(text for _, text in windows.values())
    message += f' · consider: catchup fork {agent} --into {target}'
    if agent == 'claude':
        print(message)
    if agent == 'codex' or os.environ.get('CATCHUP_QUOTA_DESKTOP') == '1':
        notify_once(agent, session, windows, message)


if __name__ == '__main__':
    try:
        main()
    except (OSError, ValueError, KeyError, TypeError, AttributeError, IndexError, subprocess.SubprocessError):
        # Optional UI integration must not disrupt the coding agent.
        pass
