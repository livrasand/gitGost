# Style Guide

Welcome to the gitGost style guide.

This document describes the coding, documentation, interface, and communication styles used throughout the gitGost project.

The goal of this guide is not to enforce personal preferences. It exists to keep gitGost consistent, readable, maintainable, privacy-conscious, and easy for contributors to understand.

This guide covers:

* Go coding style
* Web coding style
* Android coding style
* Git and commit message style
* Documentation writing style
* User interface writing style
* Pull request and issue writing style

Rules enforced automatically by project tooling take precedence over the subjective guidance in this document.

---

## Style Rules vs. Style Guidance

Not every style decision should be enforced by a tool.

We distinguish between two categories:

### Style rules

A **style rule** is a convention that can reasonably be checked automatically.

Examples include:

* Go formatting
* Import formatting
* Build errors
* Test failures
* Static analysis
* Invalid syntax
* Certain linting violations

When automated tooling reports a style violation, contributors are expected to fix it before submitting a pull request.

### Style guidance

**Style guidance** covers decisions that are difficult or inappropriate to enforce automatically.

Examples include:

* Choosing a clear function name
* Keeping an API simple
* Avoiding unnecessary abstractions
* Writing useful error messages
* Keeping UI text concise
* Avoiding unnecessary comments
* Choosing an appropriate level of documentation

Style guidance is reviewed by humans and should be followed whenever reasonably possible.

---

# Go Style Guide

Go is the primary language of the gitGost backend and CLI.

Follow the conventions established by the Go project unless gitGost has a documented reason to do otherwise.

## Formatting

Go code must be formatted with `gofmt`.

Run:

```bash
gofmt -w .
```

Do not manually format code in a way that conflicts with `gofmt`.

Formatting changes should generally be kept separate from unrelated behavioral changes.

## Imports

Keep imports organized according to standard Go conventions.

Do not manually maintain complicated import ordering when the formatter or tooling can do it automatically.

## Naming

Prefer short, descriptive names.

Good:

```go
func getRepositoryPolicy(repo string) (*RepoPolicy, error)
```

Avoid:

```go
func getRepositoryPolicyFromRepositoryStringValue(repoStringValue string) (*RepoPolicy, error)
```

Exported identifiers should use clear names that make sense without requiring an implementation comment.

Use Go naming conventions:

```go
type Repository struct{}

func GetRepository() {}

const MaxRepositorySize = 500
```

Avoid unnecessary abbreviations.

Prefer:

```go
repository
configuration
request
response
```

over:

```go
repo
config
req
resp
```

unless the abbreviation is well established in the surrounding code.

## Functions

Prefer small functions with a clear responsibility.

If a function is doing several unrelated things, consider splitting it into smaller functions.

Avoid creating abstractions merely to make a function shorter.

The simplest implementation that correctly expresses the behavior is generally preferred.

## Error Handling

Errors should provide useful context.

Prefer:

```go
return fmt.Errorf("fetch repository metadata: %w", err)
```

over:

```go
return err
```

when additional context would help identify where the failure occurred.

Do not silently ignore errors unless there is a deliberate reason to do so.

Avoid exposing sensitive information through errors or logs.

Never include:

* access tokens
* passwords
* private keys
* authentication headers
* unnecessary personal information

in error messages or logs.

## Logging

Logs should help operators diagnose problems without unnecessarily exposing user information.

Do not log sensitive request data merely because it is convenient.

In particular, avoid logging:

* authentication tokens
* cookies
* authorization headers
* full request bodies containing user data
* unnecessary IP addresses
* private repository information

When logging an error, prefer useful context without leaking sensitive data.

---

# Privacy-Aware Coding

Privacy is a core design principle of gitGost.

When implementing a feature, ask:

> Does this code collect, store, transmit, or expose information that it does not strictly need?

Prefer implementations that minimize data.

Avoid introducing:

* unnecessary telemetry
* tracking identifiers
* persistent user identifiers
* unnecessary cookies
* unnecessary third-party services
* unnecessary network requests
* unnecessary logging
* unnecessary data retention

When a feature can be implemented without collecting additional information, prefer that implementation.

Privacy considerations should be explicitly discussed in pull requests when a change affects user data, metadata, network behavior, or anonymity.

For the security and privacy model, see:

* [`Privacy Guarantees.md`](./Privacy%20Guarantees.md)
* [`THREAT_MODEL.md`](./THREAT_MODEL.md)
* [`SECURITY.md`](./SECURITY.md)

---

# Security-Aware Coding

Security should be considered part of normal development, not something added after implementation.

Be particularly careful when modifying:

* Git protocol handling
* repository URLs
* proxy endpoints
* authentication
* tokens
* filesystem operations
* command execution
* archive handling
* HTTP requests
* redirects
* user-controlled input
* repository content
* Markdown or HTML rendering

Never trust user-controlled input simply because it originated from a Git provider.

Validate inputs at trust boundaries.

When modifying security-sensitive code, add tests covering invalid and unexpected inputs whenever practical.

---

# Provider-Agnostic Design

gitGost supports multiple Git forges.

Current integrations include:

* GitHub
* GitLab
* Codeberg

When implementing functionality that exists across providers, prefer shared abstractions rather than duplicating the same behavior three times.

For example, if a concept exists for all providers, consider whether it belongs behind a common interface.

At the same time, do not force unrelated provider APIs into an artificial abstraction.

Provider differences are acceptable when they represent genuine differences between the platforms.

Prefer:

```text
shared behavior
    ↓
provider abstraction
    ↓
GitHub / GitLab / Codeberg implementation
```

over:

```text
GitHub implementation
GitLab copy of GitHub implementation
Codeberg copy of GitHub implementation
```

when the behavior is fundamentally the same.

---

# Web Style Guide

The `web/` directory contains the gitGost web interface.

The web interface should feel lightweight, functional, and consistent with gitGost's privacy-focused identity.

## HTML

Prefer semantic HTML.

Use elements according to their meaning:

```html
<nav>
<main>
<section>
<article>
<button>
<form>
```

instead of using generic `<div>` elements for everything.

Interactive elements should be accessible using the keyboard.

Prefer:

```html
<button type="button">Search</button>
```

over:

```html
<div onclick="search()">Search</div>
```

## CSS

Prefer simple, reusable styles.

Avoid introducing a large amount of CSS for a single component when an existing pattern can be reused.

Keep selectors understandable.

Avoid unnecessary specificity.

Prefer:

```css
.file-tree {
    display: flex;
}
```

over deeply nested selectors that are difficult to override.

## JavaScript

Keep frontend logic modular and readable.

Avoid large functions that manipulate unrelated parts of the UI.

Prefer explicit names:

```js
toggleLeftNav()
loadRepository()
renderFileTree()
```

over:

```js
doStuff()
handleThing()
process()
```

Avoid global state unless it is genuinely required.

When manipulating user-controlled content, use safe DOM APIs and avoid unnecessary use of `innerHTML`.

---

# Android Style Guide

The Android application should follow standard Android and Kotlin conventions.

Prefer idiomatic Kotlin.

Use descriptive names and keep Activities, Fragments, and other UI components focused on presentation and coordination rather than large amounts of business logic.

Keep network, persistence, and business logic separated from UI code where practical.

UI changes should consider:

* accessibility
* different screen sizes
* offline behavior
* loading states
* error states
* privacy
* unnecessary permissions

Do not request an Android permission unless the application genuinely requires it.

---

# Comments

Comments should explain **why**, not simply repeat **what** the code does.

Avoid:

```go
// Increment i
i++
```

Prefer:

```go
// GitHub limits this endpoint to 100 results per request.
page++
```

A comment should remain useful even if the implementation changes.

Do not use comments to preserve obsolete reasoning.

Update or remove comments when their underlying behavior changes.

---

# Documentation Style

Documentation should be:

* clear
* concise
* factual
* easy to scan
* technically accurate

Prefer simple language.

Good:

> gitGost removes the contributor's name and email before creating the anonymous contribution.

Avoid unnecessary marketing language in technical documentation:

> gitGost uses an incredibly revolutionary and groundbreaking privacy architecture to completely transform anonymous Git collaboration.

Technical documentation should describe what the software actually does.

Do not promise guarantees that the system cannot provide.

For example, prefer:

> gitGost provides anonymity against the threats described in the threat model.

over:

> gitGost makes you completely anonymous.

---

# README Style

The README is primarily a user-facing document.

It should answer these questions quickly:

1. What is gitGost?
2. Why does it exist?
3. How does it work?
4. How can I use it?
5. What are its limitations?
6. How can I contribute?

Keep examples practical.

Prefer commands that users can copy and run.

When documenting privacy features, clearly distinguish between:

* what gitGost protects against
* what gitGost does not protect against
* assumptions required for stronger anonymity

Never hide important limitations for the sake of marketing.

---

# UI Writing

gitGost uses short, direct language in its interface.

Prefer:

> Create issue

over:

> Create a new issue by clicking here

Prefer:

> Repository not found

over:

> Unfortunately, we were unable to locate the repository you were looking for.

## Buttons

Buttons should describe the action.

Good:

* Search
* Clone
* Push
* Create issue
* Open pull request
* View files
* Copy URL

Avoid vague buttons:

* Continue
* Proceed
* Do it
* Submit

unless the context makes the action completely obvious.

## Errors

Errors should tell the user:

1. What happened.
2. When possible, why it happened.
3. What they can do next.

Good:

> Push rejected: this repository has opted out of anonymous contributions.

Avoid:

> Something went wrong.

---

# Commit Messages

gitGost uses concise, descriptive commit messages.

Prefer conventional prefixes when appropriate:

```text
feat: add Codeberg release downloads
fix: preserve Git protocol query parameters
docs: improve contribution guide
test: add repository policy tests
refactor: simplify provider handling
build: update Android dependencies
ci: verify release artifacts
chore: update dependencies
```

Use the imperative mood.

Prefer:

```text
fix: preserve repository query parameters
```

over:

```text
fixed repository query parameters
```

Keep the first line concise.

For larger changes, include additional context in the commit body.

Example:

```text
fix: preserve Git protocol query parameters

Git clients may include protocol negotiation parameters in the
request query string. Preserve them when forwarding requests to
the upstream provider.
```

Avoid meaningless messages such as:

```text
update
changes
fix stuff
minor fixes
working now
final
final2
```

---

# Pull Request Titles

Pull request titles should describe the change clearly.

Good:

```text
feat: add anonymous issue comments for Codeberg
fix: prevent repository URL SSRF
docs: add self-hosting guide
refactor: unify provider repository lookups
```

Avoid:

```text
Update
Fix
Changes
My PR
Important
Please merge
```

The title should make sense when read in the project's release history.

---

# Issues

Issue titles should describe the problem or requested feature.

Good:

```text
Git protocol push fails for repositories with redirects
Android app does not preserve selected forge
Add repository opt-out documentation
```

Avoid:

```text
Bug
Help
It doesn't work
Problem!!!
Feature request
```

Provide enough information for another contributor to reproduce or understand the issue.

Remove secrets and private information before submitting logs.

---

# Pull Request Descriptions

A good pull request should explain:

* What changed
* Why it changed
* How it was implemented
* How it was tested
* Any privacy or security implications

For example:

```markdown
## Summary

Adds support for anonymous issue comments on Codeberg.

## Changes

- Adds the Codeberg comment API integration.
- Adds the corresponding proxy endpoint.
- Updates the repository issue UI.
- Adds tests for successful and rejected comments.

## Testing

- `go test ./...`
- Manual testing against a Codeberg repository.

## Privacy / Security

No additional user identifiers are stored or transmitted.
```

---

# AI-Assisted Development

AI-assisted development is allowed, but the contributor remains responsible for the submitted code.

Contributors should:

* understand the code they submit
* review generated changes
* verify behavior
* run appropriate tests
* be able to explain the implementation
* respond to review feedback

Do not use AI-generated code as a substitute for understanding the system.

For the specific rules governing AI agents, autonomous contributions, bulk submissions, and automated comments, see [`AI-POLICY.md`](./AI-POLICY.md).

---

# Avoiding Unnecessary Complexity

Prefer the simplest implementation that correctly solves the problem.

Do not introduce:

* abstractions without a clear benefit
* dependencies for trivial functionality
* frameworks for isolated problems
* configuration for things that do not need configuration
* generalized systems for one-off behavior

Before adding an abstraction, ask:

> Will this make the next change easier, or am I solving a problem we do not currently have?

Readable and boring code is often preferable to clever code.

---

# Consistency Over Personal Preference

When modifying existing code, follow the style of the surrounding code unless there is a good reason to change it.

Do not introduce a style change merely because you personally prefer another convention.

If an area of the codebase is inconsistent, prefer making a focused improvement rather than combining a style migration with an unrelated feature.

Large style changes should be discussed separately.

---

# How to Propose a Style Change

Anyone can propose a change to this guide.

Before proposing a project-wide style change, consider:

1. What problem does the current convention cause?
2. How would the proposed convention improve the project?
3. Can the new convention be enforced automatically?
4. Would it affect existing code?
5. Is the migration worth the maintenance cost?

Style discussions should focus on objective project benefits rather than personal preference.

For example:

> "This naming convention makes provider implementations easier to discover."

is more useful than:

> "I think this looks cleaner."

When possible, demonstrate the proposed convention with real code.

---

# Final Principle

The purpose of this style guide is not to make every contributor write code in exactly the same way.

The purpose is to make gitGost feel like **one coherent project**.

When choosing between two valid implementations, prefer the one that is:

1. simpler
2. clearer
3. safer
4. more privacy-preserving
5. easier to test
6. easier for another contributor to maintain

**Be clear. Be boring when boring is better. Protect the user.**
