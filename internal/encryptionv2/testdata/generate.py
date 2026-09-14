"""Generate format fixtures with Python's standard library."""

import base64
import hashlib
import json
import struct
from pathlib import Path


def b64(value):
    return base64.urlsafe_b64encode(value).decode().rstrip("=")


def field(value):
    return struct.pack(">I", len(value)) + value


account = "usr_00000000000000000000000001"
library = "lib_00000000000000000000000002"
node = "nod_00000000000000000000000003"
child = "nod_00000000000000000000000004"
version = "ver_00000000000000000000000005"
deployment = bytes(range(16))
nonce = bytes(range(24))
content = bytes(range(16))
header = (
    b"STOCAT02" + bytes([2, 1, 0, 0]) + struct.pack(">IQ", 8388608, 1)
    + content + bytes(range(16, 32)) + bytes(8)
)
recipient_public_key = bytes.fromhex("8520f0098930a754748b7ddcb43ef75a0dbf3a0d26381af4eba4a98eaa9b4e6a")
recipient_key_id = hashlib.sha256(
    field(b"stocat/v2/recipient-key-id") + field(deployment) + field(account.encode())
    + struct.pack(">Q", 1) + field(recipient_public_key)
).digest()
common = [("account_id", account), ("library_id", library), ("node_id", node)]
account_context = [("account_id", account), ("generation", 1), ("bundle_revision", 2)]
sealed = [("nonce", nonce), ("ciphertext", bytes(range(48)))]
records = {
    "identity": [
        ("account_id", account), ("generation", 1),
        ("signing_public_key", bytes.fromhex("d75a980182b10ab7d54bfed3c964073a0ee172f3daa62325af021a68f707511a")),
        ("recipient_public_key", recipient_public_key),
        ("recipient_key_id", recipient_key_id),
    ],
    "identity-continuity": [("account_id", account), ("previous_generation", 1), ("generation", 2),
                            ("previous_identity_hash", bytes(32)), ("identity_hash", bytes(range(32)))],
    "contact-store": [("account_id", account), ("generation", 1), ("revision", 1),
                      ("previous_revision_hash", None)] + sealed,
    "account-password": account_context + [("profile", "argon2id-v19-m65536-t3-p4-l32"), ("salt", bytes(range(16)))] + sealed,
    "account-recovery": account_context + sealed,
    "account-private": account_context + sealed,
    "owner-root": common + [("generation", 1), ("node_epoch", 2), ("revision", 3)] + sealed,
    "parent-envelope": [("account_id", account), ("library_id", library), ("parent_id", node), ("child_id", child),
                        ("parent_epoch", 1), ("child_epoch", 2), ("generation", 3), ("revision", 4)] + sealed,
    "node-metadata": common + [("node_epoch", 1), ("revision", 2)] + sealed,
    "file-key": common + [("version_id", version), ("content_id", content), ("node_epoch", 1), ("generation", 2)] + sealed,
    "version-manifest": common + [("version_id", version), ("content_id", content), ("node_epoch", 1), ("revision", 2),
                                  ("header", header), ("ciphertext_hash", bytes(range(32))), ("key_envelope_hash", bytes(32))],
    "current-version": common + [("node_epoch", 1), ("revision", 2), ("version_id", version)],
}
fixtures = []
for kind, entries in records.items():
    variants = [entries]
    if kind == "contact-store":
        variants.append([(k, bytes(range(32)) if k == "previous_revision_hash" else v) for k, v in entries])
    if kind == "current-version":
        variants.append([(k, None if k == "version_id" else v) for k, v in entries])
    for values in variants:
        encoded = field(("stocat/v2/" + kind).encode()) + field(deployment) + bytes([1])
        fields = {}
        aad = None
        for name, value in values:
            if (kind == "contact-store" and name == "previous_revision_hash") or (kind == "current-version" and name == "version_id"):
                encoded += bytes([value is not None])
            if value is None:
                continue
            if name == "ciphertext":
                aad = encoded.hex()
            if isinstance(value, int):
                encoded += struct.pack(">Q", value)
                fields[name] = str(value)
            elif isinstance(value, bytes):
                encoded += field(value)
                fields[name] = b64(value)
            else:
                encoded += field(value.encode())
                fields[name] = value
        signature_input = field(b"stocat/v2/signature") + field(kind.encode()) + field(hashlib.sha256(encoded).digest())
        fixtures.append({"record": {"type": kind, "deployment_id": b64(deployment), "fields": fields},
                         "hex": encoded.hex(), "signature_input_hex": signature_input.hex(), "aad_hex": aad})
Path(__file__).with_name("records.json").write_text(json.dumps(fixtures, indent=2) + "\n")
frame_aad = field(b"stocat/v2/file-frame") + field(header) + struct.pack(">QQ", 0, 1)
Path(__file__).with_name("frame.json").write_text(json.dumps({"header_hex": header.hex(), "index": "0", "aad_hex": frame_aad.hex(),
                                                          "nonce_hex": (bytes(range(16, 32)) + bytes(8)).hex()}, indent=2) + "\n")
