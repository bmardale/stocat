# Quotas

A quota limits the bytes that a user stores on one storage backend.
Each backend has a separate quota. Usage on one backend does not change the quota on a different backend.

The server finds the limit in this order:

1. The override of the user for the backend.
2. The default quota of the user.
3. The global default quota.

Each level contains a limit in bytes or no limit.
A user level without a value uses the next level. The global default quota starts with no limit.

## Usage

Usage on a backend is the sum of these values:

- The stored size of each blob in the libraries of the user on the backend. This includes old versions and trashed files.
- The declared size of each unfinished upload to these libraries.

The stored size is the size after deduplication and encryption. Trashed files count until the server purges them.

## Admission

The server checks the quota when it creates an upload session.
If the declared size does not fit, the server returns status 413.
When an administrator lowers a quota, stored files stay. The server rejects only new uploads.

## Routes

| Method | Path | Access | Result |
| --- | --- | --- | --- |
| GET | `/api/v1/storage-usage` | User | List usage and the limit on each backend that a library of the user uses. |
| GET | `/api/v1/admin/quota` | Administrator | Get the global default quota. |
| PUT | `/api/v1/admin/quota` | Administrator | Set the global default quota. |
| GET | `/api/v1/admin/users` | Administrator | List users with their default quotas and backend overrides. |
| PUT | `/api/v1/admin/users/{id}/quota` | Administrator | Replace the default quota and all backend overrides of a user. |

A quota has a `mode`: `inherit`, `unlimited`, or `limited`. Send `limit_bytes` only with `limited`.
The global default quota does not accept `inherit`. A backend override with `inherit` removes the override.
