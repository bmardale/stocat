# Authentication

The `Auth` OpenAPI tag contains these routes:

| Method | Path | Result |
| --- | --- | --- |
| GET | `/api/v1/auth/config` | Return public registration settings. |
| POST | `/api/v1/auth/register` | Create a user and a session. Return the user with status 201. |
| POST | `/api/v1/auth/login` | Create a session. Return the user with status 200. |
| GET | `/api/v1/auth/me` | Require a valid session. Return the user with status 200. |
| POST | `/api/v1/auth/logout` | Delete the current session and clear its cookie. Return status 204. |
| GET | `/api/v1/auth/sessions` | Require a valid session. Return the active sessions of the user with status 200. |
| DELETE | `/api/v1/auth/sessions` | Require a valid session. Delete all other sessions of the user. Return status 204. |
| DELETE | `/api/v1/auth/sessions/{id}` | Require a valid session. Delete one session of the user. Return status 204. |
| POST | `/api/v1/auth/passkeys/login/options` | Start a passkey sign-in. Return the request options with status 200. |
| POST | `/api/v1/auth/passkeys/login` | Verify a passkey and create a session. Return the user with status 200. |
| GET | `/api/v1/auth/passkeys` | Require a valid session. Return the passkeys of the user with status 200. |
| POST | `/api/v1/auth/passkeys/registration/options` | Require a valid session and the password. Return the creation options with status 200. |
| POST | `/api/v1/auth/passkeys` | Require a valid session. Verify and store a passkey. Return it with status 201. |
| PATCH | `/api/v1/auth/passkeys/{id}` | Require a valid session. Rename one passkey of the user. Return it with status 200. |
| DELETE | `/api/v1/auth/passkeys/{id}` | Require a valid session. Delete one passkey of the user. Return status 204. |

Registration requires `name`, `email`, and `password`. Invite-only registration also requires `invite_code`.
Login requires `email` and `password`.
Email comparison ignores case. Registration and login remove leading and trailing spaces from email addresses.
Registration stores email addresses in lowercase.
Passwords require at least 15 characters and at most 1024 bytes. Passwords retain spaces and case.
Argon2id uses 19 MiB, two iterations, one thread, and a random 16-byte salt.

User responses contain `id`, `name`, `email`, and `is_admin`. The `id` field contains the public user ID.
Responses omit the internal user ID and password hash.

The `stocat_session` cookie contains a random 256-bit token.
The database stores its SHA-256 hash, user, user agent, connection IP, creation time, and expiry time.
The database stores at most 512 bytes of the user agent. The server replaces invalid UTF-8 in the user agent.
The server ignores forwarded IP headers. The IP field is null when the connection address is unavailable.
Sessions expire after 30 days. Requests do not extend this period.
Login and registration replace the session presented by the current cookie.
Logout succeeds when the cookie is missing, invalid, expired, or already revoked.
Logout preserves sessions from other devices.

## Session management

Each session has a public ID with the `ses_` prefix. Responses never contain the token or its hash.
Session responses contain `id`, `user_agent`, `ip_address`, `created_at`, and `current`.
The `current` field is true for the session that sent the request.
The list contains only sessions that have not expired. The current session is first. Newer sessions come next.
The server stores the raw user agent. Clients parse it for display.

`DELETE /auth/sessions` keeps the current session. Use logout to also delete the current session.
`DELETE /auth/sessions/{id}` returns status 404 when the session does not exist or belongs to another user.
When the ID identifies the current session, the response also clears the cookie.

Cookies use `HttpOnly`, `SameSite=Lax`, and `Path=/`. The server configuration controls `Secure`.
The server rejects cross-origin write requests through `http.CrossOriginProtection`.

## Passkeys

Passkeys use WebAuthn discoverable credentials. The server requires user verification for registration and sign-in.
The server requests no attestation. It accepts any authenticator that verifies the user.

Set these variables to configure the relying party:

| Variable | Default | Value |
| --- | --- | --- |
| `WEBAUTHN_RP_ID` | `localhost` | The domain of the site, without a scheme or port. |
| `WEBAUTHN_RP_NAME` | `Stocat` | The name that authenticators show. |
| `WEBAUTHN_RP_ORIGINS` | `http://localhost:5173` | A comma-separated list of the origins that serve the web app. |

The RP ID must equal the host of each origin or a registrable domain suffix of that host.
The server stores the RP ID with each passkey. A change to the RP ID hides all earlier passkeys.
When the auth configuration has no RP ID, passkey ceremonies return status 503.

Each ceremony has two requests. The options request returns JSON for `PublicKeyCredential.parseCreationOptionsFromJSON()`
or `PublicKeyCredential.parseRequestOptionsFromJSON()`. Send the `toJSON()` value of the credential in the second request.
The server stores each challenge for 5 minutes. The second request deletes the challenge before verification.
Thus each challenge allows one attempt. Registration challenges belong to the user that requested them.

Registration requires the current password, so a stolen session cannot add a persistent credential.
The server excludes the existing passkeys of the user from registration.
Each user has one random 64-byte WebAuthn user handle. The handle contains no account data.

Passkey sign-in creates a session in the same way as password sign-in.
Sign-in fails with status 401 when the sign counter does not increase. This can indicate a cloned authenticator.
Authenticators that always report a zero counter, such as synced passkeys, pass this check.
Passkey responses contain `id`, `name`, `created_at`, `last_used_at`, and `synced`.
The `synced` field is true when the authenticator backs up the passkey.

## Rate limits

The server uses token buckets. Each bucket starts full and restores tokens continuously.
Each accepted request consumes one token. Rejected requests do not extend the bucket's recovery time.

| Requests | Identity | Tokens per minute | Burst |
| --- | --- | --- | --- |
| All requests except GET health probes | Connection IP | 300 | 60 |
| Login, registration, and passkey sign-in, combined | Connection IP | 20 | 10 |
| Registration | Connection IP | 5 | 3 |
| Login | Normalized email and connection IP | 5 | 5 |
| Login | Normalized email across all IPs | 30 | 15 |
| Registration | Normalized email | 1 | 2 |
| Protected routes, combined | Verified user ID | 120 | 30 |

All applicable limits must allow the request. Burst capacity specifies how many requests can arrive together.
The rates specify continuous token recovery. They do not specify fixed windows.
Auth policies live in `internal/auth/auth.go`. The general IP policy lives in `internal/server/server.go`.
Each policy specifies a token interval and burst capacity. Invalid values cause a startup error.

IP checks run before body parsing, session queries, and password hashing.
Malformed login and registration requests consume IP tokens.
Email checks run after schema validation and before database queries or password hashing.
Registration also validates the name and password length before it consumes an email token.
These checks count successful and failed attempts. They also count attempts for email addresses without accounts.
Login and registration use separate email buckets. These buckets store hashes of normalized email addresses.
Login checks the email-and-IP bucket and the shared email bucket together.
One IP cannot consume shared email tokens faster than the shared bucket restores them.
Clients on the same IP or IPv6 /64 share the stricter bucket for each email.
Distributed attackers can still exhaust the shared email budget and prevent login while they sustain the attack.
Rejected attempts do not revoke existing sessions.

Administrators can change registration access at `/api/v1/admin/config`.
They can create, list, and revoke invite codes at `/api/v1/admin/invite-codes`.
Each invite code can register one account.

Protected routes share each user's limit across sessions, IP addresses, and child groups.
Repeated `Protected` calls within nested groups reuse the verified user and consume one user token per request.
The server uses the verified user ID because an email address can change.
The general IP limit also applies to authenticated requests and invalid session tokens.
`/api/v1/auth/me` uses the general IP and user limits. Logout uses only the general IP limit.
Login throttling does not prevent logout unless the general IP limit also rejects the request.

The server returns status 429 with the standard problem body and a `Retry-After` header in seconds.
These responses prohibit caching. Auth and protected routes document this response in OpenAPI.
Buckets checked together consume tokens only when all of them allow the request.
The retry delay is the longest delay among these rejecting buckets. Other traffic or a later check can require a longer delay.

The server groups IPv6 addresses by /64. IPv4 and IPv4-mapped IPv6 addresses share the same identity.
Unavailable connection addresses share one fallback bucket.
The server ignores `Forwarded`, `X-Forwarded-For`, and `X-Real-IP`.
Clients behind one reverse proxy share its connection IP limit.
Before deployment behind a proxy, configure verified client addresses at the edge or add explicit trusted-proxy support.
Do not trust client-supplied forwarding headers.

Each bucket table holds at most 50,000 identities. Each request removes entries whose tokens have fully recovered.
A heap orders entries by recovery time. A full table evicts the earliest entry to admit a new identity.
This prevents table capacity from blocking every new client. It also removes the shared cleanup countdown.
An evicted identity starts with a full bucket when it returns. Sustained identity rotation can therefore weaken rate enforcement.
Later recovery times preserve heavily used identities longer. This policy favors client access when the table fills.
Each server process owns its tables; restarts clear them.
Before running multiple replicas, replace the tables with shared atomic counters or enforce equivalent limits at the edge.
Tests inject `RateLimitClock` through auth and server configuration. Production uses `time.Now` when this field is nil.

## Protect other routes

Create one auth service when you build the server. Register public routes on `api`.
Register private routes on a group from `Protected`:

```go
authService, err := auth.New(pool, auth.Config{SecureCookies: true, Logger: log})
if err != nil {
    return err
}
authService.Register(api)

private := authService.Protected(api)
files := huma.NewGroup(private, "/files")
```

The group verifies sessions before handlers run. It also documents cookie authentication and authentication errors in OpenAPI.
Child groups inherit authentication. Add permission middleware to child groups, or check resource permissions in their handlers.
Use `Admin` instead of `Protected` for routes that only administrators can use:

```go
storageService.Register(authService.Admin(api, "/admin"))
```

The admin group adds the checks of `Protected`. It returns status 403 when `is_admin` is false.
Use `auth.UserFromContext(ctx)` to read the user. Its internal `ID` supports database queries and never enters JSON responses.
The context user contains no password hash. Each request loads current user data from the database.
