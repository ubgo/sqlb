# Security Policy

## Reporting a vulnerability

**Please do not report security vulnerabilities through public GitHub issues.**

Instead, report them privately:

- Use **[GitHub Security Advisories](https://github.com/ubgo/sqlb/security/advisories/new)** (preferred), or
- Email **khanakia@gmail.com** with the details.

Please include:

- A description of the vulnerability and its impact.
- Steps to reproduce (proof-of-concept if possible).
- Affected version(s) and environment.

We will acknowledge your report within a few days and keep you updated on remediation. We ask that you give us a reasonable window to release a fix before any public disclosure, and we're happy to credit you in the advisory.

## Supported versions

Security fixes are applied to the latest released version. Older versions may not receive patches; please upgrade to stay supported.

| Version | Supported |
|---|---|
| latest `v0.x` | ✅ |
| any earlier `v0.x` | ❌ |

This module is pre-GA. Under SemVer a `v0.x` release may break, so the only supported version is the newest one; fixes are not backported to an earlier `v0.x`.

## Scope

The library builds SQL text and bound arguments. The security property it is responsible for is that a value which enters through `Param`, `Set`, `In` or a predicate leaves as a bound parameter and never as SQL text. A report showing request data reaching the statement string is in scope and treated as a vulnerability.

Out of scope: `Literal` and the `QuoteLit` helpers in the engine packages, which render their argument into the statement by design and are documented for author-chosen constants only. Passing request data to them is a caller error, not a library vulnerability. The same applies to a raw string handed to `ExecContext` around the builder.
