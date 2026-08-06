# The execution boundary ships with pooled compute or pooled compute does not ship

The realistic attacker is one of our own agents steered by content it read, not a malicious colleague. The boundary: workers fetch and execute only `TrustedRef`s (protected branches, reviewed PR heads of allow-listed repos) and push only `WorkspaceRef`s, which are never claimable; agent-authored offers need human approval bound to the offer's content hash; execution is containerized from day one with egress pinned to package registries, the tracker/git APIs, and a named allow-list of LLM provider endpoints; the offer body is data, never instructions.

## Consequences

Repo-granularity allow-listing was rejected: an agent with commit access can push a branch whose test files or lifecycle hooks are the attack, so trust lives at ref granularity. The LLM egress lane is the boundary's soft spot by construction (a repo-reading agent that reaches an arbitrary endpoint can exfiltrate through prompts) — "open egress for agent tasks" is the one shortcut that silently removes the boundary. Rules are carried by types (`ClaimableOffer` has no public constructor), so there is no partial version to accidentally ship.
