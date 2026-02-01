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

## Maturity statement

This model makes clear what is protected and what is not, positioning gitGost as a serious tool: it provides practical anonymity in public PRs but does not offer perfect anonymity against observers capable of correlation or advanced analysis.
