---
written_by: ai
---

# Product language ownership

`HumanCopy.swift` contains text owned by Josh. Agents must never edit its
strings unless Josh directly supplies or approves the exact replacement.
Implementation authority, copy review, grammar correction, test repair and
requests to make text clearer do not authorise changes. The file must remain
tracked and committed.

`AgentPrompts.swift` contains prompts for AIs. Every prompt must follow the
official GPT-5.6 prompting guide and state its actual intent inside the prompt.
Prompts are judged by whether an AI can understand and perform the intended
task. Do not add vague, decorative or misleading prompts.

All product and technical writing follows ADS-STE100 Simplified Technical
English or the GOV.UK style guide. Use whichever rule makes the text clearer.
Put the decision or action first. Use concrete words, active voice and the same
term for the same thing. In product text, use "app" and "AI". Do not use
"source", "crawler", "connector" or "coding agent".

Operational errors must say what happened, what still works and what the
person can do. Do not expose raw paths, commands or implementation errors as
the primary message.
