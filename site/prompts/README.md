# Website prompt system

A small, versioned prompt kit for building marketing sites with an LLM. It turns
"make it look good" into a brief → contract → build → critique → revise loop.

## Why these files exist

| Technique | Where it lives |
| --- | --- |
| Spec-first / contract prompting | `DESIGN.example.md` (tokens + Do's and Don'ts) |
| Structured prompt anatomy (role, context, format, constraints, examples) | `system.md` |
| Task decomposition (brief → tokens → structure → build → review) | this README + `system.md` |
| Few-shot with positive and negative examples | `system.md` § Examples |
| Rubric-based self-refine | `critique.md` |
| Hard acceptance gates (a11y, contrast, honesty, SEO) | `critique.md` § Gates |
| Context hygiene (one direction, no contradictory docs) | `system.md` § Rules of engagement |
| Structured output (file list + token delta) | `system.md` § Output contract |

## The loop

```
1. Brief        five lines: user, problem, core action, metric, non-goals
2. Contract     copy DESIGN.example.md, fill tokens + prose for this brand
3. Structure    choose sections from the catalog; write copy before markup
4. Build        one page at a time; tokens only, no off-scale values
5. Critique     run critique.md against rendered screenshots (desktop + mobile)
6. Revise       fix every A0/A1; re-render; max 3 rounds
7. Ship         deploy; verify links, contrast, and the acceptance checklist
```

## Using it with an agent

Paste `system.md`, then the filled `DESIGN.md`, then the page request. After the
build, paste `critique.md` plus the screenshots. Do not let the agent critique
from code alone — the review is visual.

## Rules that matter most

1. **One direction.** Never mix two visual schools in one prompt; state the
   anchor and the anti-defaults explicitly.
2. **Content before chrome.** Real copy, real screenshots, real numbers. No
   invented testimonials or usage stats.
3. **Rubric, not vibes.** Ask for named criteria and severities, not "improve
   this".
4. **Render before judging.** Desktop 1440 and mobile 390; section crops.
5. **Converge.** Round 1 always revises; approve from round 2 when no A0/A1
   remain.
