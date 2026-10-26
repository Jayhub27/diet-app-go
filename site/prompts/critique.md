# Critique rubric

Paste this after a build, together with full-page and section screenshots at
1440 and 390 widths. Score honestly; most real pages land 24–32 / 40.

## A. Scored criteria (0–4 each, /40)

1. **Direction fidelity** — palette, type, spacing and signature match the
   `DESIGN.md`. No drift to a generic SaaS or AI-default look.
2. **Hierarchy** — one clear focal point per section; the primary action is the
   strongest control; headings, body, labels and metadata have distinct roles.
3. **Composition & rhythm** — asymmetry or tension where it helps; section
   density varies; no uniform card grid repeating the same shape.
4. **Typography** — real display↔body jump; 45–75ch measure; no orphans or bad
   wraps; one emphasis cue; tabular numbers where compared.
5. **Colour & contrast** — semantic roles; AA contrast on every surface; colour
   is never the only cue for state.
6. **Detail & alignment** — shared baselines/edges; even padding; no clipped or
   colliding text; consistent icon family, size and optical alignment.
7. **Copy honesty & clarity** — verifiable claims only; no invented stats,
   testimonials or logos; specific outcomes; one term per concept.
8. **Accessibility** — landmarks, heading order, focus-visible, labels, alt
   text, reduced motion, 44px targets, keyboard operability.
9. **Performance & SEO** — dimensions on images, lazy loading below the fold,
   font strategy, unique title/description, canonical, OG, JSON-LD.
10. **Distinctiveness** — one memorable signature element; could this page be
    swapped onto a competitor without noticing?

## B. Hard gates (any failure = NO-GO)

- [ ] Body text contrast < 4.5:1 anywhere
- [ ] Horizontal scroll at 360px, or clipped text
- [ ] Missing focus state on an interactive element
- [ ] Fabricated claim (stat, customer, testimonial, rating)
- [ ] Broken download/link or placeholder URL
- [ ] Image without dimensions, causing layout shift
- [ ] No `h1`, or skipped heading levels

## C. Severity routing

- **A0 — direction violation.** Off-contract palette/type, an anti-default tell,
  broken hierarchy. Blocks.
- **A1 — composition or detail failure.** Uniform card grids, no signature
  section, awkward line breaks, misaligned or misused icons, overflowing text.
  Blocks.
- **A2 — refinement.** Sub-perceptual spacing, measure, motion tuning. Advisory.
- **A3 — delight.** Optional polish.

## D. Output format

```
Scores: <criterion>: <0-4> (one line each) — total /40
Gates: <pass | fail + which>
Findings:
  [A0|A1] <location> — <issue> → <concrete fix>
  [A2|A3] <location> — <issue> → <fix>
Verdict: revise | approve
```

Rules:

- **Round 1 never approves.** If no A0/A1 exist, promote the 1–3 highest-value
  A2 items to blocking for one polish pass.
- **From round 2, approve as soon as A0/A1 are clear.** Do not introduce new
  nitpicks you let pass earlier.
- **Max 3 rounds.** If A0/A1 remain at the cap, ship with the findings recorded.
- Critique from **rendered screenshots**, section by section — never from CSS
  alone.
