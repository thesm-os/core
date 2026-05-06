---
name: Feature request
about: Propose a new interface, capability, or improvement
title: "feat: "
labels: ["enhancement"]
assignees: []
---

## Problem

<!-- What problem are you trying to solve? Be concrete: cite the
     consumer code or use case where the current shape falls short. -->

## Proposed solution

<!-- Sketch the interface or behaviour you're proposing. Inline Go
     code is welcome. -->

```go
// proposed interface
```

## Alternatives considered

<!-- What other shapes did you consider, and why did you settle on the
     proposed one? -->

## Why this belongs in core

<!-- core is stdlib-only and a hard dependency of every thesmos
     library. Justify why this seam belongs here rather than in a
     consumer package.

     Reasons that typically qualify:
       - Multiple downstream libraries need the same seam.
       - The interface enables determinism (clock, rand, …).
       - The interface is part of a contract (reporter, logger, …).

     Reasons that typically do NOT qualify:
       - Single-consumer convenience.
       - Anything that requires a non-stdlib dependency.
       - Speculative future use ("we might want…"). -->

## Additional context

<!-- Links to related RFCs, ADRs, issues, or external references. -->
