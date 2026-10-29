# Security policy

## Reporting a vulnerability

Please use GitHub's private vulnerability reporting:
https://github.com/Jayhub27/diet-app-go/security/advisories/new

Do not open a public issue for a suspected vulnerability. Include the affected
URL or version, reproduction steps, and impact if you can.

This is a small project without a paid bounty. Reports are read and answered as
time allows; valid issues are fixed in a patch release and credited unless you
ask otherwise.

## Scope

- https://diet-app-go.vercel.app (marketing site and downloads)
- DietApp Lite (the Go server and its static UI: `cmd/server`, `internal`,
  `static`)
- The DietApp Android client (`android/`)

The hosted web app at https://diet-app-zeta-blond.vercel.app is a separate
codebase and is out of scope here.

## Supported versions

Only the latest release and `master` receive fixes.
