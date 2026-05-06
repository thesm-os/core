---
rfc: NNNN
title: <Short title>
author: <Name>
status: Draft | Review | Accepted | Rejected | Withdrawn | Superseded
created: YYYY-MM-DD
updated: YYYY-MM-DD
discussion: <link to PR / issue thread>
supersedes: RFC-NNNN | none
superseded-by: RFC-NNNN | none
produces-adr: ADR-NNNN, ADR-NNNN | tbd | none
---

# RFC-NNNN: <Short title>

## Summary

<One paragraph. What is being proposed, in plain language.>

## Motivation

<What problem are we solving? Why now? What breaks if we don't
do this?

Be concrete. Cite the observed pain point, the scale at which it
bites, the consequence of leaving it unaddressed. Abstract
motivations ("we should have a cleaner architecture") rarely
survive scrutiny; specific motivations ("at 100k agents per node
the current shape blows the GC mark phase") do.>

## Detailed design

<The proposal itself. This is the long section.

Include:

- Code / interface shapes where applicable (inline Go, fenced
  with ```go).
- Diagrams (Mermaid or PlantUML inline) where the shape is
  non-textual.
- Concrete examples showing the proposal in use.
- Edge cases and error modes.
- Migration path if replacing existing behavior.

Be precise. Reviewers should be able to imagine the code merging
from this section alone.>

## Alternatives considered

<What else was evaluated, and why not?

For each alternative, give it a heading, summarize it fairly
(don't strawman), and explain why this RFC proposes the current
shape over it. "We didn't consider any alternatives" is almost
never true; if it's actually true, flag that as an open
question.

Example:

### A. <Alternative name>

<One-paragraph summary>

**Why not:** <Specific reason — cost, complexity, wrong shape
for the problem, already tried and failed, etc.>>

## Drawbacks

<Honest accounting of what this proposal costs.

Be specific. "Adds complexity" is not a drawback; "adds one
additional interface per plugin domain, roughly 5 new files in
the typical plugin" is. The goal is for reviewers to understand
what they're signing up for, not to minimize the cost.>

## Open questions

<Things the RFC explicitly does not answer, where discussion is
invited.

Phrase each as a question. The intent is that by the time the
RFC is Accepted, every open question has been resolved (either
answered in-line above, or explicitly deferred to future work).>

## Unresolved / future work

<Out-of-scope extensions or follow-ups that are noted but not
part of this proposal.

This section protects the RFC from scope creep. When a reviewer
says "this should also handle X," the author can move X here
rather than expanding the proposal.>

---

<!-- Terminal-state sections; add only when the RFC reaches a
     terminal status. -->

<!--
## Rejection (if status: Rejected)

<Why was this RFC rejected? Who decided, on what date?>

## Withdrawal (if status: Withdrawn)

<Why was this RFC withdrawn? Did the need go away? Did a
different approach (possibly another RFC) replace it?>
-->
