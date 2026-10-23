# Security policy

## Supported versions

Only the latest release (the `release` branch) is supported. Fixes are published as a new patch
version; older versions get no backports.

## Reporting a vulnerability

Report privately through GitHub: <https://github.com/Comprise/auto-context/security/advisories/new>
(Security tab, "Report a vulnerability"). Please do not open a public issue.

Include the plugin version (`auto-context version -v`), the OS, a minimal repository or steps that
reproduce the problem, and the impact you see.

## What to expect

- Acknowledgement within 3 working days.
- An assessment and a decision within 14 days.
- A fix for a confirmed vulnerability as a hotfix release (see `docs/RELEASING.md`), normally within
  30 days, then a published GitHub security advisory (GHSA, with a CVE when it applies) crediting the
  reporter unless asked otherwise.

## Scope

auto-context runs by itself in hooks on repositories you may not trust (a cloned archive with a
hostile `.git/config`, hostile file and directory names, oversized files). Problems in how it
launches git, reads or writes paths, or passes repository text to the agent are in scope. The threat
model is in `context/_root.md`.
