# internal/protocol

**Layer:** Fleet  
**Status:** ✅ working — SINGLE SOURCE OF TRUTH

Wire types for the registry protocol: `RegisterRequest`, `RegisterResponse`, `Frame`, `HeartbeatPayload`.

Both the cairo client (`internal/registry/`) and the registry server (`internal/registryserver/`) import from here. This eliminates the copy-paste drift that existed between the two repos.

Source: `~/cairo/internal/protocol/`.
