# Privacy Guarantees

A short summary for paranoid folks: what we keep, what we don’t, and what happens in adverse scenarios.

## What is NOT stored
- Commits, blobs, or refs you send (they only stream through to GitHub).
- GitHub tokens or access keys.
- Your IP or logs containing it (logs only aggregated/rotated without IP).
- Emails, names, usernames, or SSH fingerprints.
- Request histories or session metadata.

## What IS stored (and why)
- Minimal aggregate metrics (e.g., PR count) for anti-abuse and service health.
- Ephemeral logs without IP or personal identifiers to debug operational failures.
- Optional Supabase config (if you enable it) for anonymous persistent statistics.

## Retention
- Aggregate metrics: short windows and automatic rotation.
- Ephemeral logs: aggressive rotation; no personal identifiers.
- Without Supabase: nothing persistent from traffic hits disk.

## If the server is compromised
- No stored tokens or user data to exfiltrate.
- Logs do not contain IPs or emails, so the attacker gains no identity.
- Deployment keys are rotated; traffic is cut and reinstalled from clean code.

## If there is a legal order
- We have no identity data to hand over (no IP, no accounts, no user tokens).
- We can only provide already-anonymized aggregate metrics.
- If future retention is required, it will be announced in changelog/policy before enabling.

## If the database is lost
- The service keeps running: it works in-memory streaming.
- Only optional aggregate metrics would be affected; no personal data to restore.

## What gitGost cannot know even if it wanted to
- It cannot know who you are (no accounts or user tokens).
- It cannot know your IP (it is not recorded).
- It cannot link commits to your real identity (author stripping + neutral PR bot).
- It cannot reconstruct your usage history: there are no persistent traces.

**Credibility > features:** less data, less risk.
