---
slug: users-auth-2fa
section: operations
order: 10
title: Users, External Auth (AD/LDAP, Keycloak), and 2FA
summary: Local accounts, password policy, TOTP two-factor authentication with trusted browsers, and corporate sign-in via Active Directory and Keycloak.
keywords: [users, passwords, 2fa, totp, ldap, active directory, keycloak, oidc, security, omb-controlctl]
---

Access control is managed across [System → Users](/users), [System → Integrations](/integrations), and [Account](/account). All authentication mechanisms (LDAPv3, OIDC/JWT, TOTP, SVG QR encoder) are implemented inside OMB with no external dependencies.

## Local accounts and password policy

- Usernames are validated against `^[A-Za-z0-9_.-]{3,32}$`; passwords must be at least 12 characters long (the built-in password generator uses simple special characters `!@#$%^&*-_+=`).
- The **“Security policy”** dialog on `/users` configures maximum password age (`max_age_days`, `0–3650`) and warning period (`warn_days`, `0–90`). Individual users can be set via the `☰` menu to “Follow global policy”, “Never expires”, or “Custom age”.
- On first login, after an administrator password reset, or upon expiration, the session is restricted and redirects to the isolated `/change-password` page until a new password is set.

## Two-factor authentication (2FA / TOTP)

Local accounts support RFC 6238 TOTP 2FA (SHA-1, 6 digits, 30 s step, ±1 window, replay protection, and a rate limit of 5 attempts per 5 min):

- **2FA policy** (under “Security policy” on `/users`): `Off` (`off`), `Required for administrators` (`admins`), or `Required for all` (`all`). When required, users are routed to the isolated `/setup-2fa` wizard on sign-in.
- **Setup in `/account`:** built-in QR code (SVG with issuer `oh my boot_`) and **8 single-use recovery codes** formatted as `XXXX-XXXX` (stored only as SHA-256 hashes).
- **Trusted browsers:** on `/login/2fa`, users can check “Trust this browser” to receive a hardened `omb_2fa_trust` cookie (`HttpOnly; Secure; SameSite=Lax`) valid for 365 days with sliding renewal. Subsequent logins from that browser skip the 2FA step. The trusted browser count and an instant revoke button are available in [Account](/account); disabling or resetting 2FA automatically revokes all trusted browsers.

## External sign-in: Active Directory (LDAP) and Keycloak (OIDC)

Configured by an administrator under [System → Integrations](/integrations). Integration secrets (bind password, client secret) are encrypted with `/etc/omb/secrets.key` (AES-256-GCM).

### Direct Active Directory / LDAP

- Connects over **LDAPS** (`:636`) or **StartTLS** (`:389`) with strict corporate CA verification.
- Uses a read-only service bind account, tracks users by immutable `objectGUID`, and supports nested AD groups (`1.2.840.113556.1.4.1941`).
- An external account appears in `/users` only after the user's first successful sign-in with an authorized group.

### External Keycloak (OIDC)

- Supports OIDC Discovery, PKCE (`S256`), JWKS token signature verification (`RS256`, `ES256`, `ES384`), Admin REST API sync, and RP-Initiated plus Back-Channel Logout with `jti` replay protection.

### External session security

- **Hybrid permission checks:** 30 s cache for read-only requests (`GET`) and a mandatory live check against AD/Keycloak with no cache on every mutating action (`POST`). If a user is disabled in the directory or removed from a group, write access is revoked immediately.
- **Local SMB/SFTP for external users:** a background check runs every 15 min and revokes file server access if an external account is disabled or loses its role.
- **Last local administrator protection:** password and 2FA policies apply to local accounts only, and the last active local `admin` cannot be deleted or disabled — emergency panel access remains available even during a directory outage.

## Emergency `omb-controlctl` commands

If a password or 2FA device is lost, use the server CLI utility:

```bash
# Create a new local administrator (password is read from stdin)
sudo -u omb-control omb-controlctl create-admin -username recovery-admin

# Reset a local user's password
sudo -u omb-control omb-controlctl reset-password -username admin

# Reset 2FA and revoke all trusted browsers for a user
sudo -u omb-control omb-controlctl reset-2fa -username admin
```
