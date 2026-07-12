# Versioning

KlainMainLang follows [Semantic Versioning](https://semver.org/) (`MAJOR.MINOR.PATCH`), with a project-specific policy for what counts as "major" before `1.0.0` is reached.

## The scheme

- **PATCH** (`0.1.0` → `0.1.1`): a bug fix — something that was broken now works correctly, no new capability added.
- **MINOR** (`0.1.0` → `0.2.0`): a new feature — new language support, a new builtin, a new CLI flag, anything additive.
- **MAJOR**: reserved for `1.0.0` itself (see criteria below), and — once past `1.0.0` — for an actual breaking change to already-shipped behavior.

This is standard SemVer (MINOR = backwards-compatible addition, PATCH = backwards-compatible fix), just spelled out in this project's own terms. It maps cleanly onto the existing "every feature and every bug fix gets its own ADR" rule (`docs/adr/README.md`) — one ADR, one version bump, one commit.

## Commit message convention

Version bumps are computed automatically from commit messages, using [Conventional Commits](https://www.conventionalcommits.org/):

| Prefix | Effect |
|---|---|
| `fix: <description>` | PATCH bump |
| `feat: <description>` | MINOR bump |
| `feat!: <description>` or a `BREAKING CHANGE:` footer | MAJOR bump (not expected before `1.0.0`) |
| `docs:`, `chore:`, `refactor:`, `test:`, ... | no release |

An ADR's title is a natural source for the commit description — e.g. an ADR titled "Implement setTimeout/clearTimeout/setInterval/clearInterval" becomes a commit message starting `feat: implement setTimeout/clearTimeout/setInterval/clearInterval`.

## 1.0.0 criteria (draft — expected to evolve)

Not met yet. Treat this list as living, not final:

- [ ] A handful of realistic, non-toy end-to-end programs (a real CLI tool, and — once an event loop + basic HTTP listening exist — a toy microservice) built and kept passing under `make examples`, not just single-feature demos.
- [ ] An event loop + basic network listening (HTTP server) — the biggest structural gap toward this project's own stated CLI/microservice goals.
- [ ] Some baseline hardening: no known crash-on-valid-input or silent-wrong-output bug left open in `STATUS.md`'s Known Limitations & Bugs section.
- [ ] A basic performance benchmark harness with numbers tracked release-over-release — doesn't need to be *fast*, needs to be *measured*, so a regression is visible instead of assumed.
- [ ] Core language coverage in `STATUS.md` above an agreed threshold (currently ~74%).

## First release, then automation

The very first commit is tagged `v0.1.0` by hand — a one-time manual step, since there's no prior tag for any tool to compute a delta from. From the second release-worthy commit onward, `.github/workflows/release.yml` runs [go-semantic-release](https://github.com/go-semantic-release/semantic-release) on every push to `main`: it reads commit messages since the last tag, computes the next version per the rules above, and creates the git tag + GitHub Release automatically. No manual `git tag` / `gh release create` needed again after that first bootstrap tag.

go-semantic-release (not the original JS `semantic-release`) was chosen specifically to keep the release pipeline Go-only — no Node.js/npm needed anywhere in CI, consistent with this project's own reason for being written in Go rather than a C/C++-toolchain language in the first place.

### Gotcha: `allow-initial-development-versions`

By default, go-semantic-release forces a MAJOR bump to `1.0.0` on the very next release once any tag exists, regardless of commit type — its `applyChange` logic explicitly does `if !allowInitialDevelopmentVersions && version.Major() == 0 { change.Major = true }`. `.github/workflows/release.yml` sets `allow-initial-development-versions: 'true'` on the `go-semantic-release/action@v1` step specifically to suppress this and stay on normal PATCH/MINOR bumps below `1.0.0`.

**This flag is itself ignored if a release tag with major version ≥ 1 already exists in the repo** (the tool's own documented behavior) — so if a premature `1.0.0` ever gets created again, setting this flag alone won't undo it; the offending tag/release has to actually be deleted first.
