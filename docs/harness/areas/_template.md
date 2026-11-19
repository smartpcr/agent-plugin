# <area name>: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". IDs in this document use the area key `<AREA>`.

| | |
|---|---|
| **Area key** | `<AREA>` |
| **Upstream packages** | count and group names |
| **Serves outcomes** | `DH-O-n` from [../design.md](../design.md) |
| **Depends on areas** | links |
| **Priority** | P0 / P1 / P2 and why |

## 1. Purpose

What this part of the harness does for a user or operator, in plain English, and what
would be missing without it.

## 2. Vocabulary

| Term | What it is | Why it exists |
|---|---|---|

Introduce every term before it is used. Define acronyms.

## 3. What upstream does

### 3.1 Packages

| Upstream unit | Package | Role (definition / provider / consumer / other) | What it does |
|---|---|---|---|

Every `UP-PKG` unit assigned to you appears in this table.

### 3.2 Behaviour

How the pieces work together at runtime: what starts what, which service each package
provides (`ctx` key), which events it emits and listens to, what it persists, which
tools it exposes to the model, and which config it reads. Cite `path:line`. A small
diagram is welcome where it makes a relationship clearer.

### 3.3 Failure, retry, rollback and cleanup upstream

What upstream does when this area fails half-way, and what it leaves behind.

## 4. Upstream decisions dissected

| Upstream decision | What was decided and why | Verdict (keep / change / drop) | Reason | Go decision |
|---|---|---|---|---|

Every `UP-DEC` unit assigned to you appears here, and so does every decision that is
only visible in code or READMEs. "Drop" and "change" need a reason a reviewer can check.

## 5. Tech debt and its resolution

| Debt ID | What it is | Evidence | Resolution in Go (design change / not applicable, and why) |
|---|---|---|---|

Every `UP-DEBT` unit assigned to you appears here. Debt may not be carried forward
without a written justification and an owning stage.

## 6. Go design

### 6.1 Shape

Go packages under `src/harness/...`, the components (cordis `Define` / `Construct`),
the services they provide and inject, and the main interfaces with their method sets.
Explain why each exists.

### 6.2 Contracts

Events, config (with defaults and validation), persisted data, tools exposed to the
model, wire messages. State which upstream contract unit each one replaces.

### 6.3 Ownership, failure, retry, rollback and cleanup

Which fiber owns each resource. What happens when a load fails, a dependency goes away,
a process dies or a write is interrupted. What is undone, what is retried, and what is
left uncertain.

## 7. Decisions

<a id="dh-adr-<area>-1"></a>
### DH-ADR-<AREA>-1 <title>

- **Decision.**
- **Why.**
- **Tradeoff.** Pros and cons, and the constraint that drove the choice.
- **Serves.** Outcome IDs.
- **Verified by.** Test or scenario IDs.

## 8. Deliverables

<a id="dh-d-<area>-1"></a>
### DH-D-<AREA>-1 <result in plain English>

- **Result and why it matters.**
- **How it is implemented.** Not only file names: the approach.
- **Components and files.**
- **Depends on.** Deliverable IDs (implementation dependencies) and, separately, runtime prerequisites.
- **Priority.**
- **Done when.** Links to the tests and scenarios below.

## 9. Acceptance

### 9.1 Local tests

| Test ID | Proves | What is asserted |
|---|---|---|

### 9.2 Real-environment scenarios

<a id="dh-e-<area>-1"></a>
#### DH-E-<AREA>-1 <title>

- **Proves.** Deliverable IDs.
- **Given.** Target environment, starting state, fixture, inputs, identities, prerequisites.
- **When.** Ordered actions and where each runs.
- **Then.** Observable pass/fail assertions, including failure, retry, ownership, rollback and cleanup behaviour.
- **Cleanup.** How the baseline is restored and what to keep if recovery fails.

Nothing here has been run. A scenario that is missing, skipped or blocked is open, not passed.

## 10. Open questions and limitations

## 11. Units dispositioned by this spec

Counts per unit type, and the path of the ledger fragment that holds the rows.
