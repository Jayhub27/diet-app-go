---
version: alpha
name: The Label
description: Packaging-studio direction for DietApp. Printed-label vernacular: ink on label stock, one emerald signal, one red warning, zero radius.
colors:
  paper: "#F2F4EE"
  surface: "#FCFBF4"
  ink: "#151811"
  ink-2: "#4A4F44"
  emerald: "#0B6B4F"
  red: "#B23A2E"
  sage: "#DCE4D6"
  kraft: "#E6D9BD"
typography:
  display: { fontFamily: Archivo, fontSize: 96px, fontWeight: 800, lineHeight: 0.94, letterSpacing: -0.032em }
  h2: { fontFamily: Archivo, fontSize: 56px, fontWeight: 800, lineHeight: 1.02, letterSpacing: -0.028em }
  body: { fontFamily: "Instrument Sans", fontSize: 16px, fontWeight: 400, lineHeight: 1.55 }
  data: { fontFamily: "IBM Plex Mono", fontSize: 11px, fontWeight: 400, letterSpacing: 0.12em }
rounded:
  none: 0px
components:
  button-primary:
    backgroundColor: "{colors.emerald}"
    textColor: "#FFFFFF"
    typography: "{typography.data}"
    rounded: "{rounded.none}"
    padding: 13px 20px
  button-secondary:
    backgroundColor: "{colors.paper}"
    textColor: "{colors.ink}"
    typography: "{typography.data}"
    rounded: "{rounded.none}"
    padding: 13px 20px
  label-panel:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.ink}"
    rounded: "{rounded.none}"
    padding: 24px 26px
---

## Overview

A landing page that behaves like the object it is about: a printed food label.
For people with allergies, intolerances and medical diets who already read every
label twice. The page's job is to make the check visible — not to look healthy.

## Colors

Paper is label stock, slightly cool, never cream. Ink is near-black with a green
cast. Emerald is the only action colour. Red is reserved for the "no / unsafe"
signal and never decorates. Sage and kraft are material accents: the panel
shadow and the screenshot frame.

## Typography

Archivo 800 at display sizes, uppercase, tight but not touching (−0.032em).
Instrument Sans for body. IBM Plex Mono for data, labels and buttons. The mono
is the label voice; do not use it for paragraphs.

## Layout

12-column grid inside a 1200px shell. Sections separated by 1px ink rules.
Asymmetry: hero copy 7 columns, label panel 5. Dotted leaders for label rows.
Numbers and values align right on the mono grid.

## Elevation & Depth

No soft shadows. One hard offset (8px sage) behind the label panel, like a
second sheet of paper. Screenshots sit in a 2px ink frame with a hard kraft
offset.

## Shapes

Zero radius everywhere. No rounded chips; status is a square swatch or a stamp.

## Do's and Don'ts

**Do** let type be the image; use rules and dotted leaders; keep one emerald and
one red; set metadata in mono; repeat the label-panel logic in other sections.

**Don't** add rounded cards, soft shadows, gradients, icon tiles, eyebrow chips,
more than one accent family, or decorative numbered markers.

## Motion

None beyond a 150ms colour swap on buttons. Respect reduced motion.
