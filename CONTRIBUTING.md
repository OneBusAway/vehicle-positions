# Contributing to vehicle-positions

Thank you for your interest in contributing to **vehicle-positions** — a Go-based backend server that ingests vehicle location reports and produces [GTFS-Realtime Vehicle Positions](https://gtfs.org/documentation/realtime/proto/) feeds. This project is part of the [OneBusAway](https://onebusaway.org) ecosystem under the [Open Transit Software Foundation (OTSF)](https://opentransitsoftwarefoundation.org).

Your contributions help bring real-time transit tracking to agencies in developing countries where traditional AVL infrastructure is unavailable. Whether you are fixing a bug, improving documentation, or building a new feature — every contribution matters.

---

## Table of Contents

- [Contributing to vehicle-positions](#contributing-to-vehicle-positions)
  - [Table of Contents](#table-of-contents)
  - [Getting Started](#getting-started)
  - [Development Environment Setup](#development-environment-setup)
    - [Prerequisites](#prerequisites)
    - [Optional: Protocol Buffers](#optional-protocol-buffers)
  - [Repository Setup](#repository-setup)
    - [1. Fork and clone the repository](#1-fork-and-clone-the-repository)
    - [2. Add the upstream remote](#2-add-the-upstream-remote)
    - [3. Install dependencies](#3-install-dependencies)
    - [4. Run the server](#4-run-the-server)
    - [5. Run with Docker Compose](#5-run-with-docker-compose)
  - [Running Tests](#running-tests)
  - [Code Style](#code-style)
    - [Formatting](#formatting)
    - [Linting](#linting)
    - [General Guidelines](#general-guidelines)
  - [Use of AI Coding Assistants](#use-of-ai-coding-assistants)
  - [Branching and Workflow](#branching-and-workflow)
    - [Step-by-step](#step-by-step)
  - [Commit Message Guidelines](#commit-message-guidelines)
    - [Format](#format)
    - [Types](#types)
    - [Examples](#examples)
    - [Guidelines](#guidelines)
    - [Atomic Commits](#atomic-commits)
      - [Squashing before opening a pull request](#squashing-before-opening-a-pull-request)
  - [Pull Request Guidelines](#pull-request-guidelines)
    - [Writing a good PR description](#writing-a-good-pr-description)
  - [Pull Request Review Expectations](#pull-request-review-expectations)
  - [Issue Reporting](#issue-reporting)
    - [Reporting a Bug](#reporting-a-bug)
    - [Requesting a Feature](#requesting-a-feature)
    - [Security Vulnerabilities](#security-vulnerabilities)
  - [Areas for Contribution](#areas-for-contribution)
  - [Contributor License Agreement](#contributor-license-agreement)
  - [Code of Conduct](#code-of-conduct)

---

## Getting Started

If you are new to the project, the best way to get involved is to look through open issues labeled:

- [`good first issue`](https://github.com/OneBusAway/vehicle-positions/issues?q=is%3Aopen+is%3Aissue+label%3A%22good+first+issue%22) — well-scoped tasks suitable for new contributors
- [`help wanted`](https://github.com/OneBusAway/vehicle-positions/issues?q=is%3Aopen+is%3Aissue+label%3A%22help+wanted%22) — issues where maintainer bandwidth is limited and community help is welcome

Before you begin working on an issue, please leave a comment expressing your intent. This prevents duplicate effort and gives maintainers an opportunity to share context or guidance upfront.

If you have a new idea that is not tracked by an existing issue, please [open an issue](#issue-reporting) first to discuss it before submitting a pull request. This ensures alignment with the project's direction before significant time is invested.

---

## Development Environment Setup

### Prerequisites

- **Go 1.22+ (or the version specified in go.mod)** — [Install Go](https://go.dev/doc/install)
- **Git**
- **Docker and Docker Compose** (optional, for running the full stack locally)
- **Protocol Buffers compiler (`protoc`)** — required only if you are modifying the GTFS-RT protobuf definitions

Verify your Go installation:

```bash
go version
```

You should see output like `go version go1.21.x ...`. If not, revisit the [official installation guide](https://go.dev/doc/install).

### Optional: Protocol Buffers

If you need to regenerate Go code from the `.proto` files:

```bash
# Install protoc (macOS)
brew install protobuf

# Install the Go protobuf plugin
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
```

---

## Repository Setup

### 1. Fork and clone the repository

[Fork the repository](https://github.com/OneBusAway/vehicle-positions/fork) on GitHub, then clone your fork locally:

```bash
git clone https://github.com/<your-username>/vehicle-positions.git
cd vehicle-positions
```

### 2. Add the upstream remote

```bash
git remote add upstream https://github.com/OneBusAway/vehicle-positions.git
```

### 3. Install dependencies

Go modules are used for dependency management. Dependencies are fetched automatically, but you can download them explicitly:

```bash
go mod download
```

### 4. Run the server

```bash
go run .
```

By default, the server starts on port `8080`. Refer to `README.md` for available configuration environment variables (API keys, database settings, port, etc.).

### 5. Run with Docker Compose

To run the full stack (server + database) locally using Docker:

```bash
docker compose up
```

---

## Running Tests

The project uses the standard Go testing toolchain. To run the full test suite:

```bash
go test ./...
```

To run tests with verbose output:

```bash
go test -v ./...
```

To run tests for a specific package:

```bash
go test -v ./handlers/...
```

To run a single named test:

```bash
go test -v -run TestVehiclePositionFeed ./...
```

All tests must pass before a pull request can be merged. If you are adding a new feature or fixing a bug, please include corresponding tests.

---

## Code Style

This project follows standard Go conventions and uses automated formatting to keep the codebase consistent.

### Formatting

Format your code before committing:

```bash
gofmt -w .
```

Alternatively, [`gofumpt`](https://github.com/mvdan/gofumpt) is preferred for stricter, more consistent formatting:

```bash
# Install gofumpt
go install mvdan.cc/gofumpt@latest

# Format all files
gofumpt -w .
```

Most editors (VS Code with the Go extension, GoLand, Neovim) can be configured to run `gofmt` or `gofumpt` on save automatically.

### Linting

If available in the project CI, run the linter locally before submitting:

```bash
# Install golangci-lint
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

golangci-lint run
```

### General Guidelines

- Follow the conventions in [Effective Go](https://go.dev/doc/effective_go) and the [Go Code Review Comments](https://github.com/golang/go/wiki/CodeReviewComments).
- Keep functions focused and reasonably short.
- Export only what needs to be exported; prefer unexported identifiers for internal implementation details.
- Write clear comments on all exported types, functions, and methods.
- Avoid unnecessary dependencies; prefer the standard library where practical.

---

## Use of AI Coding Assistants

We recognize that AI tools (like GitHub Copilot, ChatGPT, or similar assistants) are valuable for learning, drafting boilerplate, and solving problems. You are welcome to use them when contributing, but please keep the following in mind:

* **You are accountable for the code:** If you submit code generated by AI, you are responsible for understanding exactly how it works. "The AI wrote it" is not a valid reason for introducing bugs, security vulnerabilities, or performance regressions.
* **Respect licenses and copyright:** Ensure that any AI-generated code does not violate third-party licenses or inject proprietary code into this repository.
* **Focus on the 'Why':** AI tools are great at writing the *how*, but you must provide the *why*. Ensure your PR descriptions and code comments explain the reasoning behind the architectural choices, not just what the code does. 
* **Maintain project context:** AI tools often hallucinate or suggest patterns that do not match our existing codebase. Always refactor AI suggestions to match the established Go conventions and architecture of the `vehicle-positions` project.
  
---

## Branching and Workflow

This project uses a standard **fork → branch → commit → pull request** workflow.

### Step-by-step

1. **Sync your fork** with the upstream `main` branch before starting work:

   ```bash
   git fetch upstream
   git checkout main
   git merge upstream/main
   ```

2. **Create a feature branch** with a descriptive name:

   ```bash
   git checkout -b feat/gtfs-rt-staleness-filter
   # or
   git checkout -b fix/auth-token-expiry-handling
   ```

3. **Make your changes.** Keep changes focused — one logical change per branch and pull request.

4. **Commit your work** using clear commit messages (see below).

5. **Push your branch** to your fork:

   ```bash
   git push origin feat/gtfs-rt-staleness-filter
   ```

6. **Open a pull request** against `OneBusAway/vehicle-positions:main` on GitHub.

---

## Commit Message Guidelines

Clear commit messages make the project history easy to read and help automate changelogs. Follow the [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/) specification.

### Format

```
<type>(<optional scope>): <short description>

<optional body — explain what and why, not how>

<optional footer — e.g., Closes #123>
```

### Types

| Type | When to use |
|---|---|
| `feat` | A new feature |
| `fix` | A bug fix |
| `docs` | Documentation changes only |
| `refactor` | Code restructuring with no behavior change |
| `test` | Adding or correcting tests |
| `chore` | Build process, tooling, dependency updates |
| `perf` | Performance improvements |
| `ci` | CI/CD configuration changes |

### Examples

```
feat(feed): add configurable staleness threshold for GTFS-RT feed

Vehicles that have not reported a position within the configured
threshold (default 5 minutes) are now excluded from the feed output.

Closes #42
```

```
fix(auth): return 401 instead of 500 on expired JWT token
```

```
docs: add Docker deployment section to README
```

```
test(handlers): add coverage for location report validation edge cases
```

### Guidelines

- Use the imperative mood in the subject line: "add feature", not "added feature" or "adds feature"
- Keep the subject line under 72 characters
- Separate the subject from the body with a blank line
- Reference the relevant GitHub issue number in the footer where applicable

### Atomic Commits

Each commit should represent exactly **one logical change**. Avoid combining unrelated changes into a single commit.

Atomic commits make the project history easier to read, speed up code review, and simplify debugging. When a commit does one thing, reviewers can evaluate it in isolation, and tools like `git bisect` can pinpoint regressions precisely. A history of atomic commits is also far easier to revert if something goes wrong.

**Good — one commit, one concern:**

```
feat(api): add endpoint for retrieving vehicle details

Introduces a new GET endpoint that returns basic metadata
for a vehicle including label, agency tag, and active status.

Closes #87
```

This commit focuses on **one clearly defined change**.

---

**Bad — multiple unrelated changes bundled together:**

```
update API endpoint, fix tests, update README, bump dependencies
```

This commit mixes multiple independent changes:

- an API change
- test fixes
- documentation updates
- dependency updates

Each of these should be **separate commits** so they can be reviewed, reverted, or cherry-picked independently.

#### Squashing before opening a pull request

If your branch contains several work-in-progress commits that together represent a single logical change (e.g., `wip`, `fix typo`, `address review feedback`), please squash them into a single well-described commit before opening a pull request:

```bash
git rebase -i HEAD~N
```

Replace `N` with the number of commits you want to consolidate. In the interactive rebase screen, mark the commits you want to merge into the first one as `squash` (or `s`), then edit the resulting commit message to clearly describe the complete change.

---

## Pull Request Guidelines

Before opening a pull request, please verify the following:

- [ ] All tests pass locally (`go test ./...`)
- [ ] Code is formatted (`gofmt -w .` or `gofumpt -w .`)
- [ ] New functionality includes tests
- [ ] The PR description clearly explains the problem being solved and the approach taken
- [ ] The PR is focused on a single concern — avoid bundling unrelated changes
- [ ] If the PR closes an issue, include `Closes #<issue-number>` in the description

### Writing a good PR description

A good PR description answers three questions:

1. **What** — what does this change do?
2. **Why** — why is this change needed?
3. **How** — briefly, how was it implemented? (especially useful for non-obvious approaches)

If the change affects behavior observable to the end user or a transit agency operator, include a short summary of how to manually verify the behavior.

---

## Pull Request Review Expectations

Maintainers aim to review pull requests within **5–7 business days**. Please be patient — this is a volunteer-driven project.

When reviewing your pull request, maintainers will evaluate:

- **Correctness** — does the change do what it claims? Are edge cases handled?
- **Test coverage** — are the tests meaningful and sufficient?
- **Code quality** — is the code readable, idiomatic Go?
- **Formatting** — is the code properly formatted?
- **Scope** — is the PR focused, or does it contain unrelated changes?
- **Spec compliance** — for GTFS-RT changes, does output remain compliant with the [GTFS-RT specification](https://gtfs.org/documentation/realtime/proto/)?

Reviewers may request changes. This is a normal part of the process and not a rejection. Please address feedback constructively and update your branch accordingly.

Once all feedback is addressed and automated checks pass, a maintainer will merge the pull request.

---

## Issue Reporting

### Reporting a Bug

If you encounter a bug, please [open a GitHub issue](https://github.com/OneBusAway/vehicle-positions/issues/new) and include:

- A clear, descriptive title
- Steps to reproduce the problem
- What you expected to happen
- What actually happened (include relevant logs or error output)
- Your Go version (`go version`) and operating system
- Any relevant configuration (environment variables, database backend, Docker vs. local)

The more detail you provide, the faster the issue can be diagnosed and resolved.

### Requesting a Feature

For feature requests, please open an issue describing:

- The problem or limitation you are encountering
- The solution or behavior you would like to see
- Any alternatives you have considered

Feature requests are evaluated against the project's scope and roadmap. Adding context about your use case (e.g., a specific transit agency scenario) is very helpful.

### Security Vulnerabilities

Please **do not** open a public issue for security vulnerabilities. Instead, report them privately by emailing the maintainers or using [GitHub's private vulnerability reporting](https://github.com/OneBusAway/vehicle-positions/security/advisories/new) if enabled.

---

## Areas for Contribution

Not sure where to start? Here are areas where contributions are particularly welcome:

- **Documentation** — improving setup guides, adding architecture diagrams, writing API usage examples
- **Test coverage** — adding unit and integration tests for handlers, the store layer, the tracker, and the GTFS-RT feed builder
- **API improvements** — implementing or refining REST endpoints (vehicle management, driver management, trip lifecycle)
- **GTFS-RT compliance** — ensuring feed output passes the [MobilityData GTFS-RT Validator](https://github.com/MobilityData/gtfs-realtime-validator) under all conditions
- **Performance optimization** — improving in-memory state management, concurrent request handling, and feed serialization
- **Admin interface** — building or improving the operator-facing web UI (vehicle map, driver management, trip history)
- **Android integration** — improving compatibility with the companion Android driver app, including the location reporting API contract
- **Deployment tooling** — Docker, docker-compose improvements, documentation for production deployments (PostgreSQL, reverse proxy, TLS, systemd)
- **Offline queuing (v2)** — designing and implementing batch location ingestion for the planned offline-first Android client upgrade

---

## Contributor License Agreement

By submitting a pull request, you represent that you have the right to contribute the code and that your contribution may be distributed under the project's [Apache 2.0 License](./LICENSE).

Depending on the repository's policy at the time of contribution, you may be asked to sign a Contributor License Agreement (CLA) before your pull request can be merged. If required, a bot will comment on your pull request with instructions.

---

## Code of Conduct

This project is part of the OneBusAway community under the Open Transit Software Foundation. All contributors are expected to engage respectfully and constructively.

Please be kind to other contributors and maintainers. Harassment, discrimination, and disrespectful behavior of any kind will not be tolerated.

By participating in this project, you agree to uphold our community standards. If you experience or witness behavior that violates these standards, please report it to the project maintainers.

---

Thank you for helping make real-time transit accessible to more people around the world. 🚌