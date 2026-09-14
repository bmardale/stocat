# Encrypted sharing: phase 1–2 record contract

This document defines the server-side binary records for account encryption and owned v2 content.
The format identifier is v2. The suite identifier is 1.
Sharing handlers, recipient envelopes, public links, browser cryptography, and rotation clients remain future work.

## Encoding

Use the field order in the tables below.
Encode each byte string with a four-byte unsigned big-endian length, followed by its bytes.
Encode each counter as an eight-byte unsigned big-endian integer.
Counters must be between 1 and 9,223,372,036,854,775,807, inclusive.
File sizes and frame indices follow the separate framing rules below.
Encode the suite as one byte.
Encode optional fields with a one-byte presence flag, then the value when present.
The presence flag must be 0 or 1. An absent value has no length prefix.
Reject unknown types, unknown suites, invalid lengths, noncanonical values, missing fields, and trailing bytes.

Every record starts with these fields:

| Field | Encoding |
| --- | --- |
| Domain | Byte string: UTF-8 `stocat/v2/` followed by the record type. |
| Deployment | Byte string: 16 bytes from the installation UUID, in network byte order. |
| Suite | One byte: `01`. |

The tables use these types:

| Type | Meaning |
| --- | --- |
| `user` | Canonical `usr_` public identifier. |
| `library` | Canonical `lib_` public identifier. |
| `node` | Canonical `nod_` public identifier. |
| `version` | Canonical `ver_` public identifier. |
| `counter` | Positive counter with the range specified above. |
| `bytes(N)` | Byte string of exactly N bytes. |
| `ciphertext(N)` | Byte string of 16 through N bytes, including the authentication tag. |
| `optional(T)` | Presence flag followed by T when present. |

Public identifiers contain their prefix, an underscore, and 26 uppercase Crockford base32 ULID characters.
Encode identifiers as UTF-8 byte strings. Never use database row identifiers in records.

The Go `Record` representation stores binary fields as unpadded base64url strings.
It stores counters as canonical decimal strings. It omits absent optional fields.
Resource identifiers and profile identifiers remain strings.
This representation supports fixtures and internal callers. It does not define an HTTP request format.
Future JSON handlers must reject duplicate fields before constructing a `Record`.

Limit complete key records to 16 KiB.
Limit node metadata and contact-store ciphertext to 64 KiB.
Limit their complete records to 64 KiB plus 2,048 bytes for public fields.
Validate limits before allocation or cryptographic processing.

## Account records

Each table row gives the exact field sequence after the common prefix.
The `generation` field identifies the account identity generation in these records.
The `bundle_revision` field identifies the envelope revision within that generation.

| Record type | Ordered fields |
| --- | --- |
| `identity` | `account_id:user`, `generation:counter`, `signing_public_key:bytes(32)`, `recipient_public_key:bytes(32)`, `recipient_key_id:bytes(32)` |
| `identity-continuity` | `account_id:user`, `previous_generation:counter`, `generation:counter`, `previous_identity_hash:bytes(32)`, `identity_hash:bytes(32)` |
| `contact-store` | `account_id:user`, `generation:counter`, `revision:counter`, `previous_revision_hash:optional(bytes(32))`, `nonce:bytes(24)`, `ciphertext:ciphertext(65536)` |
| `account-password` | `account_id:user`, `generation:counter`, `bundle_revision:counter`, `profile:string`, `salt:bytes(16)`, `nonce:bytes(24)`, `ciphertext:bytes(48)` |
| `account-recovery` | `account_id:user`, `generation:counter`, `bundle_revision:counter`, `nonce:bytes(24)`, `ciphertext:bytes(48)` |
| `account-private` | `account_id:user`, `generation:counter`, `bundle_revision:counter`, `nonce:bytes(24)`, `ciphertext:ciphertext(16384)` |

The only password profile is `argon2id-v19-m65536-t3-p4-l32`.
It specifies Argon2id version 19, 65,536 KiB, three passes, four lanes, and 32 output bytes.
Reject other profile strings before running the KDF.
Password and recovery envelopes encrypt exactly the 32-byte account master key.
The private envelope contains the encrypted private-key bundle. Never include the recovery secret in that bundle.

Hash the complete canonical identity record with SHA-256 to produce its fingerprint.
Exclude the detached signature from this hash.
Continuity records bind the hashes of both complete identity records.
Verify continuity with the previous identity's signing key.
The owner signs contact-store records and retains authenticated revision checkpoints.
Application validation must check revision continuity and public-key validity before accepting identities.
The binary parser checks encodings and lengths; it does not establish identity trust.

## Identity keys

Derive `recipient_key_id` as follows:

```text
SHA256(C("stocat/v2/recipient-key-id", deployment, account_id, generation, recipient_public_key))
```

The generation is a counter. The other values are length-prefixed byte strings.
An identity record must use this value.

Accept a signing public key only when it is a canonical Ed25519 point.
Reject the key if the cofactor multiple of the point is the identity point.
Accept a recipient public key only when its little-endian value is less than 2^255 - 19.
Reject the key if an X25519 exchange with it gives an all-zero shared secret.
`ValidateSigningPublicKey` and `ValidateRecipientPublicKey` apply these rules.

## Node and version records

The `account_id` field identifies the owner.
The `generation` field identifies the visible encryption generation, except in `owner-root`.
In `owner-root`, `generation` identifies the account identity generation that owns the wrapping key.
Epochs identify node keys. Revisions identify signed resource changes.

| Record type | Ordered fields |
| --- | --- |
| `owner-root` | `account_id:user`, `library_id:library`, `node_id:node`, `generation:counter`, `node_epoch:counter`, `revision:counter`, `nonce:bytes(24)`, `ciphertext:bytes(48)` |
| `parent-envelope` | `account_id:user`, `library_id:library`, `parent_id:node`, `child_id:node`, `parent_epoch:counter`, `child_epoch:counter`, `generation:counter`, `revision:counter`, `nonce:bytes(24)`, `ciphertext:bytes(48)` |
| `node-metadata` | `account_id:user`, `library_id:library`, `node_id:node`, `node_epoch:counter`, `revision:counter`, `nonce:bytes(24)`, `ciphertext:ciphertext(65536)` |
| `file-key` | `account_id:user`, `library_id:library`, `node_id:node`, `version_id:version`, `content_id:bytes(16)`, `node_epoch:counter`, `generation:counter`, `nonce:bytes(24)`, `ciphertext:bytes(48)` |
| `version-manifest` | `account_id:user`, `library_id:library`, `node_id:node`, `version_id:version`, `content_id:bytes(16)`, `node_epoch:counter`, `revision:counter`, `header:bytes(64)`, `ciphertext_hash:bytes(32)`, `key_envelope_hash:bytes(32)` |
| `current-version` | `account_id:user`, `library_id:library`, `node_id:node`, `node_epoch:counter`, `revision:counter`, `version_id:optional(version)` |

Owner-root and parent envelopes encrypt exactly one 32-byte node key.
File-key envelopes encrypt exactly one 32-byte content key.
Reject parent envelopes whose parent and child identifiers are equal.
The database must also enforce that both nodes belong to the envelope's library.
Application validation must verify the expected parent edge and resource context.

The metadata record encrypts the node name and its confidential attributes.
Use the same record type for library root metadata.
Store no plaintext library title for v2. Duplicate encrypted titles are permitted.
An absent current version represents an empty file node before initial publication.
The signed revision still advances when the current version changes.

The manifest header must pass framing validation.
Its content identifier must equal the manifest's content identifier.
The ciphertext hash covers the entire stored file, including its header.
The key-envelope hash covers the complete canonical `file-key` record, excluding its detached signature.
Verify the manifest and current-version record before using a version key.

## Authentication and derivation context

For encrypted records, use all bytes before the final ciphertext length prefix as AEAD additional data.
This includes the domain, deployment, suite, resource identifiers, counters, password parameters, and nonce.
`Record.AssociatedData` returns these bytes.
Authenticate ciphertext with XChaCha20-Poly1305. Never reuse a nonce with the same key.

Store signatures separately as exactly 64 bytes.
Construct the Ed25519 signature input as:

```text
C("stocat/v2/signature", record_type, SHA256(record_bytes))
```

Here, `record_type` is the table's type string, without the domain prefix.
Each of the three values is a length-prefixed byte string.
`SignatureInput` constructs this input. `VerifyRecord` verifies the detached signature with the supplied signing key.
The caller must select a trusted signing key and compare the authenticated context with the requested resource.
An identity signs itself. The owner signs content records and account changes.
A valid signature alone does not authorize a request or establish first-contact trust.

Use SHA-256 HKDF with a 32-byte zero salt and a 32-byte output, unless password derivation supplies the key directly.
Use these exact derivation contexts:

| Key purpose | Input key | HKDF info tuple |
| --- | --- | --- |
| Recovery envelope | Recovery secret | `C("stocat/v2/account-wrap", deployment, account_id, generation, "recovery")` |
| Private-key envelope | Account master key | `C("stocat/v2/account-wrap", deployment, account_id, generation, "private")` |
| Contact store | Account master key | `C("stocat/v2/account-wrap", deployment, account_id, generation, "contacts")` |
| Owner root | Account master key | `C("stocat/v2/account-wrap", deployment, account_id, generation, "root", library_id, node_id, node_epoch)` |
| Node metadata | Node key | `C("stocat/v2/node-metadata", deployment, library_id, node_id, node_epoch)` |
| Child envelope | Parent node key | `C("stocat/v2/child-wrap", deployment, library_id, parent_id, parent_epoch)` |
| File-key envelope | File node key | `C("stocat/v2/file-wrap", deployment, library_id, node_id, node_epoch)` |
| Name token | Parent node key | `C("stocat/v2/name-token", deployment, library_id, parent_id, parent_epoch)` |

Use the Argon2id output directly as the password-envelope key.
For name tokens, compute HMAC-SHA-256 over `C(parent_id, parent_epoch, NFC(name))`.
The derivation tuples use the same byte-string and counter encodings as records.
Derivation and AEAD implementations remain browser work. These tuples fix their context encoding in advance.

## File framing

The 64-byte header contains these fields without tuple length prefixes:

```text
magic:8 = STOCAT02
version:u8 = 2
suite:u8 = 1
reserved:2 = 0
frame_size:u32
plaintext_size:u64
content_id:16
nonce_prefix:16
reserved:8 = 0
```

Use unsigned big-endian integers.
Registered frame sizes are powers of two from 65,536 through 8,388,608 bytes.
Use 8,388,608 bytes for new files.
The frame count is `max(1, ceil(plaintext_size / frame_size))`.
The ciphertext size is `64 + plaintext_size + 16 * frame_count`.
Both sizes must be at most 9,007,199,254,740,991.
Reject invalid sizes before arithmetic, serialization, or parsing completes.

A frame nonce is `nonce_prefix || frame_index:u64`.
A frame index starts at zero and must be less than the frame count.
Its additional data is `C("stocat/v2/file-frame", header, frame_index, plaintext_length)`.
The final two values use eight-byte unsigned integers. The first two use length-prefixed byte strings.
An empty file contains one authenticated empty frame and occupies 80 ciphertext bytes.

## Persistence and rollback

Migration 16 stores the random installation UUID in the singleton `encryption_deployment` table.
Read it through `GetEncryptionDeployment`. Back up this table with all encrypted records.
Database triggers reject identifier changes, row deletion, and truncation.
Do not regenerate the identifier when restarting the service or restoring a backup.

V2 libraries store null plaintext names and null legacy key envelopes.
Legacy libraries retain their title uniqueness and key-envelope constraints.
V2 child nodes store encrypted metadata and a 32-byte sibling name token.
They do not require the legacy encrypted-name column.
V2 blobs store null legacy fingerprints and null blob-level encrypted keys.
Version access records store their content-key envelopes.

The Down migration restores the legacy constraints when no v2 data exists.
It rejects rollback when v2 data would become invalid or lose its deployment context.
It never deletes or converts encrypted user data to make rollback succeed.
Use a pre-v2 backup when returning an initialized installation to the legacy schema.

## Account bundle API

The `/api/v2/encryption` routes store the account records above.
JSON fields contain canonical records and detached signatures in unpadded base64url.
Counters in JSON are decimal strings.

| Method | Path | Behavior |
| --- | --- | --- |
| GET | `/bundle` | Return the deployment identifier, the bundle state, the current envelopes, and all identities. |
| POST | `/bundle` | Store generation 1 of the identity and bundle revision 1 of the envelopes. |
| PUT | `/bundle` | Replace one or more envelopes with the next bundle revision. |
| POST | `/identities/rotate` | Store the next identity, its continuity record, and new envelopes with bundle revision 1. |

The bundle ETag is `"<generation>.<bundle_revision>"`.
PUT and rotation require it in `If-Match`. A different value returns status 409.
All three changes require a sign-in or password confirmation within the last 10 minutes.
Otherwise they return status 403. `POST /api/v1/auth/reauthenticate` confirms the password.

The server applies these checks before it stores a record:

- The record parses, and its type matches the request field.
- The deployment and account identifiers match the installation and the signed-in account.
- The identity uses the expected generation, valid public keys, and the derived key identifier.
- The identity signature verifies with its own signing key.
- Each envelope uses the identity generation and the expected bundle revision.
- Each envelope signature verifies with the signing key of that identity generation.
- A continuity record binds the SHA-256 hashes of the current and new identity records.
- The signing key of the current identity signs the continuity record.

The envelope signatures prove that the client holds the private signing key.
The server does not store them. AEAD authenticates the stored envelopes.
The server cannot decrypt an envelope and cannot confirm that it contains the account master key.

Migration 18 stores v2 bundles with `format_version` 2 and the identity generation.
V2 bundles store no legacy KDF parameters and no master-key-encrypted recovery key.
Identity rows store the certificate signature and the optional continuity signature.

## Owner library API

The `/api/v2` routes below store v2 library and folder records for their owner.
The v1 library, file, upload, tag, and replication routes return status 404 for v2 libraries.

| Method | Path | Behavior |
| --- | --- | --- |
| GET, POST | `/libraries` | List v2 libraries or create one with its root metadata and owner-root envelope. |
| GET, PATCH, DELETE | `/libraries/{id}` | Read the library, replace its root metadata, or delete it when it contains no files. |
| POST | `/libraries/{id}/folders` | Create a folder with its metadata, parent envelope, and name token. |
| GET | `/libraries/{id}/nodes` | List active children with their metadata, parent envelopes, and current-version records. |
| PATCH | `/nodes/{id}` | Replace the metadata and name token of a child file or folder. |

The client generates the library, root node, and folder identifiers inside the signed records.
The server stores the records only when the identifiers are unused.

The server applies these checks:

- The current account identity signs every node-metadata and parent-envelope record.
- A new root or folder uses node epoch 1 and metadata revision 1.
- The owner-root envelope names the same library and root node, and the current identity generation.
- A parent envelope names the metadata node as its child, an active parent folder in the same library, and the parent epoch.
- New parent envelopes use generation 1 and revision 1.
- A metadata replacement uses the node epoch and the next metadata revision.
- `If-Match` contains the current metadata revision in quotes. A different value returns status 409.
- A complete metadata record must not exceed 64 KiB, because the database limits the stored record to that size.

The server stores root metadata in `libraries.encrypted_root_metadata`.
The root node row stores the signature, key epoch, and metadata revision.
Child node rows store the metadata record, its signature, and the name token.
`node_key_envelopes` stores each parent envelope and its signature.

## Owner upload API

`POST /api/v2/uploads` creates a session for a new file or a replacement.
Send the ciphertext to `upload_url` with the resumable upload protocol.
Then send `POST /api/v2/uploads/{id}/complete`.
The v1 completion route returns status 404 for v2 sessions.

For a new file, the create request contains the signed node-metadata record, the signed parent envelope, and the name token.
The server applies the folder rules from the owner library API.
For a replacement, the request contains the target file and its current content revision.
The declared size is the complete ciphertext size, and it must be at least 80 bytes.

The completion request contains these records:

| Field | Record | Signature |
| --- | --- | --- |
| `file_key` | `file-key` | None. The manifest signs its hash. |
| `manifest` | `version-manifest` | Current account identity. |
| `current_version` | `current-version` | Current account identity. |

The server applies these checks at completion:

- The records name the library, the file node, and its key epoch. A new file uses node epoch 1.
- The client generates the version identifier. All three records use it.
- The file-key record and the manifest use the same content identifier. The file-key record uses generation 1.
- `key_envelope_hash` equals the SHA-256 hash of the file-key record.
- The manifest header gives a ciphertext size equal to the declared upload size.
- The manifest and current-version records use the content revision after publication.
  A new file uses revision 2. A replacement uses its expected revision plus 1.

The publication worker compares the stored header with the manifest header.
It compares the SHA-256 hash of the stored ciphertext with `ciphertext_hash`.
A difference sets the session state to `failed` with the code `invalid_encryption`.
The server cannot authenticate frames, because it has no content key.

Publication stores a new blob for every v2 version. It never deduplicates v2 ciphertext.
The file version stores the plaintext size from the header and no plaintext hash.
`file_version_keys` stores the file-key record, the manifest, and the manifest signature.
The node row stores the current-version record and its signature.
A changed content revision or key epoch sets the session state to `conflict`.

## Owner download API

`GET /api/v2/files/{id}` returns the current version identifier, the content revision, and the plaintext and ciphertext sizes.
It also returns the file-key record, the signed manifest, and the signed current-version record.
`GET /api/v2/files/{id}/content` streams the stored ciphertext and accepts one byte range.

The content route always sends `application/octet-stream` as the attachment `download.bin`.
The server cannot read the file name or the media type.
The v1 file routes return status 404 for v2 files.

Before the client uses plaintext, it must apply these checks:

- Verify the current-version and manifest signatures with the owner identity.
- Compare the manifest header with the first 64 bytes of the ciphertext.
- Verify each covering frame before it releases plaintext from that frame.
- Verify a complete download against `ciphertext_hash` and the expected frame count.

## Client account formats

The server stores these formats only as ciphertext. Browser clients must use them exactly.

The `account-private` ciphertext encrypts this tuple:

```text
C("stocat/v2/private-bundle", generation, signing_seed, recipient_private_key, previous_keys)
```

The generation is a counter. The signing seed and the recipient private key contain 32 bytes each.
`previous_keys` is a list with a four-byte count.
Each entry contains a generation counter and a 32-byte X25519 private key.
The client derives the Ed25519 key pair from the seed and the X25519 public key from the private key.
The derived identity record must be byte-equal to the current identity record.

Every bundle change must also replace the `account-private` envelope with the same new revision.
The authenticated revision of that envelope then equals the bundle revision.
The client rejects a bundle when the private envelope uses another revision.

After a successful unlock or change, the client stores `<generation>.<bundle_revision>` in local storage.
The checkpoint contains no key material.
The client rejects a later bundle with a lower generation, or with the same generation and a lower revision.

Display the recovery secret as unpadded base64url.
Display its checksum separately as the first four bytes of this hash, in grouped uppercase hexadecimal:

```text
SHA256(C("stocat/v2/recovery-checksum", recovery_secret))
```

Display the identity fingerprint as the uppercase hexadecimal SHA-256 hash of the identity record, in groups of four.

## Fixtures and verification

The fixtures reside in `internal/encryptionv2/testdata`.
`records.json` contains record inputs, canonical bytes, AEAD context bytes, signature inputs, and detached Ed25519 signatures.
`frame.json` contains a header, frame nonce, and frame additional data.
The fixtures include both optional-field states.

Regenerate the fixtures from the repository root:

```sh
python3 internal/encryptionv2/testdata/generate.py
go run internal/encryptionv2/testdata/sign.go
```

Python's standard library independently produces the binary encodings and SHA-256 signature inputs.
Go's standard Ed25519 implementation produces deterministic signatures from the fixture-only seed in `sign.go`.
The ciphertext bytes are synthetic. These fixtures do not claim AEAD, Argon2id, or HPKE interoperability.
Run browser interoperability tests before enabling v2 encryption.

Go tests check every fixture, every truncation boundary, trailing bytes, invalid sizes, and signature substitution across resource contexts.
PostgreSQL tests check v2 persistence, legacy compatibility, same-library envelopes, deployment immutability, and migration rollback.
