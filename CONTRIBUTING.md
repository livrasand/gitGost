# Contributing to gitGost

Thank you for your interest in contributing to **gitGost**.

gitGost is an open-source, privacy-focused Git proxy and client that aims to make working with GitHub, GitLab, and Codeberg possible without unnecessarily exposing a contributor's identity.

Contributions of all sizes are welcome, including bug fixes, documentation improvements, tests, accessibility improvements, performance improvements, and new features.

## Before You Start

Before opening an issue or pull request:

1. Search existing issues and pull requests for duplicates.
2. Read the relevant documentation.
3. For security vulnerabilities, **do not open a public issue**. Follow the instructions in [`SECURITY.md`](./SECURITY.md).
4. For questions or ideas that are not yet actionable bugs or feature requests, consider starting a discussion first.

---

## Development Environment

gitGost is primarily written in **Go**, with web and Android components.

### Prerequisites

* Go 1.26 or newer
* Git
* Node.js and npm for web/frontend development
* Android Studio and the Android SDK for Android development

Check the repository and package configuration for the exact versions required by the component you are working on.

### Clone the repository

```bash
git clone https://github.com/livrasand/gitGost.git
cd gitGost
```

Before submitting a PR, make sure your changes do not introduce formatting changes unrelated to your work.

---

## Project Structure

The repository contains several major components.

### `cmd/`

Command-line applications and executables.

The `git-gost` CLI lives here.

### `internal/`

Internal application logic that is not intended to be imported by external projects.

This includes HTTP handlers, providers, Git protocol handling, jobs, authentication/token handling, and other core functionality.

### `pkg/`

Packages intended to contain reusable functionality.

### `web/`

The gitGost web interface and frontend assets.

### `android/`

The Android application.

When possible, keep changes isolated to the component they affect.

---

## Supported Providers

gitGost integrates with multiple Git forges, including:

* GitHub
* GitLab
* Codeberg

When adding provider-specific functionality, avoid unnecessarily coupling it to another provider.

Prefer shared abstractions when the behavior is conceptually common between providers.

Provider-specific differences should remain explicit when necessary.

---

## Privacy First

Privacy is a fundamental design principle of gitGost.

Contributions should avoid introducing unnecessary:

* telemetry
* tracking
* personally identifiable information
* persistent identifiers
* third-party analytics
* unnecessary logging
* external requests
* data retention

Before introducing a new external service, dependency, analytics system, or form of telemetry, consider whether the feature can be implemented without it.

If a change affects the privacy guarantees of gitGost, document the impact clearly in the pull request.

---

## Security

Security-sensitive changes require additional care.

Do not include secrets, access tokens, private keys, credentials, or other sensitive information in commits, issues, or pull requests.

If you discover a security vulnerability, follow [`SECURITY.md`](./SECURITY.md) instead of publicly reporting the vulnerability.

---

## Issues

### Before opening an issue

Please:

1. Search existing issues.
2. Confirm that the problem still exists on the latest version.
3. Provide enough information for another contributor to reproduce the problem.

### Bug reports

A useful bug report should include:

* What you expected to happen
* What actually happened
* Steps to reproduce the problem
* gitGost version or commit
* Operating system
* Relevant provider (GitHub, GitLab, or Codeberg)
* Relevant logs or error messages
* A minimal reproduction, when possible

Please remove credentials, tokens, personal information, and other sensitive data before posting logs.

### Feature requests

Feature requests should explain:

* The problem you are trying to solve
* Why the feature would be useful
* How you expect it to work
* Any relevant privacy, security, or compatibility considerations

---

## Pull Requests

Before opening a pull request:

1. Make sure your branch is based on the current `main`.
2. Keep the change focused.
3. Add or update tests when appropriate.
4. Update documentation when behavior changes.
5. Run the relevant build and test commands.
6. Review your own diff before submitting.

A pull request should explain:

* **What** changed
* **Why** it changed
* **How** it was implemented
* **How** it was tested
* Any relevant privacy or security implications

Avoid combining unrelated changes into a single PR.

### Small PRs are preferred

A focused PR is easier to review, test, discuss, and maintain.

If a change contains several independent improvements, consider submitting separate pull requests.

---

## AI-Assisted Contributions

We allow contributors to use AI-assisted development tools, including:

* GitHub Copilot
* Claude Code
* Codex
* Cursor
* Cline
* Other coding assistants and LLM-based tools

Using AI does not remove the contributor's responsibility for the code.

If AI substantially contributed to your pull request, **disclose this in the PR description**.

The contributor must:

* Understand the submitted code.
* Review the generated changes.
* Verify that the implementation is correct.
* Be able to explain the relevant changes.
* Respond to review feedback.
* Take responsibility for the final patch.

AI-generated code is reviewed in the same way as human-written code.

### Autonomous and bulk contributions

gitGost does **not** accept bulk, queue-driven, or mass-generated contributions from autonomous coding agents.

Do not:

* Iterate through the repository's issues with an autonomous agent and submit PRs for each one.
* Generate patches for many unrelated issues automatically.
* Submit duplicate fixes because an agent independently discovered an existing issue.
* Operate an automated system whose primary purpose is generating PR volume.
* Use an autonomous agent to repeatedly submit PRs without meaningful human involvement.

A contribution generated with the assistance of an autonomous agent is acceptable when a specific human contributor has deliberately selected the specific change and is actively responsible for it.

The human contributor must remain involved in the review process and must be able to understand and discuss the submitted code.

Repeated bulk or automated submissions may be closed without review and may result in restrictions on further contributions.

For more detailed AI-specific rules, see [`AI-POLICY.md`](./AI-POLICY.md).

---

## Automated Comments

Do not use automated systems to post generated comments on issues or pull requests unless the repository explicitly provides a workflow for that purpose.

In particular, contributors should not use bots or agents to automatically post:

* Issue summaries
* PR summaries
* Repeated status comments
* AI-generated review comments
* Promotional comments
* Duplicate comments

Contributors are expected to communicate directly and meaningfully with maintainers.

---

## Commit Messages

Use clear and concise commit messages.

Prefer messages that describe the change rather than the implementation process.

Examples:

```text
fix: preserve git protocol query parameters
feat: add Codeberg release downloads
docs: improve anonymous contribution guide
test: cover repository policy validation
refactor: simplify provider request handling
```

Keep unrelated changes out of the same commit when practical.

---

## Code Style

Follow the conventions already used in the area of the code you are modifying.

For Go:

```bash
gofmt -w .
go test ./...
```

Avoid introducing unnecessary abstractions or dependencies.

When modifying existing code, prefer consistency with the surrounding implementation unless there is a clear reason to improve the design.

---

## Dependencies

Before adding a dependency, consider:

* Is it necessary?
* Is there already an equivalent dependency in the project?
* Is it actively maintained?
* What license does it use?
* Does it introduce telemetry or external network requests?
* Does it increase the attack surface?
* Does it affect the privacy guarantees of gitGost?
* Can the functionality reasonably be implemented without another dependency?

For security-sensitive or privacy-sensitive dependencies, explain the rationale in the pull request.

---

## Documentation

Documentation changes are welcome.

When adding or changing functionality, update the relevant documentation when appropriate.

Examples include:

* `README.md`
* Architecture documentation
* Privacy documentation
* CLI documentation
* API documentation
* Configuration documentation

Documentation should remain accurate with the current implementation.

---

## Review Process

Pull requests are reviewed by maintainers and may receive requests for:

* Code changes
* Tests
* Documentation
* Simplification
* Privacy or security improvements
* Better error handling
* Compatibility fixes

Please treat review as collaboration rather than approval/rejection.

A maintainer may ask for a different implementation even when the original implementation works.

---

## What We Look For

A good contribution generally has:

* A clear purpose
* A focused scope
* Tests where appropriate
* Clear documentation when needed
* Minimal unnecessary dependencies
* Good error handling
* Privacy-conscious design
* Security-conscious implementation
* Compatibility with supported providers
* A clear explanation in the pull request

---

## License

By contributing to gitGost, you agree that your contributions are provided under the license of the project.

Please review the repository's `LICENSE` file before contributing.

---

## Thank You

Every contribution helps make gitGost better.

Whether you are fixing a typo, improving documentation, reporting a bug, adding tests, improving privacy, or implementing a major feature, we appreciate your time and effort.
