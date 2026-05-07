# internal/authn

**Layer:** Zero Trust — User Pillar  
**ZT Gate:** Identity verification  
**Status:** 🔲 SLOT

*Verify explicitly. Authenticate every request, not just login.*

This is the User gate. It verifies who is making a request and issues a scoped identity claim that downstream gates can validate. It does not grant access — that is `internal/access/`. It only answers: "Is this identity real and currently valid?"

## Zero Trust principles this gate enforces

- **Never trust, always verify** — tokens are validated on every request, not cached as "logged in"
- **Assume breach** — even a valid token is re-checked against revocation on sensitive operations
- **Least privilege** — identity claims are scoped (user ID, roles, department) — no "god token"

## Responsibilities

- Validate identity tokens from the upstream IDP (OIDC `id_token` verification: signature, expiry, audience)
- Extract identity claims: user ID, email, roles, department memberships
- Detect and reject revoked or expired sessions
- Issue session context that downstream gates (`internal/access/`) consume

## What this is NOT

Not an Identity Provider. Cairo does not issue identity. That is the upstream SSO provider's job (Okta, Azure AD, Ping, or any OIDC-compliant IDP). This package validates what the IDP issued.

## Gate position in the chain

```
[Blazor: SSO redirect + MFA] → [authn: token validation] → [access: policy decision] → ...
```

Blazor handles the user-facing SSO flow. `authn` handles the backend token validation. Together they form the User gate.

## Implementation path

1. **Now:** tsnet peer certificate → extract Tailscale user identity. Already implicit; make it explicit.
2. **Near-term:** OIDC `id_token` validation for the enterprise UI session.
3. **Enterprise:** SAML 2.0 assertion validation for IdP-initiated SSO (DoD PKI, CAC card).

Build when: the first enterprise SSO integration is configured.
