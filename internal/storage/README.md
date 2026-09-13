# Storage backends

A storage backend is a place that keeps file contents. The server supports two types:

| Type | Settings | Encrypted settings |
| --- | --- | --- |
| `local` | `root` | None |
| `s3` | `endpoint`, `region`, `bucket`, `prefix`, `force_path_style` | `access_key_id`, `secret_access_key` |

The `Storage` OpenAPI tag contains these routes. Only administrators can use them.
Other users get status 403. Requests without a valid session get status 401.

| Method | Path | Result |
| --- | --- | --- |
| GET | `/api/v1/admin/storage-backends` | Return all backends, sorted by name, with status 200. |
| POST | `/api/v1/admin/storage-backends` | Create a backend. Return it with status 201. |
| GET | `/api/v1/admin/storage-backends/{id}` | Return one backend with status 200. |
| PUT | `/api/v1/admin/storage-backends/{id}` | Replace the name, enabled flag, and settings. Return the backend with status 200. |
| DELETE | `/api/v1/admin/storage-backends/{id}` | Delete a backend. Return status 204. |
| POST | `/api/v1/admin/storage-backends/check` | Check the connection with settings that are not saved. Return the result with status 200. |
| POST | `/api/v1/admin/storage-backends/{id}/check` | Check the connection of a saved backend. Return the result with status 200. |

Backend IDs have the `stb_` prefix. Names are unique. The comparison ignores case.
A request body contains `local` for a local backend or `s3` for an S3 backend. It cannot contain both.
The type cannot change after creation. Create a new backend instead.

## Validation

- `local.root` must be an absolute path below the file system root. The server removes `.` and `..` segments.
- `s3.endpoint` is empty for AWS. Otherwise it is an HTTP or HTTPS URL without a path, query, or credentials.
- `s3.region` contains lowercase letters, digits, and hyphens.
- `s3.bucket` follows the S3 bucket name rules: 3 to 63 lowercase letters, digits, dots, and hyphens.
- `s3.prefix` has no empty, `.`, or `..` segments. The server removes leading and trailing slashes.
- A new S3 backend requires both credentials. On update, send both credentials or neither.
  Empty credentials keep the stored credentials. When the endpoint changes, send both credentials again.
  Requests to a service contain the access key ID, so stored credentials never go to a new endpoint.

Validation errors return status 422. Error details never contain request values.

## Encryption

The server encrypts the credentials with `APP_KEY` before it writes them to the `encrypted_secrets` column.
Responses never contain credentials. The server logs never contain credentials.
The `internal/platform/crypt` package uses AES-256-GCM with a random nonce for each value.

Generate a key with `make key-generate`. Keep the key with your database backups.
The server cannot decrypt stored credentials without the key.

To rotate the key:

1. Move the current key to `APP_PREVIOUS_KEYS`. Separate keys with commas.
2. Set a new `APP_KEY`, and restart the server.
3. Save each S3 backend again. The server encrypts the credentials with the new key.
4. Remove the old key from `APP_PREVIOUS_KEYS`.

If an update cannot decrypt the stored credentials, it returns status 422. Enter the credentials again.

## Connection checks

A check writes a small object, reads it, compares the content, and deletes it.
The object name starts with `.stocat-check-`. S3 checks put the object below the prefix.
A check stops after 15 seconds.

A completed check returns status 200 with `ok` and `message`. The message tells the administrator what to correct,
for example "The bucket does not exist." It never contains credentials. The server logs the full error at the warning level.
Invalid settings return status 422 before the check starts.

`POST /admin/storage-backends/check` accepts `type`, `local`, and `s3`. Add `id` to test changes to a saved backend.
With `id`, empty S3 credentials use the stored credentials, if the endpoint does not change.

The S3 client uses only the backend settings. It ignores the AWS environment variables and configuration files of the server.
It sends checksums only when S3 requires them, because many S3-compatible services reject the default checksums.
The server does not limit the endpoints that an administrator can check.

## Limits

File operations do not use backends yet.
Before libraries use backends, prevent deletion of a backend that stored data uses.
