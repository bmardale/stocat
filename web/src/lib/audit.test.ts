import { describe, expect, it } from "vite-plus/test";
import type { AuditEvent } from "@/api/generated/model";
import { auditActorName, auditEventSummary } from "./audit";

function event(overrides: Partial<AuditEvent>): AuditEvent {
  return {
    id: "aud_test",
    action: "account.signed_in",
    actor_type: "user",
    actor: { id: "usr_ada", name: "Ada Lovelace" },
    details: {},
    created_at: "2026-09-14T10:00:00Z",
    ...overrides,
  };
}

describe("auditEventSummary", () => {
  it.each([
    [
      "a renamed file",
      event({
        action: "file.renamed",
        details: { previous_name: "draft.txt", name: "final.txt", library_name: "Documents" },
      }),
      "“draft.txt” → “final.txt” · in Documents",
    ],
    [
      "a file in an encrypted library",
      event({ action: "file.trashed", details: { encrypted: true, library_name: "Secret" } }),
      "Encrypted name · in Secret",
    ],
    [
      "an encrypted library",
      event({ action: "library.created", details: { name: "Secret", encrypted: true } }),
      "“Secret” · End-to-end encrypted",
    ],
    [
      "a changed email",
      event({
        action: "account.updated",
        details: { previous_email: "ada@example.com", email: "ada@lovelace.dev" },
      }),
      "“ada@example.com” → “ada@lovelace.dev”",
    ],
    ["a sign-in method", event({ details: { method: "passkey" } }), "With passkey"],
    [
      "an upload",
      event({ action: "file.uploaded", details: { name: "a.bin", size_bytes: 2048 } }),
      "“a.bin” · 2.0 KiB",
    ],
    [
      "a replication",
      event({
        action: "replication.created",
        details: { library_name: "Photos", destination_library_name: "Backup" },
      }),
      "Photos → Backup",
    ],
    [
      "a user quota",
      event({
        action: "quota.user_updated",
        details: {
          quota_mode: "limited",
          quota_limit_bytes: 10 * 1024 ** 3,
          backend_quota_count: 1,
        },
      }),
      "Limit 10 GiB · 1 backend override",
    ],
    [
      "a disabled backend",
      event({
        action: "storage_backend.updated",
        details: { name: "Archive", backend_type: "s3", backend_enabled: false },
      }),
      "“Archive” · S3 · Disabled",
    ],
    ["an event without details", event({ action: "account.password_changed" }), ""],
  ])("describes %s", (_, value, expected) => {
    expect(auditEventSummary(value)).toBe(expected);
  });
});

describe("auditActorName", () => {
  it("names the system and deleted users", () => {
    expect(auditActorName(event({}))).toBe("Ada Lovelace");
    expect(auditActorName(event({ actor_type: "system", actor: undefined }))).toBe("System");
    expect(auditActorName(event({ actor: undefined }))).toBe("Deleted user");
  });
});
