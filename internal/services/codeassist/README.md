# internal/services/codeassist

**Layer:** AI Application Surfaces  
**Status:** ✅ this is what cairo already does — slot formalizes it

Code review, refactoring, generation, and explanation. The core developer-facing capability.

In standalone mode: accessed via TUI, HTTP API, or VS Code extension.  
In enterprise mode: users select "Code Assistant" from the enterprise UI, which routes their session to their personal cairo agent (or a dept agent if they select one).

No new code needed here initially — this package is a conceptual slot that names what already exists.
