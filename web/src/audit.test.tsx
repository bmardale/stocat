import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vite-plus/test";
import type { AdminUser, AuditEvent, User } from "@/api/generated/model";
import { jsonResponse, noLibraries, renderApp, stubApi, testUser } from "@/test/app";

const adminUser: User = { ...testUser, is_admin: true };

const signedIn: AuditEvent = {
  id: "aud_signed_in",
  action: "account.signed_in",
  actor_type: "user",
  actor: { id: testUser.id, name: testUser.name, email: testUser.email },
  subject: { id: testUser.id, name: testUser.name, email: testUser.email },
  details: { method: "passkey" },
  ip_address: "203.0.113.10",
  user_agent:
    "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36",
  created_at: "2026-09-13T15:02:58Z",
};

const quotaChanged: AuditEvent = {
  id: "aud_quota",
  action: "quota.user_updated",
  actor_type: "user",
  actor: { id: "usr_grace", name: "Grace Hopper" },
  subject: { id: testUser.id, name: testUser.name },
  target_id: testUser.id,
  details: { quota_mode: "unlimited" },
  created_at: "2026-09-14T09:00:00Z",
};

const purged: AuditEvent = {
  id: "aud_purged",
  action: "file.deleted",
  actor_type: "system",
  subject: { id: testUser.id, name: testUser.name, email: testUser.email },
  target_id: "nod_old",
  details: { name: "old.txt", library_name: "Documents" },
  created_at: "2026-09-12T00:00:00Z",
};

describe("activity", () => {
  it("lists the activity of the current user and loads more events", async () => {
    stubApi({
      "GET /api/v1/auth/me": () => jsonResponse(200, testUser),
      "GET /api/v1/libraries": noLibraries,
      "GET /api/v1/activity": () =>
        jsonResponse(200, { items: [quotaChanged, signedIn], next_cursor: signedIn.id }),
      "GET /api/v1/activity?cursor=aud_signed_in": () => jsonResponse(200, { items: [purged] }),
    });
    await renderApp("/settings/activity");
    expect(await screen.findByRole("heading", { name: "Activity" })).toBeDefined();
    const list = await screen.findByRole("list", { name: "Activity" });
    const items = within(list).getAllByRole("listitem");
    expect(items).toHaveLength(2);
    expect(within(items[0]).getByText("User quota changed")).toBeDefined();
    expect(within(items[0]).getByText("No limit")).toBeDefined();
    expect(within(items[0]).getByText(/By Grace Hopper/)).toBeDefined();
    expect(within(items[1]).getByText("Signed in")).toBeDefined();
    expect(within(items[1]).getByText("With passkey")).toBeDefined();
    expect(within(items[1]).getByText(/203\.0\.113\.10 · Chrome 140 on macOS/)).toBeDefined();
    expect(within(items[1]).queryByText(/By /)).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "Load more" }));
    await waitFor(() => expect(within(list).getAllByRole("listitem")).toHaveLength(3));
    const purgedItem = within(list).getAllByRole("listitem")[2];
    expect(within(purgedItem).getByText("“old.txt” · in Documents")).toBeDefined();
    expect(within(purgedItem).getByText(/By System/)).toBeDefined();
    expect(screen.queryByRole("button", { name: "Load more" })).toBeNull();
  });
});

describe("audit log", () => {
  const users: AdminUser[] = [
    {
      id: testUser.id,
      name: testUser.name,
      email: testUser.email,
      is_admin: false,
      default_quota: { mode: "inherit" },
      backend_quotas: [],
    },
  ];

  it("is available only to administrators", async () => {
    stubApi({
      "GET /api/v1/auth/me": () => jsonResponse(200, testUser),
      "GET /api/v1/libraries": noLibraries,
    });
    const router = await renderApp("/admin/audit");
    await waitFor(() => expect(router.state.location.pathname).toBe("/"));
    expect(screen.queryByRole("link", { name: "Audit log" })).toBeNull();
  });

  it("lists and filters the events of all users", async () => {
    stubApi({
      "GET /api/v1/auth/me": () => jsonResponse(200, adminUser),
      "GET /api/v1/libraries": noLibraries,
      "GET /api/v1/admin/users": () => jsonResponse(200, users),
      "GET /api/v1/admin/audit-events": () => jsonResponse(200, { items: [quotaChanged, purged] }),
      "GET /api/v1/admin/audit-events?action=file.deleted": () =>
        jsonResponse(200, { items: [purged] }),
      "GET /api/v1/admin/audit-events?action=file.deleted&user=usr_test": () =>
        jsonResponse(200, { items: [] }),
    });
    const router = await renderApp("/");
    fireEvent.click(await screen.findByRole("link", { name: "Audit log" }));
    expect(await screen.findByRole("heading", { name: "Audit log" })).toBeDefined();
    const table = await screen.findByRole("table", { name: "Audit events" });
    expect(within(table).getAllByRole("row")).toHaveLength(3);
    expect(within(table).getByText("Grace Hopper")).toBeDefined();
    expect(within(table).getByText("System")).toBeDefined();
    expect(within(table).getAllByText("For Ada Lovelace")).toHaveLength(2);

    fireEvent.change(screen.getByLabelText("Action"), { target: { value: "file.deleted" } });
    await waitFor(() =>
      expect(
        within(screen.getByRole("table", { name: "Audit events" })).getAllByRole("row"),
      ).toHaveLength(2),
    );
    expect(router.state.location.search).toEqual({ action: "file.deleted" });

    fireEvent.change(screen.getByLabelText("User"), { target: { value: testUser.id } });
    expect(await screen.findByText("No events match the filters.")).toBeDefined();
    expect(router.state.location.search).toEqual({ action: "file.deleted", user: testUser.id });
  });
});
