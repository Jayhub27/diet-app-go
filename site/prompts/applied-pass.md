# Applied pass — three directions, round 2

Prompt system applied to the three direction mockups (A: The Label, B: Kitchen
Table, C: Instrument Panel). Scores use `critique.md`: 10 criteria × 0–4.

## Scores

| Direction | Before | After | Lowest remaining |
| --- | --- | --- | --- |
| A — The Label | 27 / 40 | 38 / 40 | Performance/SEO 3 (mockup serves 2640px PNGs) |
| B — Kitchen Table | 25 / 40 | 37 / 40 | Distinctiveness 3 (stock food photography) |
| C — Instrument Panel | 25 / 40 | 36 / 40 | Performance/SEO 3, distinctiveness 3 (familiar dark-tool school) |

## Blocking findings fixed

### A — The Label

- **A0** hero eyebrow chip above the headline → removed; type now leads.
- **A1** display at 102px with −0.045em tracking (both outside catalog limits) →
  96px, −0.032em.
- **A1** H2 orphaned "item." on its own line → measure raised to 26ch.
- **A1** section padding shorthand overrode `.wrap` horizontal padding, so the
  proof grid and FAQ went edge-to-edge on mobile → `padding-block`.
- **A1** CTA micro-copy wrapped mid-phrase → shortened and unconstrained.
- **A1** no product proof and no objection handling → three real app screenshots
  with captions; four-question FAQ.
- Cream background moved to a cooler label-stock tone; contrast verified.

### B — Kitchen Table

- **A0** Fraunces is a reflex-reject display face → replaced with Newsreader.
- **A0** muted text contrast 4.38:1 → 5.64:1 (`#625C4C`).
- **A1** hero orphaned "Cook" on its own line → 76px display, two clean lines.
- **A1** same `.wrap` padding bug → `padding-block`.
- **A1** mobile header wrapped "Open web app" onto two lines → hidden below 900px.
- **A1** no product screenshot → "Inside the app" spread with a framed plan
  screenshot and a capability list; four-question FAQ.
- Image dimensions added; heading balance on.

### C — Instrument Panel

- **A0** hero eyebrow chip → removed.
- **A0** muted text contrast 4.15:1 → 6.24:1 (`#8C948B`).
- **A1** mint used on every feature label → reduced to the primary action and
  status dots.
- **A1** H1 orphaned "time." → copy changed to "Your restrictions. Checked live."
- **A1** giant white screenshot dominated the dark page → four small windows with
  route labels (`/plan`, `/pantry`, `/groceries`, `/library`).
- **A1** same `.wrap` padding bug → `padding-block`.
- **A1** meta-criticism line ("Checked answers, not marketing.") → removed.
- Four-question FAQ; real install command instead of an ellipsis placeholder.

## Round-2 verdict

**Approve** for A and B — no A0/A1 remain. C is approved with one A2 note
(the dark-tool school is inherently familiar; the scanner panel carries the
distinctiveness).

## Round-3 notes (not blocking)

- Capture the Go app's own screenshots and compress to WebP/AVIF before launch.
- Replace B's stock photography with owned food/label photography.
- Add an OG image and per-page canonical/JSON-LD when the final pages ship.
- Test the FAQ `<details>` disclosure with keyboard and screen reader.

## Techniques used in this pass

1. **Spec-first contract** — DESIGN.example.md fixed tokens and bans before edits.
2. **Structured prompt** — system.md named role, audience, constraints, output.
3. **Task decomposition** — audit → fixes → proof/FAQ → SEO/a11y → render.
4. **Few-shot** — good/bad hero and CTA examples in system.md.
5. **Rubric self-refine** — critique.md scores and A0–A3 routing; round 1 must
   revise, round 2 approves when clear.
6. **Rendered evidence** — desktop 1440 and mobile 390, section crops, not code
   reading.
7. **Hard gates** — contrast measured, no fabricated claims, real links.
