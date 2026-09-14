import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vite-plus/test";
import { jsonResponse, noLibraries, renderApp, stubApi, testUser } from "@/test/app";

const adminUser = { ...testUser, is_admin: true };

describe("registration administration", () => {
  it("toggles invite-only registration and creates a code", async () => {
    let inviteOnly = false;
    const codes: { id: string; code: string; created_at: string }[] = [];
    stubApi({
      "GET /api/v1/auth/me": () => jsonResponse(200, adminUser),
      "GET /api/v1/libraries": noLibraries,
      "GET /api/v1/admin/config": () => jsonResponse(200, { invite_only: inviteOnly }),
      "GET /api/v1/admin/invite-codes": () => jsonResponse(200, codes),
      "PUT /api/v1/admin/config": (init) => {
        inviteOnly = (JSON.parse(init?.body as string) as { invite_only: boolean }).invite_only;
        return jsonResponse(200, { invite_only: inviteOnly });
      },
      "POST /api/v1/admin/invite-codes": () => {
        const code = { id: "inv_test", code: "TEST-INVITE", created_at: "2026-09-14T12:00:00Z" };
        codes.unshift(code);
        return jsonResponse(201, code);
      },
    });

    await renderApp("/admin/settings");
    fireEvent.click(screen.getByRole("switch", { name: "Invite-only registration" }));
    fireEvent.click(screen.getByRole("button", { name: "Save registration settings" }));
    await waitFor(() => expect(inviteOnly).toBe(true));
    fireEvent.click(screen.getByRole("button", { name: "Create code" }));
    expect(await screen.findByText("TEST-INVITE")).toBeDefined();
    const table = screen.getByRole("table", { name: "Invite codes" });
    expect(within(table).getByText("Available")).toBeDefined();
  });
});
