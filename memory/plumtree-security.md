---
name: plumtree-security
description: Plumtree security model decisions — public-only apps, SSRF egress guard, build sandbox limits
metadata:
  type: project
---

Security review of Plumtree (2026-06-25) with the owner. Plumtree is a public,
multi-tenant host for WASM-sandboxed terminal apps; see [[plumtree-platform]].

Decisions and changes made:

- **No private apps.** The server is intentionally public; users who need private
  apps self-host. The `Visibility`/private/public concept was deleted entirely
  (control `types`/`store`/`validation`/`persistence`/`httpapi`, `pt deploy`
  `--visibility` flag, dashboard column). Every app is public.

- **Egress SSRF guard.** `runner.AllowlistFetcher` now blocks non-public IPs
  (loopback/private/link-local incl. 169.254.169.254 metadata/ULA/unspecified/
  multicast) on every dial AND every redirect hop, via `net.Dialer.Control` +
  `CheckRedirect` — defeats DNS rebinding and redirect SSRF. `AllowPrivateIPs`
  bool opts out (tests/self-host loopback only). `control.ValidateEgressHost`
  rejects IP literals and internal/single-label names at `pt egress add` time.

- **Build worker hardening.** `enforceModulePolicy` now rejects `replace` and
  `exclude` go.mod directives (replace can redirect an allowlisted module or pull
  host files via //go:embed). Linux build process gets RLIMIT_AS (2 GiB default,
  `--max-memory-bytes`) + RLIMIT_CPU via prlimit (`proc_linux.go`, raw
  SYS_PRLIMIT64, no new deps). **Production still must run the build-worker in a
  no-network container with a tmpfs work dir** — OS-level netns isolation was not
  added in-code (would need userns and risks breaking builds).

Still open (flagged, not done): plaintext dev-token compared non-constant-time in
`authorizeDevDeploy`; CLI `controlFilter` passes C1 bytes (TUI sink already
sanitizes them).
