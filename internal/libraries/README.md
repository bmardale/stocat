# Libraries

Each library belongs to one user and uses one enabled storage backend.
The service never returns a library to another user.

Use these authenticated routes:

| Method | Path | Result |
| --- | --- | --- |
| GET | `/api/v1/libraries` | List the current user's libraries. |
| POST | `/api/v1/libraries` | Create a library and its root folder. |
| GET | `/api/v1/libraries/{id}` | Get one library. |
| POST | `/api/v1/libraries/{id}/folders` | Create a folder. |
| GET | `/api/v1/libraries/{id}/nodes` | List direct children of a folder. |

Choose `none` or `e2ee` when you create a library.
You cannot change this choice.

For an encrypted library, send the wrapped library key as `key_envelope`.
Send folder names as `encrypted_name`.
Send a 32-byte `name_token` with each encrypted name.
Compute the token with a keyed hash that includes the parent identifier.
The token lets PostgreSQL reject sibling name collisions without seeing the name.

The server stores key envelopes and encrypted names as opaque bytes.
The server does not receive plaintext library keys or encryption passphrases.

Folder listings use an opaque cursor and a maximum page size of 200 items.
