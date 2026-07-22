# Security Policy

## Supported versions

The `fuaran-go` module is pre-1.0. Security fixes are applied to the latest released `0.x` version on the
`main` branch. Older pre-releases are not maintained.

## Reporting a vulnerability

Please report suspected vulnerabilities privately — do **not** open a public issue.

- **Preferred:** GitHub's private vulnerability reporting (the repository's **Security** tab →
  **Report a vulnerability**).
- **Or email:** security@fuaran.com — include a description, the affected version, and steps
  to reproduce.

We aim to acknowledge a report within five business days and to agree a disclosure timeline with
you. Please allow a reasonable window to ship a fix before any public disclosure.

## Scope

This repo is the Go host of the Fuaran UI wire format: it decodes wire JSON — often
AI-emitted — and emits server-side HTML (static, islands partial-hydration, and server-driven).

- **Wire decoding:** a decode path that admits malformed wire as valid, or parser resource
  exhaustion (unbounded depth or size), is in scope.
- **Emitted-HTML injection safety:** tree content must never escape into markup as script or
  active content — a rendered tree that can inject HTML/JS through text, URLs (`href`/`src`
  scheme filtering), or attribute values is a vulnerability we want to hear about.
- **Server-driven driver:** the driver applies ops to server-held session trees — a crafted
  inbound event that mutates another session's tree or bypasses the op validator is in scope.
