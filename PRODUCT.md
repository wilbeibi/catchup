# PRODUCT.md — catchup.pages.dev

## What this is

Single-page marketing site for `catchup`, an open-source Go CLI that reads a
coding agent's local session history (Claude Code, Codex, Antigravity,
OpenCode, Pi Agent) and hands the conversation to the next agent. Static HTML
on Cloudflare Pages; the `site` branch is the deploy source (`just deploy`).

## Register

Brand / marketing. Design IS the product here; the page has one job: a
developer who just hit an agent usage limit lands, gets it in ten seconds,
copies the install command.

The spine follows the product's mental model, three jobs with a session:
**recap** (pull it back into context), **find** (list or search for the
right one), **hand off** (continue the work). The usage-limit scene is the
cold open, not the whole claim; the three jobs are three narrative beats,
not a feature-card grid. Keep it that way. This mirrors the `--help` /
SKILL.md RECAP / FIND / HAND OFF buckets; update the site when they change.

## Audience

Terminal-native developers who run two or more coding agents. They are
allergic to AI-generated marketing pages and hype copy. They respect
plainness, honesty, and stated limitations.

## Voice and aesthetic lane

**A 2000s network TV spot, delivered earnestly.** Think Get-a-Mac white
stage: a person talks to camera, the product sits on a white void, the fine
print scrolls at the bottom. Spokesperson copy in second person, short
sentences, one dry wink per section, honest disclaimers.

Named anti-lanes (do not drift back): editorial-parchment (serif + cream +
roman numerals, the previous design), terminal-dark cosplay, SaaS gradient.

- Type: Helvetica/Arial system stack (period-literal broadcast plainness);
  system mono for commands. No webfonts.
- Color: white stage, near-black ink, terminal-green `$`, one ochre accent.
  Dark end-card band for the close, like an ad end-card.
- Imagery: the two demo GIFs are vhs-scripted demos (label them as such; no "actual footage" claims); they sit on the stage
  with a floor shadow.
- Motion: staggered fade-up of the opening monologue lines; nothing else
  except a gentle reveal on the stage. Reduced-motion: everything static.

## Constraints

- Keep: SEO head, JSON-LD, llms.txt / index.md mirrors (update in lockstep
  with the page), copy-to-clipboard install, zero build step.
- Truthfulness: never claim cross-agent native-state transfer; `fork --into`
  carries a transcript. Boundaries copy comes from the README.
