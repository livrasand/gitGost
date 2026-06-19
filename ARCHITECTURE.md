# gitGost Architecture & Data Flow

For a privacy-focused project, trust should never be based on marketing claims. Trust must be earned through technical transparency, reproducible systems, and explicit boundaries. 

This document explains exactly how gitGost works under the hood, how your data is processed, what is stored, and how you can independently verify that the system is doing exactly what it claims to do.

## High-Level Architecture

When you push code through gitGost, the system acts as a temporary proxy and transformation layer between your local machine and the GitHub API. 

```text
      User Machine
           │
           ▼
    git push gost
           │
           ▼
    gitGost Server (Fly.io)
           │
           ├─ 1. Validates target repository
           ├─ 2. Validates payload size
           ├─ 3. Strips all personal metadata
           ├─ 4. Creates a temporary fork
           ├─ 5. Pushes anonymized commits to fork
           ├─ 6. Opens Pull Request via Bot Account
           └─ 7. Deletes temporary fork (if applicable)
           │
           ▼
       GitHub API

```

## The Data Flow: What Happens During a Push?

To understand how anonymity is achieved, here is the exact lifecycle of a `git push` through our system:

1. **Reception:** You execute `git push gost`.
2. **Payload Parsing:** gitGost receives the raw Git objects (trees, blobs), your commit messages, and the target branch.
3. **Anonymization Engine:** Before anything touches GitHub, gitGost rewrites the Git history in memory. It actively **removes**:
* `Author Name`
* `Author Email`
* `Committer Name`
* `Committer Email`
* `Commit Timestamps` (Timezones and exact times are neutralized)
* Local Git configuration signatures


4. **Hash Generation:** gitGost generates a cryptographic, anonymous hash (HMAC-SHA256) to represent the session/user without tying it to an identity.
5. **Infrastructure Proxying:** gitGost creates a temporary fork of the target repository under the neutral `@gitgost-anonymous` bot account.
6. **Delivery:** The sanitized commits are pushed to the temporary fork, and a Pull Request is opened on the original target repository.

## Data Storage & Retention

We operate on a strict data-minimization principle. We cannot leak what we do not have.

| Data Point | Stored by gitGost? | Explanation |
| --- | --- | --- |
| **IP Address** | ❌ **No** | Dropped immediately after connection. Never logged or saved to the DB. |
| **Email Address** | ❌ **No** | Stripped from the commit payload before processing. |
| **Real Name** | ❌ **No** | Stripped from the commit payload before processing. |
| **Local Git Config** | ❌ **No** | Ignored and stripped during the push payload rewrite. |
| **Original Commits** | ❌ **No** | Only the anonymized, rewritten commits are pushed out. The originals are destroyed in memory. |
| **PR URL** | ✅ **Yes** | Saved to provide you with the link to your successful anonymous contribution. |
| **Anonymous Hash** | ✅ **Yes** | Stored to allow you to interact with your PR/Issue and to maintain the karma/moderation system. |
| **Aggregated Stats** | ✅ **Yes** | General counters (e.g., total anonymous PRs opened) are kept for project metrics. |

## Trust Boundaries & Limits

Anonymity is a spectrum, and gitGost is a tool, not a magic bullet. Here is where our responsibility ends and yours begins.

### What You MUST Trust

* **The Server Implementation:** You must trust that the server running on Fly.io is actually stripping the metadata as defined in the source code.
* **Build Integrity:** You must trust that the binary deployed matches the open-source repository (see *Reproducible Verification* below to eliminate this trust).

### What You Do NOT Need to Trust

* **GitHub:** GitHub only ever sees the `@gitgost-anonymous` bot account and its IP. They do not see your IP, your account, or your email.
* **Other Users / Target Repositories:** Maintainers receive a pristine PR with no metadata. They cannot trace it back to your GitHub profile.

### What Can Still Leak Your Identity (Residual Risks)

We sanitize the Git protocol, but we cannot sanitize your actual code. You can still expose yourself via:

1. **Programming Style:** Unique naming conventions, idiosyncratic formatting, or highly specific architectural choices.
2. **Contribution Timing:** If you only push at 2:00 AM UTC, patterns can be inferred over time.
3. **Network Layer Identity:** If you do not route your `git push` through Tor or a VPN, your ISP still knows you connected to the gitGost servers.
4. **File Contents & Binaries:** If you accidentally include your name in a comment, or leave EXIF/metadata inside an uploaded binary (like a `.pdf` or `.png`), gitGost will not catch it.

## Infrastructure Map

Our operational infrastructure is designed to be minimal and resilient:

```text
    Fly.io (Compute Layer)
       │
       └─ Runs the stateless Go binary. Memory-wiped frequently.

    GitHub App (Integration Layer)
       │
       └─ Handles authentication to fork repos and open PRs/Issues.

    Supabase / PostgreSQL (Persistence Layer)
       │
       └─ Stores cryptographic hashes, PR URLs, and Karma counts. No PII.

```

## Reproducible Verification (Don't Trust, Verify)

For gitGost to be truly trustworthy, you shouldn't have to take our word that the server is running the code in this repository. We utilize reproducible, signed builds so you can independently audit the deployed application.

```text
    GitHub Repository (Source)
          │
          ▼
    GitHub Actions (CI/CD)
          │
          ▼
    Signed Build (SLSA Provenance)
          │
          ▼
    Fly.io (Deployment)

```

**How to verify the deployment:**

1. **Check the Attestations:** Every release includes a cryptographic attestation mapping the compiled binary back to a specific commit hash in this repository.
2. **Download the Binary:** You can download the exact binary running on our servers from the Releases page.
3. **Compare Hashes:** Run `sha256sum gitgost-server` on the downloaded binary and compare it against the signed SHA256 checksums provided by the GitHub Actions build runner.
4. **Audit the Source:** Because the hashes match, you are mathematically guaranteed that the binary on the server was built exactly from the open-source code you can read here, with no hidden modifications or backdoors.
