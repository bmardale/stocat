import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vite-plus/test";
import type { Backend, ConnectionCheck, User } from "@/api/generated/model";
import { jsonResponse, renderApp, stubApi, testUser } from "@/test/app";

const adminUser: User = { ...testUser, is_admin: true };

const localBackend: Backend = {
  id: "stb_local",
  name: "Local disk",
  type: "local",
  enabled: true,
  local: { root: "/srv/stocat" },
};

const s3Backend: Backend = {
  id: "stb_s3",
  name: "Archive",
  type: "s3",
  enabled: false,
  s3: {
    endpoint: "https://rustfs.example.com",
    region: "us-east-1",
    bucket: "stocat",
    prefix: "objects",
    force_path_style: true,
  },
};

function stubBackends(initial: Backend[]) {
  let backends = initial;
  const bodies: unknown[] = [];
  const save = (id: string, init: RequestInit | undefined) => {
    const body = JSON.parse(init?.body as string);
    bodies.push(body);
    const current = backends.find((backend) => backend.id === id);
    const saved: Backend = {
      id,
      name: body.name,
      type: body.type ?? current?.type,
      enabled: body.enabled,
      local: body.local,
      s3: body.s3 && {
        endpoint: body.s3.endpoint,
        region: body.s3.region,
        bucket: body.s3.bucket,
        prefix: body.s3.prefix,
        force_path_style: body.s3.force_path_style,
      },
    };
    backends = current
      ? backends.map((backend) => (backend.id === id ? saved : backend))
      : [...backends, saved];
    return saved;
  };
  const fetchMock = stubApi({
    "GET /api/v1/auth/me": () => jsonResponse(200, adminUser),
    "GET /api/v1/admin/storage-backends": () => jsonResponse(200, backends),
    "POST /api/v1/admin/storage-backends": (init) => jsonResponse(201, save("stb_new", init)),
    "PUT /api/v1/admin/storage-backends/stb_s3": (init) => jsonResponse(200, save("stb_s3", init)),
    "DELETE /api/v1/admin/storage-backends/stb_local": () => {
      backends = backends.filter((backend) => backend.id !== "stb_local");
      return jsonResponse(204);
    },
  });
  return { fetchMock, bodies };
}

function fill(container: HTMLElement, label: string, value: string) {
  fireEvent.change(within(container).getByLabelText(label), { target: { value } });
}

const rows = () =>
  within(screen.getByRole("table", { name: "Storage backends" })).getAllByRole("row");

async function openActions(name: string) {
  fireEvent.click(await screen.findByRole("button", { name: `Actions for ${name}` }));
}

describe("storage backends", () => {
  it("hides the page from users who are not administrators", async () => {
    stubApi({ "GET /api/v1/auth/me": () => jsonResponse(200, testUser) });
    const router = await renderApp("/admin/storage");
    expect(await screen.findByRole("heading", { name: "Home" })).toBeDefined();
    expect(router.state.location.pathname).toBe("/");
    expect(screen.queryByRole("link", { name: "Storage" })).toBeNull();
  });

  it("opens the page from the sidebar", async () => {
    stubBackends([]);
    const router = await renderApp("/");
    fireEvent.click(await screen.findByRole("link", { name: "Storage" }));
    expect(await screen.findByRole("heading", { name: "Storage" })).toBeDefined();
    expect(router.state.location.pathname).toBe("/admin/storage");
    expect(screen.getByText("No storage backends")).toBeDefined();
  });

  it("lists backends with their locations", async () => {
    stubBackends([s3Backend, localBackend]);
    await renderApp("/admin/storage");
    await screen.findByRole("table", { name: "Storage backends" });
    const [, archive, local] = rows();
    expect(within(archive).getByText("Archive")).toBeDefined();
    expect(within(archive).getByText("s3://stocat/objects · rustfs.example.com")).toBeDefined();
    expect(within(archive).getByText("Disabled")).toBeDefined();
    expect(within(local).getByText("/srv/stocat")).toBeDefined();
    expect(within(local).getByText("Enabled")).toBeDefined();
  });

  it("adds a local backend", async () => {
    const { bodies } = stubBackends([]);
    await renderApp("/admin/storage");
    fireEvent.click(await screen.findByRole("button", { name: "Add backend" }));
    const dialog = await screen.findByRole("dialog");
    fill(dialog, "Name", "Media");
    fill(dialog, "Directory", "/srv/media");
    fireEvent.click(within(dialog).getByRole("button", { name: "Add backend" }));
    await waitFor(() => expect(rows()).toHaveLength(2));
    expect(bodies).toEqual([
      { name: "Media", type: "local", enabled: true, local: { root: "/srv/media" } },
    ]);
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it("adds an S3 backend", async () => {
    const { bodies } = stubBackends([]);
    await renderApp("/admin/storage");
    fireEvent.click(await screen.findByRole("button", { name: "Add backend" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("radio", { name: /S3/ }));
    fill(dialog, "Name", "Archive");
    fill(dialog, "Endpoint", "https://rustfs.example.com");
    fill(dialog, "Region", "us-east-1");
    fill(dialog, "Bucket", "stocat");
    fill(dialog, "Access key ID", "AKIAEXAMPLE");
    fill(dialog, "Secret access key", "secret");
    fireEvent.click(within(dialog).getByRole("switch", { name: "Path-style URLs" }));
    fireEvent.click(within(dialog).getByRole("button", { name: "Add backend" }));
    await waitFor(() => expect(rows()).toHaveLength(2));
    expect(bodies).toEqual([
      {
        name: "Archive",
        type: "s3",
        enabled: true,
        s3: {
          endpoint: "https://rustfs.example.com",
          region: "us-east-1",
          bucket: "stocat",
          prefix: "",
          force_path_style: true,
          access_key_id: "AKIAEXAMPLE",
          secret_access_key: "secret",
        },
      },
    ]);
  });

  it("validates a new S3 backend before it sends a request", async () => {
    const { fetchMock } = stubBackends([]);
    await renderApp("/admin/storage");
    fireEvent.click(await screen.findByRole("button", { name: "Add backend" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("radio", { name: /S3/ }));
    fireEvent.click(within(dialog).getByRole("button", { name: "Add backend" }));
    expect(await within(dialog).findByText("Enter a name.")).toBeDefined();
    expect(within(dialog).getByText("Enter a bucket name.")).toBeDefined();
    expect(within(dialog).getByText("Enter the secret access key.")).toBeDefined();
    expect(fetchMock).not.toHaveBeenCalledWith(
      "/api/v1/admin/storage-backends",
      expect.objectContaining({ method: "POST" }),
    );
  });

  it("edits a backend and keeps the stored credentials", async () => {
    const { bodies } = stubBackends([s3Backend]);
    await renderApp("/admin/storage");
    await openActions("Archive");
    fireEvent.click(await screen.findByRole("menuitem", { name: "Edit" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).queryByRole("radio")).toBeNull();
    expect((within(dialog).getByLabelText("Bucket") as HTMLInputElement).value).toBe("stocat");
    fill(dialog, "Name", "Cold archive");
    fireEvent.click(within(dialog).getByRole("button", { name: "Save changes" }));
    expect(await screen.findByText("Cold archive")).toBeDefined();
    expect(bodies).toEqual([
      {
        name: "Cold archive",
        enabled: false,
        s3: { ...s3Backend.s3, access_key_id: "", secret_access_key: "" },
      },
    ]);
  });

  it("shows the server error in the dialog", async () => {
    stubApi({
      "GET /api/v1/auth/me": () => jsonResponse(200, adminUser),
      "GET /api/v1/admin/storage-backends": () => jsonResponse(200, []),
      "POST /api/v1/admin/storage-backends": () =>
        jsonResponse(409, {
          status: 409,
          detail: "A storage backend with this name already exists.",
        }),
    });
    await renderApp("/admin/storage");
    fireEvent.click(await screen.findByRole("button", { name: "Add backend" }));
    const dialog = await screen.findByRole("dialog");
    fill(dialog, "Name", "Media");
    fill(dialog, "Directory", "/srv/media");
    fireEvent.click(within(dialog).getByRole("button", { name: "Add backend" }));
    expect(
      await within(dialog).findByText("A storage backend with this name already exists."),
    ).toBeDefined();
  });

  it("deletes a backend after confirmation", async () => {
    const { fetchMock } = stubBackends([s3Backend, localBackend]);
    await renderApp("/admin/storage");
    await openActions("Local disk");
    fireEvent.click(await screen.findByRole("menuitem", { name: "Delete" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByText("Delete Local disk?")).toBeDefined();
    fireEvent.click(within(dialog).getByRole("button", { name: "Delete" }));
    await waitFor(() => expect(rows()).toHaveLength(2));
    expect(screen.queryByText("/srv/stocat")).toBeNull();
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/v1/admin/storage-backends/stb_local",
      expect.objectContaining({ method: "DELETE" }),
    );
  });

  it("tests the connection of unsaved settings in the dialog", async () => {
    const bodies: unknown[] = [];
    stubApi({
      "GET /api/v1/auth/me": () => jsonResponse(200, adminUser),
      "GET /api/v1/admin/storage-backends": () => jsonResponse(200, []),
      "POST /api/v1/admin/storage-backends/check": (init) => {
        const body = JSON.parse(init?.body as string);
        bodies.push(body);
        const result: ConnectionCheck = body.local.root.endsWith("missing")
          ? { ok: false, message: "The directory does not exist." }
          : { ok: true, message: "The server can write, read, and delete objects." };
        return jsonResponse(200, result);
      },
    });
    await renderApp("/admin/storage");
    fireEvent.click(await screen.findByRole("button", { name: "Add backend" }));
    const dialog = await screen.findByRole("dialog");
    fill(dialog, "Directory", "/srv/missing");
    fireEvent.click(within(dialog).getByRole("button", { name: "Test connection" }));
    expect(await within(dialog).findByText("Connection failed")).toBeDefined();
    expect(within(dialog).getByText("The directory does not exist.")).toBeDefined();

    fill(dialog, "Directory", "/srv/media");
    await waitFor(() => expect(within(dialog).queryByText("Connection failed")).toBeNull());
    fireEvent.click(within(dialog).getByRole("button", { name: "Test connection" }));
    expect(await within(dialog).findByText("Connection works")).toBeDefined();
    expect(bodies).toEqual([
      { type: "local", local: { root: "/srv/missing" } },
      { type: "local", local: { root: "/srv/media" } },
    ]);
  });

  it("tests changes to a saved backend with the stored credentials", async () => {
    let body: unknown;
    stubApi({
      "GET /api/v1/auth/me": () => jsonResponse(200, adminUser),
      "GET /api/v1/admin/storage-backends": () => jsonResponse(200, [s3Backend]),
      "POST /api/v1/admin/storage-backends/check": (init) => {
        body = JSON.parse(init?.body as string);
        return jsonResponse(422, {
          status: 422,
          detail: "Enter the credentials again when you change the endpoint.",
        });
      },
    });
    await renderApp("/admin/storage");
    await openActions("Archive");
    fireEvent.click(await screen.findByRole("menuitem", { name: "Edit" }));
    const dialog = await screen.findByRole("dialog");
    fill(dialog, "Endpoint", "https://other.example.com");
    fireEvent.click(within(dialog).getByRole("button", { name: "Test connection" }));
    expect(
      await within(dialog).findByText("Enter the credentials again when you change the endpoint."),
    ).toBeDefined();
    expect(body).toEqual({
      id: "stb_s3",
      type: "s3",
      s3: {
        ...s3Backend.s3,
        endpoint: "https://other.example.com",
        access_key_id: "",
        secret_access_key: "",
      },
    });
  });

  it("tests the connection of a saved backend from the table", async () => {
    stubApi({
      "GET /api/v1/auth/me": () => jsonResponse(200, adminUser),
      "GET /api/v1/admin/storage-backends": () => jsonResponse(200, [s3Backend, localBackend]),
      "POST /api/v1/admin/storage-backends/stb_s3/check": () =>
        jsonResponse(200, { ok: false, message: "The bucket does not exist." }),
      "POST /api/v1/admin/storage-backends/stb_local/check": () =>
        jsonResponse(200, { ok: true, message: "The server can write, read, and delete objects." }),
    });
    await renderApp("/admin/storage");
    await screen.findByRole("table", { name: "Storage backends" });
    const [, archive, local] = rows();
    expect(within(archive).getByText("Not tested")).toBeDefined();

    await openActions("Archive");
    fireEvent.click(await screen.findByRole("menuitem", { name: "Test connection" }));
    expect(await within(archive).findByText("The bucket does not exist.")).toBeDefined();

    await openActions("Local disk");
    fireEvent.click(await screen.findByRole("menuitem", { name: "Test connection" }));
    expect(await within(local).findByText("Works")).toBeDefined();
    expect(within(archive).getByText("The bucket does not exist.")).toBeDefined();
  });
});
