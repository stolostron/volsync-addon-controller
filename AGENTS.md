# volsync-addon-controller

This repository contains the RHACM VolSync add-on controller. It is a Go controller that runs on an ACM hub cluster and deploys VolSync to managed clusters.

## Repository layout

- `main.go` starts the controller-runtime manager, leader election, health probes, and the add-on framework runnable.
- `controllers/` contains the add-on framework implementation, label-triggered installation controller, manifest helpers, and tests.
- `controllers/manifests/` contains embedded no-op and Helm deployment manifests.
- `controllers/helmutils/` renders and initializes the embedded Helm chart.
- `helmcharts/` contains the chart embedded for normal builds; `hack/testhelmcharts/` contains test chart variants.
- `hack/crds/` contains CRDs used by local testing.

## Architecture

The process uses controller-runtime for manager lifecycle, leader election, and health/readiness probes. After the manager becomes leader, it loads default VolSync image settings from the hub's MulticlusterHub resource, initializes the embedded Helm chart, and starts the Open Cluster Management add-on framework controllers.

The primary add-on agent watches `ManagedClusterAddOn` resources named `volsync` and produces ManifestWork content for the target managed cluster. The default path deploys VolSync as a Helm chart; an annotation can select the no-op path. A separate label controller creates the `volsync` `ManagedClusterAddOn` when a hub `ManagedCluster` has `addons.open-cluster-management.io/volsync: "true"`.

## Development commands

Run commands from the repository root.

| Command | Purpose |
|---|---|
| `make build` | Build `bin/controller` with version and commit linker flags. |
| `make test` | Download/setup golangci-lint, envtest, and Ginkgo as needed, lint, then run the Ginkgo test suite with coverage. |
| `make lint` | Run golangci-lint against `./...`. |
| `make run` | Run the controller locally against the current kubeconfig; it sets `POD_NAMESPACE=open-cluster-management` and uses `helmcharts/`. |
| `make docker-build` | Run tests and build the container image. Override `IMG` to select the image name/tag. |
| `make docker-push` | Push `IMG` to the configured registry. |
| `go test ./...` | Run Go package tests directly when envtest/Ginkgo setup is not required. |

`make test` and `make docker-build` require network access to download helper tools and envtest assets. `make run` requires access to a configured Kubernetes/OpenShift cluster. `make docker-build` and `make docker-push` require Docker and registry access respectively.

## Personal configuration

Read personal config at the start of any task that needs an assignee, email, or project key.
Canonical path: `~/.config/user.local.md` (tool-agnostic, global).
If the file does not exist, fall back to agent memory (`user-config`), then placeholders.
This repository does not define `make personalize`; use the Fleet Engineering repository's personalization tooling when generating or updating the shared file.

## Tool availability

- GitHub operations: GitHub MCP tools are available when the relevant org-scoped server is enabled. The `gh` CLI is not installed in this environment.
- Jira operations: Jira MCP tools are available. A `jira` CLI is not assumed.
- GitHub token conventions for CLI-compatible tooling use `GH_TOKEN` or `GH_TOKEN_<ORG>` with the organization lowercased and hyphens replaced by underscores.

## Contribution conventions

- Keep controller behavior covered by the existing Ginkgo/Gomega tests under `controllers/`.
- Use the repository's signed-commit convention when committing changes.
- Do not edit generated or embedded deployment content without checking the corresponding chart/manifests and tests.

## Fleet Engineering Skills

Fetch and apply the relevant skill when the task matches its domain.

| Skill | When to use |
|---|---|
| [bug-specialist](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/bug-specialist/SKILL.md) | Bug triage, reproduction steps, fix planning |
| [epic-specialist](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/epic-specialist/SKILL.md) | Multi-sprint epics with outcomes |
| [feature-specialist](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/feature-specialist/SKILL.md) | Large customer-facing capabilities |
| [initiative-specialist](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/initiative-specialist/SKILL.md) | Multi-team strategic programs |
| [jira-create](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/jira-create/SKILL.md) | Interactive issue creation with specialist delegation |
| [jira-qe-readiness](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/jira-qe-readiness/SKILL.md) | Check whether a Jira ticket has enough information for QE verification |
| [jira-report](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/jira-report/SKILL.md) | Jira portfolio reports and issue quality reviews |
| [jira-specialist](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/jira-specialist/SKILL.md) | General Jira triage, search, linking, and transitions |
| [jira-type-audit](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/jira-type-audit/SKILL.md) | Audit and correct Jira issue types across the hierarchy |
| [outcome-specialist](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/outcome-specialist/SKILL.md) | Strategic outcomes tied to OKRs |
| [release-dod](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/release-dod/SKILL.md) | Release Definition of Done checklists |
| [risk-report](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/risk-report/SKILL.md) | Automated Jira risk signal detection and status drafting |
| [risk-specialist](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/risk-specialist/SKILL.md) | Risk register and mitigation planning |
| [spike-specialist](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/spike-specialist/SKILL.md) | Time-boxed research and proofs of concept |
| [story-specialist](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/story-specialist/SKILL.md) | User stories with acceptance criteria |
| [supportex-review](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/supportex-review/SKILL.md) | Review SUPPORTEX support exception requests |
| [task-specialist](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/task-specialist/SKILL.md) | Internal technical tasks |
| [ticket-specialist](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/ticket-specialist/SKILL.md) | Stakeholder request intake and triage |
| [backlog-grooming](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/backlog-grooming/SKILL.md) | Produce a Jira grooming agenda and readiness dashboard |
| [breaking-changes](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/breaking-changes/SKILL.md) | Detect breaking API, configuration, behavior, and integration changes |
| [ci-triage](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/ci-triage/SKILL.md) | Diagnose failing PR checks and suggest targeted fixes |
| [coderabbit-sync](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/coderabbit-sync/SKILL.md) | Maintain the Fleet reference CodeRabbit configuration |
| [cve-sustaining-handoff](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/cve-sustaining-handoff/SKILL.md) | Resolve CVE trackers and hand off sustaining work |
| [vulnerability-slack-report](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/vulnerability-slack-report/SKILL.md) | Draft weekly vulnerability summaries |
| [diagnosing-bugs](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/diagnosing-bugs/SKILL.md) | Structure debugging, reproduction, minimization, and fixes |
| [finish-work](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/finish-work/SKILL.md) | Commit, push, open a PR, and update Jira |
| [github-org-access](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/github-org-access/SKILL.md) | Modify GitHub org access through Peribolos configuration |
| [init-context-docs](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/init-context-docs/SKILL.md) | Assess and bootstrap repository AI-readiness documentation |
| [opencode-setup](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/opencode-setup/SKILL.md) | Configure OpenCode models, MCP servers, and skills |
| [org-repo-audit](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/org-repo-audit/SKILL.md) | Audit organization repositories for SDLC readiness |
| [pr-fix](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/pr-fix/SKILL.md) | Fix PR merge conflicts, CI failures, and review comments |
| [pr-hygiene](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/pr-hygiene/SKILL.md) | Scan repositories for stale PRs and review needs |
| [pr-review](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/pr-review/SKILL.md) | Review GitHub PRs with layered analysis and inline comments |
| [pr-review-detailed](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/pr-review-detailed/SKILL.md) | Perform structured checklist-based PR analysis |
| [pr-review-fix](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/pr-review-fix/SKILL.md) | Multi-model pre-commit review and fix passes |
| [release-notes](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/release-notes/SKILL.md) | Generate categorized release notes between git refs |
| [renovate-prs](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/renovate-prs/SKILL.md) | Manage Renovate and MintMaker dependency PRs |
| [repo-content-audit](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/repo-content-audit/SKILL.md) | Find unlinked or orphaned repository content |
| [repo-setup](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/repo-setup/SKILL.md) | Onboard a repository to the Fleet Engineering Agentic SDLC |
| [rhacm-addon-wizard](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/rhacm-addon-wizard/SKILL.md) | Guide RHACM add-on development and scaffold generation |
| [scored-code-review](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/scored-code-review/SKILL.md) | Deprecated code review workflow retained for one release cycle |
| [session-summary](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/session-summary/SKILL.md) | Correlate session work with Jira and GitHub |
| [start-work](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/start-work/SKILL.md) | Create a Jira sub-task for current work |
| [test-coverage-gap](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/test-coverage-gap/SKILL.md) | Identify risk-prioritized test coverage gaps |
| [f2f-daily-summary](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/f2f-daily-summary/SKILL.md) | Capture daily F2F notes as Jira sub-tasks |
| [f2f-epic-specialist](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/f2f-epic-specialist/SKILL.md) | Create and manage F2F meeting epics |
| [presentation-task](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/presentation-task/SKILL.md) | Log delivered presentations as Jira sub-tasks |
| [scrum-status](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/scrum-status/SKILL.md) | Capture scrum bullets and generate weekly team reports |
