# Uploads

Create each upload through `POST /api/v1/uploads`. Use the returned URL for tus `HEAD` and `PATCH` requests.

Send `Tus-Resumable: 1.0.0` with each tus request. Send patches with `application/offset+octet-stream`.

The server stores incoming bytes in `UPLOAD_STAGING_DIR`. It reserves the declared size before it creates a session.

If the declared size does not fit in the storage quota of the user on the library backend, the server returns status 413. Refer to `internal/quota`.

Plain uploads enter finalization when the last patch commits. Encrypted uploads wait for `POST /api/v1/uploads/{id}/complete`.

River runs publication jobs from PostgreSQL. Run `make migrate-up` to install both application and River migrations.

The E2EE object format uses this 32-byte header:

```text
STOCAT01 | version:1 | reserved:3 | frame_size:u32 | plaintext_size:u64 | nonce_prefix:8
```

Use big-endian integers. Encrypt each frame with AES-256-GCM. Build its nonce from the eight-byte prefix and frame index.

Authenticate the header, frame index, and plaintext frame length as additional data. Keep the file key in its library envelope.
