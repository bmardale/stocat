import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vite-plus/test";
import type { AdminUser, Backend, Usage } from "@/api/generated/model";
import { jsonResponse, renderApp, stubApi, testUser } from "@/test/app";

const gib = 1024 ** 3;

describe("quotas", () => {
  it("shows storage usage for each backend in the sidebar", async () => {
    const usage: Usage[] = [
      {
        backend: { id: "stb_b2", name: "Backblaze", type: "s3" },
        used_bytes: 512 * 1024 ** 2,
        limit_bytes: gib,
      },
      {
        backend: { id: "stb_local", name: "Local disk", type: "local" },
        used_bytes: 2 * gib,
        limit_bytes: null,
      },
    ];
    stubApi({
      "GET /api/v1/auth/me": () => jsonResponse(200, testUser),
      "GET /api/v1/libraries": () => jsonResponse(200, []),
      "GET /api/v1/files/trash": () => jsonResponse(200, { items: [] }),
      "GET /api/v1/storage-usage": () => jsonResponse(200, usage),
    });

    await renderApp("/trash");
    const meter = await screen.findByRole("meter", { name: "Backblaze storage" });
    expect(meter.getAttribute("aria-valuetext")).toBe("512 MiB of 1.0 GiB");
    expect(screen.getByText("2.0 GiB used")).toBeDefined();
    expect(screen.queryByRole("meter", { name: "Local disk storage" })).toBeNull();
  });

  it("sets a quota override for one storage backend", async () => {
    const member: AdminUser = {
      id: "usr_member",
      name: "Grace Hopper",
      email: "grace@example.com",
      is_admin: false,
      default_quota: { mode: "inherit" },
      backend_quotas: [],
    };
    const backends: Backend[] = [
      {
        id: "stb_b2",
        name: "Backblaze",
        type: "s3",
        enabled: true,
        s3: {
          endpoint: "https://s3.example.com",
          region: "eu-central-1",
          bucket: "stocat",
          prefix: "",
          force_path_style: false,
        },
      },
      {
        id: "stb_local",
        name: "Local disk",
        type: "local",
        enabled: true,
        local: { root: "/srv" },
      },
    ];
    let body: unknown;
    stubApi({
      "GET /api/v1/auth/me": () => jsonResponse(200, { ...testUser, is_admin: true }),
      "GET /api/v1/admin/users": () => jsonResponse(200, [member]),
      "GET /api/v1/admin/quota": () =>
        jsonResponse(200, { default_quota: { mode: "limited", limit_bytes: 10 * gib } }),
      "GET /api/v1/admin/storage-backends": () => jsonResponse(200, backends),
      "GET /api/v1/storage-usage": () => jsonResponse(200, []),
      "PUT /api/v1/admin/users/usr_member/quota": (init) => {
        body = JSON.parse(init?.body as string);
        return jsonResponse(200, member);
      },
    });

    await renderApp("/admin/users");
    expect(await screen.findByText("Global (10 GiB)")).toBeDefined();
    fireEvent.click(screen.getByRole("button", { name: "Edit quotas for Grace Hopper" }));
    const dialog = await screen.findByRole("dialog");
    const backblaze = within(dialog).getByRole("group", { name: "Backblaze" });
    fireEvent.click(within(backblaze).getByRole("radio", { name: "Limit" }));
    fireEvent.change(await within(backblaze).findByLabelText("Backblaze in GiB"), {
      target: { value: "1.5" },
    });
    fireEvent.click(within(dialog).getByRole("button", { name: "Save quotas" }));

    await waitFor(() =>
      expect(body).toEqual({
        default_quota: { mode: "inherit" },
        backend_quotas: [{ backend_id: "stb_b2", mode: "limited", limit_bytes: 1.5 * gib }],
      }),
    );
  });
});
