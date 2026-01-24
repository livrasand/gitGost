# gitGost 👻

**Contribute to any GitHub repo without leaving a trace.**

Zero accounts • Zero tokens • Zero metadata • Designed for strong anonymity

[![License: AGPL-3.0](https://img.shields.io/badge/License-AGPL--3.0-blue.svg)](https://www.gnu.org/licenses/agpl-3.0)
[![Powered by Go](https://img.shields.io/badge/Powered%20by-Go-00ADD8.svg?logo=go)](https://go.dev)
[![Privacy First](https://img.shields.io/badge/Privacy-First-success)](https://github.com/livrasand/gitGost)

https://gitgost.leapcell.app

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

## Features

| Feature                     | Description                                                                 |
|-----------------------------|-----------------------------------------------------------------------------|
| **Total Anonymity**         | Strips author name, email, timestamps, and all identifying metadata. PRs created by neutral `@gitgost-anonymous` bot. |
| **One-Command Setup**       | Just `git remote add gost <url>` – no accounts, tokens, or browser extensions. |
| **Battle-tested Security**  | Rate limiting, repository size caps, commit validation. Written in pure Go with minimal dependencies – fully auditable. |
| **Works Everywhere**        | Terminal, CI/CD, Docker, scripts – any public GitHub repo, anywhere Git runs. |
| **Open Source & AGPL**      | 100% transparent. Fork it, audit it, host it yourself.                     |

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

## Threat Model

gitGost is designed to protect against common identification threats in contributions to public repos, but does not offer perfect anonymity. Below details what it protects against, what it does not, who it protects against, and key assumptions.

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

For more details, see [SECURITY.md](SECURITY.md).

## Hosted Configuration

gitGost requires a GitHub personal access token to create PRs. You can optionally configure Supabase for persistent statistics.

### 1. GitHub Token (Required)

Create a personal access token with `repo` permissions:
1. Go to [GitHub Settings > Developer settings > Personal access tokens](https://github.com/settings/tokens)
2. Click "Generate new token (classic)"
3. Select scopes: `repo` (full control of private repositories)
4. Copy the token

### 2. Environment Variables

Copy `.env.example` to `.env` and configure:

```bash
cp .env.example .env
# Edit .env with your actual values
```

**Required:**
- `GITHUB_TOKEN=your_github_token_here`

**Optional (for persistent stats):**
- `SUPABASE_URL=https://your-project.supabase.co`
- `SUPABASE_KEY=your_supabase_key_here`

### 3. Database Setup (Optional)

If you want persistent statistics instead of in-memory only:

1. Create a [Supabase](https://supabase.com) account
2. Create a new project in Central Europe (Zurich)
3. Go to SQL Editor and run the schema from `database/schema.sql`
4. Copy URL and anon key to your `.env`

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

Everything is designed to prevent abuse while keeping you anonymous.

Stats
-----

![Alt](https://repobeats.axiom.co/api/embed/1117c7e8a1ec122dd758a070c8d4d1cb2707141b.svg "Repobeats analytics image")

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

## Made with ❤️ for privacy

Star this repo if you believe developers deserve the right to contribute anonymously.

[![GitHub stars](https://img.shields.io/github/stars/livrasand/gitGost?style=social)](https://github.com/livrasand/gitGost/stargazers)
[![GitHub forks](https://img.shields.io/github/forks/livrasand/gitGost?style=social)](https://github.com/livrasand/gitGost/network/members)

**github.com/livrasand/gitGost**

Be a ghost. Fix the internet.

*✨ Thanks for visiting **gitGost**!*

<img src="https://visitor-badge.laobi.icu/badge?page_id=livrasand.gitGost&style=for-the-badge&color=00d4ff" alt="Views">
