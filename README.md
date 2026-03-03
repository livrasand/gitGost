![gitGost Logo Light](web/assets/logos/light-logo.png#gh-light-mode-only)
![gitGost Logo Dark](web/assets/logos/dark-logo.png#gh-dark-mode-only)


**Contribute to any GitHub repo without leaving a trace.**

Zero accounts • Zero tokens • Zero metadata • Designed for strong anonymity

## One-liner demo

```bash
# Add as remote → fix → push → done. Fully anonymous.
git remote add gost https://gitgost.leapcell.app/v1/gh/torvalds/linux
git checkout -b fix-typo
git commit -am "fix: obvious typo in README"
git push gost fix-typo:main
# → PR opened as @gitgost-anonymous with zero trace to you
```

That’s it. No login. No token. No name. No email. No history.

[![Ask DeepWiki](https://deepwiki.com/badge.svg)](https://deepwiki.com/livrasand/gitGost)
[![Desplegado](https://gitgost.leapcell.app/badges/deployed.svg)](https://gitgost.leapcell.app/health)
[![Say Thanks!](https://img.shields.io/badge/Say%20Thanks-!-1EAEDB.svg)](https://saythanks.io/to/livrasand)
[![License: AGPL-3.0](https://img.shields.io/badge/License-AGPL--3.0-blue.svg)](https://www.gnu.org/licenses/agpl-3.0)
[![Security Responsible Disclosure](https://img.shields.io/badge/Security-Responsible%20Disclosure-yellow)](SECURITY.md)
[![Legal Notice](https://img.shields.io/badge/Legal-Notice%20%26%20CLA-red)](LEGAL.md)
[![Powered by Go](https://img.shields.io/badge/Powered%20by-Go-00ADD8.svg?logo=go)](https://go.dev)
[![Privacy First](https://img.shields.io/badge/Privacy-First-59316b)](https://github.com/livrasand/gitGost)
[![GitHub repo size](https://img.shields.io/github/repo-size/livrasand/gitGost)](https://github.com/EduardaSRBastos/my-essential-toolbox)
  <img
  src="https://img.shields.io/badge/GitHub-Available-brightgreen?logo=github"
  alt="GitHub – Coming Soon"/>
<img
  src="https://img.shields.io/badge/GitLab-Coming%20Soon-lightgrey?logo=gitlab&logoColor=white"
  alt="GitLab – Coming Soon"/>
<img
  src="https://img.shields.io/badge/Bitbucket-Coming%20Soon-lightgrey?logo=bitbucket"
  alt="Bitbucket – Coming Soon"/>
<img
  src="https://img.shields.io/badge/Codeberg-Coming%20Soon-lightgrey?logo=codeberg&logoColor=white"
  alt="Codeberg – Coming Soon"/>

<br />

> "Fixed GPIO mapping bug in 10s without doxxing risk – @gitgost-anonymous"  
> [View PR ↗](https://github.com/mehdi7129/inky-photo-frame/pull/3) *(Example from mehdi7129/inky-photo-frame)*

<br />

<a href="https://leapcell.io?utm_source=github_readme_stats_team&utm_campaign=gitGost">
<picture>
  <source media="(prefers-color-scheme: light)" srcset="./web/assets/logos/powered-by-leapcell.svg">
  <source media="(prefers-color-scheme: dark)" srcset="./web/assets/logos/powered-by-leapcell-dark.svg">
  <img src="./web/assets/logos/powered-by-leapcell.svg" alt="Powered by Leapcell">
</picture>
</a>
<a href="https://www.producthunt.com/products/gitgost-anonymous-git-contributions?embed=true&amp;utm_source=badge-featured&amp;utm_medium=badge&amp;utm_campaign=badge-gitgost-anonymous-git-contributions" target="_blank" rel="noopener noreferrer">
<picture>
<source media="(prefers-color-scheme: light)" srcset="https://api.producthunt.com/widgets/embed-image/v1/featured.svg?post_id=1088722&amp;theme=light&amp;t=1772487442890">
<source media="(prefers-color-scheme: dark)" srcset="https://api.producthunt.com/widgets/embed-image/v1/featured.svg?post_id=1088722&amp;theme=dark&amp;t=1772487608313">
<img alt="gitGost — Anonymous Git Contributions - Contribute to any GitHub repo without leaving a trace. | Product Hunt" width="200" height="44" src="https://api.producthunt.com/widgets/embed-image/v1/featured.svg?post_id=1088722&amp;theme=light&amp;t=1772487442890">
</picture>
</a>

## Features

| Feature                     | Description                                                                 |
|-----------------------------|-----------------------------------------------------------------------------|
| **Total Anonymity**         | Strips author name, email, timestamps, and all identifying metadata. PRs created by neutral `@gitgost-anonymous` bot. |
| **One-Command Setup**       | Just `git remote add gost <url>` – no accounts, tokens, or browser extensions. |
| **Battle-tested Security**  | Rate limiting, repository size caps, commit validation. Written in pure Go with minimal dependencies – fully auditable. |
| **Works Everywhere**        | Terminal, CI/CD, Docker, scripts – any public GitHub repo, anywhere Git runs. |
| **Open Source & AGPL**      | 100% transparent. Fork it, audit it, host it yourself.                     |

## Comparison with Alternatives

| Feature       | gitGost                          | GitHub CLI                       | Forgejo                           |
|---------------|----------------------------------|----------------------------------|----------------------------------|
| **Anonymity** | ✅ **Full** - Strips all metadata, uses neutral bot | ❌ **None** - Requires account, full traceability | ⚠️ **Partial** - Depends on instance, typically requires account |
| **Setup**     | ✅ **One command** - `git remote add gost <url>` | ❌ **Complex** - Install CLI, authenticate | ❌ **Self-hosting** - Set up instance, manage accounts |
| **Providers** | ✅ **Multi** - GitHub + planned GitLab/Bitbucket | ⚠️ **Single** - GitHub only | ✅ **Any** - Self-hosted, unlimited instances |
| **Limits**    | ⚠️ **Reasonable** - 5 PRs/IP/hr, 500MB repo, 10MB commit | ⚠️ **API limits** - GitHub rate limits | ⚠️ **Instance-dependent** - Varies by host |

*Legend: ✅ Superior • ⚠️ Acceptable • ❌ Inferior*

## Anonymous Contributor Friendly Badge

To signal that your repository welcomes anonymous contributions via gitGost, add this badge to your README:

![Anonymous Contributor Friendly](https://gitgost.leapcell.app/badges/anonymous-friendly.svg)


For verified repositories, add a `.gitgost.yml` file to your repository root and use the dynamic version:

![Anonymous Contributor Friendly](https://gitgost.leapcell.app/badges/anonymous-friendly.svg?repo=livrasand%2FgitGost)

This badge helps contributors know that anonymous contributions are accepted and encouraged.

## Why developers love gitGost

> “Your commit history shouldn’t be an HR liability forever.”

- No permanent public record of your activity  
- Safely contribute to controversial projects (employer or country doesn’t like it? no problem)  
- Stop email harvesting & doxxing from public commits  
- Fix that one annoying typo without attaching your name for eternity  
- Be a ghost when you want to be

Built for developers who actually care about privacy.

## Legitimate Use Cases

gitGost is intended for responsible, good-faith contributions where identity exposure is unnecessary or undesirable.

Examples include:

Fixing typos or documentation errors without creating a permanent contribution record
Contributing to projects that may conflict with employer policies
Participating in politically sensitive or controversial repositories
Reducing exposure to email harvesting and scraping
Experimenting or testing changes without attaching personal metadata
Contributing from jurisdictions where visibility may create risk

gitGost is designed to enable privacy — not remove accountability from the review process.

All pull requests are public and subject to maintainer approval.

## When NOT to use gitGost

Do not use gitGost for:

Harassment or abuse
Spam or automated PR flooding
Evading bans or moderation
Submitting malicious code
Avoiding legal responsibility
Circumventing repository contribution policies

gitGost enforces rate limits, validation checks, and repository constraints. Abuse attempts will be mitigated.

If your goal is to harm, disrupt, or deceive — this project is not for you.

## Threat Model

gitGost is designed to protect against common identification threats in contributions to public repos, but does not offer perfect anonymity. Below details what it protects against, what it does not, who it protects against, and key assumptions.

For a terse, user-facing view of guarantees and data retention, see [Privacy Guarantees](Privacy%20Guarantees.md).

### gitGost protects against:

- Public exposure of name and email in commits
- Direct association between personal GitHub account and PR
- Passive metadata collection in public repos
- Permanent history of minor contributions

### gitGost does NOT protect against:

- IP identification (using VPN/Tor is recommended)
- Code style analysis (stylometry)
- Advanced temporal correlation
- Targeted deanonymization by adversaries with resources

### Considered adversaries

- Recruiters / HR
- Hostile maintainers
- Email scrapers
- Governments or companies with basic monitoring

### Not considered adversaries

- Nation states with infrastructure access
- Actors with active user surveillance
- Deep forensic code style analysis

### Explicit assumptions

gitGost assumes the user:

- Uses a trustworthy network (VPN / Tor)
- Does not reuse unique phrases or identifiable style
- Does not mix anonymous and personal contributions to the same repo
- Understands that perfect anonymity does not exist

For the full model, see [THREAT_MODEL.md](THREAT_MODEL.md). For more operational details, see [SECURITY.md](SECURITY.md).

## Quick Start

```bash
# 1. Add the remote (replace with any public repo)
git remote add gost https://gitgost.leapcell.app/v1/gh/username/repo

# 2. Create your branch and commit with a detailed message
git checkout -b my-cool-fix
git commit -am "fix: typo in documentation

This commit fixes a grammatical error in the README.
The word 'recieve' should be 'receive'."

# 3. Push – PR opens anonymously
git push gost my-cool-fix:main
```

Done. The PR appears instantly from `@gitgost-anonymous` with your commit message as the PR description.

**Pro tip:** Write detailed commit messages! Your commit message becomes the PR description, allowing you to provide context while staying anonymous.

## Security & Limits (we’re not reckless)

- Max 5 PRs/IP/hour
- Repository size ≤ 500 MB
- Commit size ≤ 10 MB
- Full validation of refs and objects
- No persistence of your data

> **GitHub only:** Due to GitHub's platform limits, fork repositories created by gitGost are manually deleted to stay under the 40,000-repository cap. This is a GitHub-specific constraint and does not affect functionality.

Everything is designed to prevent abuse while keeping you anonymous.

## License

**AGPL-3.0** – Free forever, open source, and copyleft.  
If you run a public instance, you must provide source code.

→ [LICENSE](LICENSE)

## Contributing

### Contributing Anonymously

```bash
git remote add gost https://gitgost.leapcell.app/v1/gh/livrasand/gitGost
git push gost my-feature:main
```

(Yes, even gitGost eats its own dogfood 👻)

### Going further: hide your IP with torsocks

gitGost strips your name, email, and metadata — but your IP is still visible to the server. If you need a stronger anonymity guarantee, wrap your push with **torsocks**, which routes the connection through the Tor network so the server only sees a Tor exit node IP.

#### Install

```bash
# Debian/Ubuntu
sudo apt install tor torsocks

# Arch
sudo pacman -S tor torsocks

# macOS
brew install tor torsocks
```

#### Start Tor

```bash
sudo systemctl start tor   # Linux
brew services start tor    # macOS
```

#### Push through Tor

```bash
torsocks git \
  -c http.extraHeader="X-Gost-Authorship-Confirmed: 1" \
  push gost my-feature:main
```

#### Optional: persistent alias so you never forget

```bash
# Inside your repo
git config http.extraHeader "X-Gost-Authorship-Confirmed: 1"

# In ~/.gitconfig
[alias]
    ghost = "!torsocks git"
```

Then simply:

```bash
git ghost push gost my-feature:main
```

#### Verify your IP is masked before pushing

```bash
torsocks curl https://check.torproject.org/api/ip
# → {"IsTor": true, "IP": "185.220.101.x"}
```

> **Heads-up:** Tor is slow. A push that normally takes seconds may take a few minutes. This is expected — Tor routes traffic through three encrypted nodes worldwide. gitGost's 10 MB commit limit is partly sized with this in mind.

### Windows alternatives

`torsocks` is not available on Windows natively. Use one of the following options instead.

#### Option 1: Tor Browser + SOCKS5 proxy (easiest)

```bash
# 1. Download and install Tor Browser
#    https://www.torproject.org/download/

# 2. Open it and leave it running (exposes SOCKS5 on 127.0.0.1:9150)

# 3. Configure Git to use it
git config --global http.proxy socks5h://127.0.0.1:9150

# 4. Push normally
git -c http.extraHeader="X-Gost-Authorship-Confirmed: 1" push gost my-branch:main
```

When done, remove the global proxy:

```bash
git config --global --unset http.proxy
```

Or configure it per-repo only (recommended):

```bash
# Inside the repo, not global
git config http.proxy socks5h://127.0.0.1:9150
git config http.extraHeader "X-Gost-Authorship-Confirmed: 1"
```

#### Option 2: WSL2 (Windows Subsystem for Linux)

If you already have WSL2, it works exactly like Linux inside it:

```bash
# Inside WSL2 (Ubuntu/Debian)
sudo apt install tor torsocks
sudo service tor start

torsocks git push gost my-branch:main
```

WSL2 has its own network stack separate from Windows, so anonymity is preserved correctly.

## Made with ❤️ for privacy

Star this repo if you believe developers deserve the right to contribute anonymously.

[![Share](https://img.shields.io/badge/share-000000?logo=x&logoColor=white)](https://x.com/intent/tweet?text=Check%20out%20this%20project%20on%20GitHub:%20https://github.com/livrasand/gitGost%20%23gitGost%20%23anonymous%20%23privacy)
[![Share](https://img.shields.io/badge/share-1877F2?logo=facebook&logoColor=white)](https://www.facebook.com/sharer/sharer.php?u=https://github.com/livrasand/gitGost)
[![Share](https://img.shields.io/badge/share-0A66C2?logo=linkedin&logoColor=white)](https://www.linkedin.com/sharing/share-offsite/?url=https://github.com/livrasand/gitGost)
[![Share](https://img.shields.io/badge/share-FF4500?logo=reddit&logoColor=white)](https://www.reddit.com/submit?title=Check%20out%20this%20project%20on%20GitHub:%20https://github.com/livrasand/gitGost)
[![Share](https://img.shields.io/badge/share-0088CC?logo=telegram&logoColor=white)](https://t.me/share/url?url=https://github.com/livrasand/gitGost&text=Check%20out%20this%20project%20on%20GitHub)

Be a ghost. Fix the internet.

*✨ Thanks for visiting **gitGost**!*

<img src="https://visitor-badge.laobi.icu/badge?page_id=livrasand.gitGost&style=for-the-badge&color=00d4ff" alt="Views">
