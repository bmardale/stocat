import type { AuditEvent, AuditEventAction } from "@/api/generated/model";
import { formatBytes } from "@/lib/utils";

// The Record type makes the build fail when the server adds an action without a label.
export const auditActionLabels: Record<AuditEventAction, string> = {
  "account.registered": "Account created",
  "account.signed_in": "Signed in",
  "account.signed_out": "Signed out",
  "account.updated": "Account details changed",
  "account.password_changed": "Password changed",
  "account.admin_granted": "Administrator role granted",
  "account.admin_revoked": "Administrator role removed",
  "session.revoked": "Session revoked",
  "session.others_revoked": "Other sessions signed out",
  "passkey.added": "Passkey added",
  "passkey.renamed": "Passkey renamed",
  "passkey.deleted": "Passkey deleted",
  "library.created": "Library created",
  "folder.created": "Folder created",
  "file.uploaded": "File uploaded",
  "file.replaced": "File version uploaded",
  "file.renamed": "File renamed",
  "file.trashed": "File moved to trash",
  "file.restored": "File restored",
  "file.deleted": "File permanently deleted",
  "file.tagged": "Tag added to file",
  "file.untagged": "Tag removed from file",
  "tag.created": "Tag created",
  "tag.updated": "Tag changed",
  "tag.deleted": "Tag deleted",
  "replication.created": "Replication created",
  "replication.sync_requested": "Replication sync started",
  "replication.deleted": "Replication stopped",
  "storage_backend.created": "Storage backend created",
  "storage_backend.updated": "Storage backend changed",
  "storage_backend.deleted": "Storage backend deleted",
  "quota.user_updated": "User quota changed",
  "quota.default_updated": "Global default quota changed",
};

const quote = (value: string) => `“${value}”`;

const change = (previous: string | undefined, current: string | undefined) =>
  previous && current ? `${quote(previous)} → ${quote(current)}` : current && quote(current);

// The summary uses only the details that the event has, so a new action needs no extra code.
export function auditEventSummary({ action, details }: AuditEvent) {
  const parts = [
    change(details.previous_name, details.name),
    change(details.previous_email, details.email),
    details.encrypted && (action === "library.created" ? "End-to-end encrypted" : "Encrypted name"),
    details.tag_name && `Tag ${quote(details.tag_name)}`,
    details.destination_library_name && details.library_name
      ? `${details.library_name} → ${details.destination_library_name}`
      : details.library_name && `in ${details.library_name}`,
    details.method && `With ${details.method}`,
    details.size_bytes && formatBytes(details.size_bytes),
    details.quota_mode && quotaSummary(details.quota_mode, details.quota_limit_bytes),
    details.backend_quota_count &&
      `${details.backend_quota_count} backend ${details.backend_quota_count === 1 ? "override" : "overrides"}`,
    details.backend_type && (details.backend_type === "s3" ? "S3" : "Local disk"),
    details.backend_enabled === false && "Disabled",
  ];
  return parts.filter(Boolean).join(" · ");
}

function quotaSummary(mode: string, limitBytes: number | undefined) {
  if (mode === "limited" && limitBytes !== undefined) return `Limit ${formatBytes(limitBytes)}`;
  return mode === "unlimited" ? "No limit" : "Global default";
}

export function auditActorName({ actor, actor_type }: AuditEvent) {
  if (actor_type === "system") return "System";
  return actor?.name ?? "Deleted user";
}
