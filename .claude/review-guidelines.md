# PTD Code Review Guidelines

## Reviewer Responsibilities

Your job is to review the **code itself**, not just the description or intent. In a world of agentic coding, the author may not have hand-written every line. That means:

- Read the actual diff, not just the PR summary
- Verify the code does what the description claims
- Look for correctness issues, not just style preferences

### What to Focus On

**Correctness and safety:**
- Does the code handle edge cases?
- Are error paths handled appropriately?
- Could this introduce security issues (credential handling, injection, RBAC)?

**Code quality fundamentals:**
- **Encapsulation**: Is internal state properly hidden? Are interfaces clean?
- **DRY**: Is there duplicated logic that should be extracted? But don't over-abstract — three similar lines can be better than a premature helper
- **Naming**: Do names reveal intent? Would a future reader understand this?
- **Complexity**: Is this more complicated than it needs to be?

**Patterns and consistency:**
- Does new code follow the existing patterns in the codebase?
- Are Pulumi resources structured consistently with existing ones?
- Are Go and Python conventions followed?

### What NOT to Focus On

- Style issues handled by formatters (`just format`)
- Personal preferences without clear benefit
- Theoretical concerns without concrete impact
- Micro-optimizing code that runs once during infrastructure provisioning

## Comment Format

Use clear, actionable language:
- **Critical**: "This will break X because Y. Consider Z."
- **Important**: "This pattern differs from existing code in A. Recommend B for consistency."
- **Suggestion**: "Consider X for improved Y."

## Self-Review Norm

PR authors are expected to review their own diff before opening the PR and add inline comments to draw attention to:
- Lines they don't fully understand
- Areas of concern or uncertainty
- Key decision points reviewers should weigh in on

This balances effort between author and reviewer. If the author hasn't commented their PR, ask them to.
