# internal/services

**Layer:** AI Application Surfaces  
**Status:** 🔲 SLOT (except codeassist, which is what cairo already is)

The AI application surfaces from the enterprise diagram. Each sub-package is a higher-level capability that composes the foundation layer (agent, llm, tools, learn) and exposes a specific surface to the enterprise UI.

## Sub-packages

| Package | Diagram surface | Status |
|---|---|---|
| `codeassist/` | Code Assistant | ✅ this is cairo today — slot formalizes it |
| `docqa/` | Document Q&A | 🔲 SLOT |
| `automation/` | Automation Hub | 🔲 SLOT |
| `analytics/` | Analytics & Insights | 🔲 SLOT |

## Notes

These packages are not separate binaries. They're capabilities registered with the enterprise control plane (`cmd/cairo-registry`) and made available to users based on their role. The enterprise UI presents them as tabs or options; the registry routes requests to the right agent or service handler.
