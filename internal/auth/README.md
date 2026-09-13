# Authentication

The `Auth` OpenAPI tag contains four routes:

| Method | Path | Result |
| --- | --- | --- |
| POST | `/auth/register` | Create a user and a session. Return the user with status 201. |
| POST | `/auth/login` | Create a session. Return the user with status 200. |
| GET | `/auth/me` | Require a valid session. Return the user with status 200. |
| POST | `/auth/logout` | Delete the current session and clear its cookie. Return status 204. |

Registration requires `name`, `email`, and `password`. Login requires `email` and `password`.
Email comparison ignores case. Registration and login remove leading and trailing spaces from email addresses.
Registration stores email addresses in lowercase.
Passwords require at least 15 characters and at most 1024 bytes. Passwords retain spaces and case.
Argon2id uses 19 MiB, two iterations, one thread, and a random 16-byte salt.

User responses contain `public_id`, `name`, `email`, `created_at`, and `is_admin`.
Responses omit the internal user ID and password hash.

The `stocat_session` cookie contains a random 256-bit token.
The database stores its SHA-256 hash, user, user agent, connection IP, creation time, and expiry time.
The database stores at most 512 bytes of the user agent. The server replaces invalid UTF-8 in the user agent.
The server ignores forwarded IP headers. The IP field is null when the connection address is unavailable.
Sessions expire after 30 days. Requests do not extend this period.
Login and registration replace the session presented by the current cookie.
Logout succeeds when the cookie is missing, invalid, expired, or already revoked.
Logout preserves sessions from other devices.

Cookies use `HttpOnly`, `SameSite=Lax`, and `Path=/`. The server configuration controls `Secure`.
The server rejects cross-origin write requests through `http.CrossOriginProtection`.

## Protect other routes

Create one auth service when you build the server. Register public routes on `api`.
Register private routes on a group from `Protected`:

```go
authService := auth.New(pool, auth.Config{SecureCookies: true, Logger: log})
authService.Register(api)

private := authService.Protected(api)
files := huma.NewGroup(private, "/files")
```

The group verifies sessions before handlers run. It also documents cookie authentication and authentication errors in OpenAPI.
Child groups inherit authentication. Add permission middleware to child groups, or check resource permissions in their handlers.
Use `auth.UserFromContext(ctx)` to read the user. Its internal `ID` supports database queries and never enters JSON responses.
The context user contains no password hash. Each request loads current user data from the database.
