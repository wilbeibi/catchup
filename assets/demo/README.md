# Demo rig

Scripts that generate the README GIFs (`assets/handoff.gif`, `assets/recall.gif`).
Everything here runs on **synthetic data** — fake sessions about a made-up
auth-token bug, seeded under `/tmp/catchup-demo`. Nothing touches your real
`~/.claude`, `~/.codex`, or other agent history.

## Regenerate the GIFs

```bash
just demo          # or: bash assets/demo/render.sh
```

Requires [vhs](https://github.com/charmbracelet/vhs) and `tmux`.

## What's here

| File | Role |
|---|---|
| `seed.sh` | Writes the synthetic agent sessions into `/tmp/catchup-demo`. |
| `handoff.tape` / `recall.tape` | [vhs](https://github.com/charmbracelet/vhs) scripts that drive the recordings. |
| `handoff-split.sh` | Split-screen tmux layout for the handoff GIF. |
| `agents/` | Tiny shell stubs that mimic Claude Code / Codex / OpenCode output on screen. |
| `tmux.conf` | Minimal tmux config for a clean recording. |
| `render.sh` | Orchestrates seed + both tapes. |
