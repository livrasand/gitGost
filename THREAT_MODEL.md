# gitGost – Threat Model (Public)

Level: serious / production. Aimed at operators and contributors seeking anonymity when creating PRs on GitHub via gitGost.

## Scope

- Push flow to `gitGost` and creation of anonymous PR on GitHub.
- Does not cover security of the user’s device or local network.

## Actors

- **GitHub**: platform hosting the target repositories.
- **gitGost operator**: runs the instance that receives pushes and creates PRs.
- **External attacker**: third party without control of the gitGost server or repository; may observe traffic, public repos, or attempt abuse.
- **Malicious maintainer**: maintains the target repo or collaborates with it to deanonymize contributions.

## Minimal assumptions

- The user uses a trustworthy network (VPN/Tor recommended) and a malware-free machine.
- The operator protects server logs and does not add hidden telemetry.
- Target repositories are public.

## Relevant attacks

- **Time correlation**: linking push/PR time with user activity.
- **Diff fingerprint**: identifying author by the pattern or content of the change.
- **PR size**: using size/shape of the change to infer author or project.
- **Commit style**: stylometry in messages or commit/PR format.

## What we mitigate

- **Separation of GitHub identity**: PRs come from `@gitgost-anonymous`, without linking to the user’s account.
- **Metadata stripping**: name, email, and commit timestamps are normalized before pushing.
- **Limits and sanitation**: repo/commit size caps, ref validation to reduce anomalous signals and abuse.
- **Reduce direct correlation**: the public PR time reflects the push to gitGost, not the local work time; buffers/queues are recommended for operators.

## What we do NOT mitigate (important)

- **Network / IP**: GitHub and the operator can see the source IP if you don’t use VPN/Tor.
- **Fine-grained time correlation**: an observer who sees your push and the near-simultaneous PR can link them if load is low or if no extra jitter is added.
- **Diff fingerprint**: code, wording, test patterns, or comments can reveal authorship.
- **PR size**: small or unique PRs can reveal you in low-activity projects.
- **Commit style**: language, format, or emojis in the message can be identifiable stylometry.
- **Private repos or hostile operators**: if the operator keeps logs or adds traces, they can identify you.
- **Platform attacks**: compromises in GitHub, MITM, or operator infrastructure are out of scope.

## Recommended practices

- Use Tor or a trustworthy VPN before pushing.
- Vary commit style and write neutral messages.
- Consider batching pushes or adding delays if the time-correlation risk is high.
- Avoid personal or internal references in the diff.

## Legal risk surface

This section addresses legal risks for the two parties most exposed by gitGost's design.

### Operator risks

The gitGost operator (the entity running a gitGost instance) acts as a technical intermediary. Relevant exposures:

- **Intermediary liability (EU DSA / MX LFDA):** Operators benefit from safe harbor only if they maintain an accessible infringement reporting channel and respond to valid notices within a reasonable timeframe. Running a gitGost instance without a functioning DMCA/IP complaint process may void this protection.
- **No content review:** Because gitGost does not review submissions before forwarding, operators cannot assert editorial knowledge of infringing content. This supports safe harbor claims but does not eliminate them entirely.
- **Recommended:** Expose a contact address for IP/copyright complaints, document a response SLA, and implement hash-blocking upon receipt of valid notices. The default gitGost instance does this; self-hosted instances must configure it independently.

### Maintainer (target repository) risks

Maintainers who accept PRs submitted via gitGost assume standard open-source contribution risk, with one additional consideration:

- **No identity chain:** There is no verified identity behind a gitGost PR. The submitter's declaration of original authorship (Terms of Submission) is a good-faith declaration, not a verified CLA. Maintainers accept this contribution under the same terms as any unverified unsigned patch.
- **Not suitable for CLA-required projects:** Projects that require identity-verified CLAs (Linux Foundation, Apache, Google, Mozilla, etc.) should not accept gitGost PRs without an out-of-band identity verification process. gitGost is designed for projects that accept unsigned contributions.
- **Supply chain audits:** Anonymous PRs have no chain of custody. Projects subject to supply chain security requirements (e.g., SLSA, SBOM mandates, corporate security policies) should reject anonymous contributions through any channel, including gitGost.
- **Recommended:** Projects that wish to signal acceptance of anonymous contributions can use the `anonymous-friendly` badge. Projects with strict CLA requirements should add a note in `CONTRIBUTING.md` that anonymous PRs are not accepted.

## Maturity statement

This model makes clear what is protected and what is not, positioning gitGost as a serious tool: it provides practical anonymity in public PRs but does not offer perfect anonymity against observers capable of correlation or advanced analysis.

gitGost is explicitly **not suitable** for contributions to projects requiring identity-verified CLAs or formal supply chain provenance. Its intended use case is personal and small open-source projects that accept informal contributions.
