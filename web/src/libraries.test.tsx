import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vite-plus/test";
import type { Library, LibraryBackend, Node, User } from "@/api/generated/model";
import {
  createKeyEnvelope,
  encryptName,
  fromBase64,
  nameToken,
  openKeyEnvelope,
} from "@/lib/library-crypto";
import { jsonResponse, noLibraries, renderApp, stubApi, testUser } from "@/test/app";

const backend: LibraryBackend = { id: "stb_local", name: "Local disk", type: "local" };

const documents: Library = {
  id: "lib_docs",
  name: "Documents",
  encryption_mode: "none",
  root_node_id: "nod_root",
  backend,
  quota_mb: null,
  created_at: "2026-09-10T10:00:00Z",
  updated_at: "2026-09-10T10:00:00Z",
};

const passphrase = "correct horse battery staple";

async function encryptedLibrary() {
  const { envelope, keys } = await createKeyEnvelope(passphrase);
  const library: Library = {
    ...documents,
    id: "lib_private",
    name: "Private",
    encryption_mode: "e2ee",
    root_node_id: "nod_private_root",
    key_envelope: envelope,
  };
  return { library, keys };
}

function folder(id: string, name: string, parent = "nod_root"): Node {
  return {
    id,
    library_id: "lib_docs",
    parent_id: parent,
    kind: "folder",
    name,
    revision: 1,
    created_at: "2026-09-11T09:00:00Z",
    updated_at: "2026-09-11T09:00:00Z",
  };
}

function fill(container: HTMLElement, label: string, value: string) {
  fireEvent.change(within(container).getByLabelText(label), { target: { value } });
}

const folderRows = () =>
  within(screen.getByRole("table", { name: "Folder contents" })).getAllByRole("row");

const folderPath = () => screen.getByRole("navigation", { name: "Folder path" });

describe("files", () => {
  it("links to library setup when there are no libraries", async () => {
    stubApi({
      "GET /api/v1/auth/me": () => jsonResponse(200, testUser),
      "GET /api/v1/libraries": noLibraries,
      "GET /api/v1/storage-backends": () => jsonResponse(200, [backend]),
    });
    const router = await renderApp("/");
    expect(await screen.findByRole("heading", { name: "Files" })).toBeDefined();
    fireEvent.click(screen.getByRole("link", { name: "Set up a library" }));
    expect(await screen.findByRole("heading", { name: "Libraries" })).toBeDefined();
    expect(router.state.location.pathname).toBe("/libraries");
  });

  it("switches libraries and asks for the passphrase of a locked library", async () => {
    const { library: secret } = await encryptedLibrary();
    stubApi({
      "GET /api/v1/auth/me": () => jsonResponse(200, testUser),
      "GET /api/v1/libraries": () => jsonResponse(200, [documents, secret]),
      "GET /api/v1/libraries/lib_docs/nodes": () =>
        jsonResponse(200, { items: [folder("nod_photos", "Photos")] }),
    });
    const router = await renderApp("/");
    expect(await screen.findByRole("link", { name: "Photos" })).toBeDefined();
    fireEvent.click(screen.getByRole("button", { name: "Library: Documents" }));
    fireEvent.click(await screen.findByRole("menuitemradio", { name: /Private/ }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText("Unlock Private")).toBeDefined();
    expect(router.state.location.search).toEqual({ library: "lib_private" });
    expect(localStorage.getItem("stocat.last-library")).toBe("lib_private");

    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(screen.getByText("Private is locked")).toBeDefined();
  });

  it("opens the last used library", async () => {
    localStorage.setItem("stocat.last-library", "lib_other");
    stubApi({
      "GET /api/v1/auth/me": () => jsonResponse(200, testUser),
      "GET /api/v1/libraries": () =>
        jsonResponse(200, [documents, { ...documents, id: "lib_other", name: "Other" }]),
      "GET /api/v1/libraries/lib_other/nodes": () => jsonResponse(200, { items: [] }),
    });
    await renderApp("/");
    expect(await screen.findByRole("button", { name: "Library: Other" })).toBeDefined();
    expect(await screen.findByText("This folder is empty")).toBeDefined();
  });

  it("browses into a folder and creates a folder there", async () => {
    let body: unknown;
    let photos: Node[] = [];
    stubApi({
      "GET /api/v1/auth/me": () => jsonResponse(200, testUser),
      "GET /api/v1/libraries": () => jsonResponse(200, [documents]),
      "GET /api/v1/libraries/lib_docs/nodes": () =>
        jsonResponse(200, {
          items: [
            folder("nod_photos", "Photos"),
            { ...folder("nod_note", "notes.txt"), kind: "file" },
          ],
        }),
      "GET /api/v1/libraries/lib_docs/nodes?parent_id=nod_photos": () =>
        jsonResponse(200, { items: photos }),
      "POST /api/v1/libraries/lib_docs/folders": (init) => {
        body = JSON.parse(init?.body as string);
        photos = [folder("nod_2026", "2026", "nod_photos")];
        return jsonResponse(201, photos[0]);
      },
    });
    const router = await renderApp("/");
    await screen.findByRole("table", { name: "Folder contents" });
    expect(screen.queryByRole("link", { name: /notes\.txt/ })).toBeNull();
    fireEvent.click(screen.getByRole("link", { name: "Photos" }));
    await waitFor(() =>
      expect(router.state.location.search).toEqual({ library: "lib_docs", folder: "nod_photos" }),
    );
    expect(await within(folderPath()).findByText("Photos")).toBeDefined();
    expect(await screen.findByText("This folder is empty")).toBeDefined();

    fireEvent.click(screen.getByRole("button", { name: "New folder" }));
    const dialog = await screen.findByRole("dialog");
    fill(dialog, "Name", "2026");
    fireEvent.click(within(dialog).getByRole("button", { name: "Create folder" }));
    expect(await screen.findByRole("link", { name: "2026" })).toBeDefined();
    expect(body).toEqual({ parent_id: "nod_photos", name: "2026" });
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());

    fireEvent.click(within(folderPath()).getByRole("link", { name: "Documents" }));
    expect(await screen.findByRole("link", { name: "Photos" })).toBeDefined();
    expect(router.state.location.search).toEqual({ library: "lib_docs" });
  });

  it("rejects a folder name with a separator before it sends a request", async () => {
    const fetchMock = stubApi({
      "GET /api/v1/auth/me": () => jsonResponse(200, testUser),
      "GET /api/v1/libraries": () => jsonResponse(200, [documents]),
      "GET /api/v1/libraries/lib_docs/nodes": () => jsonResponse(200, { items: [] }),
    });
    await renderApp("/");
    fireEvent.click(await screen.findByRole("button", { name: "New folder" }));
    const dialog = await screen.findByRole("dialog");
    fill(dialog, "Name", "a/b");
    fireEvent.click(within(dialog).getByRole("button", { name: "Create folder" }));
    expect(
      await within(dialog).findByText("Do not use slashes, NUL, dot, or dot-dot."),
    ).toBeDefined();
    expect(fetchMock).toHaveBeenCalledTimes(3);
  });

  it("shows the server error when a folder already exists", async () => {
    stubApi({
      "GET /api/v1/auth/me": () => jsonResponse(200, testUser),
      "GET /api/v1/libraries": () => jsonResponse(200, [documents]),
      "GET /api/v1/libraries/lib_docs/nodes": () => jsonResponse(200, { items: [] }),
      "POST /api/v1/libraries/lib_docs/folders": () =>
        jsonResponse(409, {
          status: 409,
          detail: "A file or folder with this name already exists.",
        }),
    });
    await renderApp("/");
    fireEvent.click(await screen.findByRole("button", { name: "New folder" }));
    const dialog = await screen.findByRole("dialog");
    fill(dialog, "Name", "Photos");
    fireEvent.click(within(dialog).getByRole("button", { name: "Create folder" }));
    expect(
      await within(dialog).findByText("A file or folder with this name already exists."),
    ).toBeDefined();
  });

  it("loads the next page of a folder", async () => {
    stubApi({
      "GET /api/v1/auth/me": () => jsonResponse(200, testUser),
      "GET /api/v1/libraries": () => jsonResponse(200, [documents]),
      "GET /api/v1/libraries/lib_docs/nodes": () =>
        jsonResponse(200, { items: [folder("nod_a", "Alpha")], next_cursor: "nod_a" }),
      "GET /api/v1/libraries/lib_docs/nodes?cursor=nod_a": () =>
        jsonResponse(200, { items: [folder("nod_b", "Beta")] }),
    });
    await renderApp("/");
    fireEvent.click(await screen.findByRole("button", { name: "Load more" }));
    expect(await screen.findByRole("link", { name: "Beta" })).toBeDefined();
    expect(folderRows()).toHaveLength(3);
    expect(screen.queryByRole("button", { name: "Load more" })).toBeNull();
  });

  it("unlocks an encrypted library in a dialog and encrypts new names", async () => {
    const { library, keys } = await encryptedLibrary();
    const secret: Node = {
      ...folder("nod_secret", "", library.root_node_id),
      library_id: library.id,
      name: undefined,
      encrypted_name: await encryptName(keys, "Tax returns"),
      name_token: await nameToken(keys, library.root_node_id, "Tax returns"),
    };
    let body: Record<string, string> | undefined;
    const fetchMock = stubApi({
      "GET /api/v1/auth/me": () => jsonResponse(200, testUser),
      "GET /api/v1/libraries": () => jsonResponse(200, [documents, library]),
      "GET /api/v1/libraries/lib_private/nodes": () => jsonResponse(200, { items: [secret] }),
      "POST /api/v1/libraries/lib_private/folders": (init) => {
        body = JSON.parse(init?.body as string);
        return jsonResponse(201, { ...secret, id: "nod_new", ...body });
      },
    });
    await renderApp("/?library=lib_private");
    expect(await screen.findByText("Private is locked")).toBeDefined();
    expect(screen.queryByRole("button", { name: "New folder" })).toBeNull();
    expect(fetchMock).toHaveBeenCalledTimes(2);

    fireEvent.click(screen.getByRole("button", { name: "Unlock" }));
    const dialog = await screen.findByRole("dialog");
    fill(dialog, "Passphrase", "wrong passphrase");
    fireEvent.click(within(dialog).getByRole("button", { name: "Unlock" }));
    expect(await within(dialog).findByText("The passphrase is incorrect.")).toBeDefined();

    fill(dialog, "Passphrase", passphrase);
    fireEvent.click(within(dialog).getByRole("button", { name: "Unlock" }));
    expect(await screen.findByRole("link", { name: "Tax returns" })).toBeDefined();
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());

    fireEvent.click(screen.getByRole("button", { name: "New folder" }));
    const folderDialog = await screen.findByRole("dialog");
    fill(folderDialog, "Name", "Receipts");
    fireEvent.click(within(folderDialog).getByRole("button", { name: "Create folder" }));
    await waitFor(() => expect(body).toBeDefined());
    expect(body?.name).toBeUndefined();
    expect(body?.parent_id).toBeUndefined();
    expect(fromBase64(body?.name_token ?? "")).toHaveLength(32);
    expect(body?.name_token).toBe(await nameToken(keys, library.root_node_id, "Receipts"));
    expect(body?.encrypted_name).not.toContain("Receipts");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());

    fireEvent.click(screen.getByRole("button", { name: "Lock" }));
    expect(await screen.findByText("Private is locked")).toBeDefined();
    expect(screen.queryByText("Tax returns")).toBeNull();
  });
});

describe("library setup", () => {
  it("opens the libraries page from the sidebar", async () => {
    stubApi({
      "GET /api/v1/auth/me": () => jsonResponse(200, testUser),
      "GET /api/v1/libraries": noLibraries,
      "GET /api/v1/storage-backends": () => jsonResponse(200, [backend]),
    });
    const router = await renderApp("/");
    fireEvent.click(await screen.findByRole("link", { name: "Libraries" }));
    expect(await screen.findByRole("heading", { name: "Libraries" })).toBeDefined();
    expect(router.state.location.pathname).toBe("/libraries");
    expect(screen.getByText("No libraries")).toBeDefined();
  });

  it("lists libraries and opens one in Files", async () => {
    const { library: secret } = await encryptedLibrary();
    stubApi({
      "GET /api/v1/auth/me": () => jsonResponse(200, testUser),
      "GET /api/v1/libraries": () => jsonResponse(200, [documents, secret]),
      "GET /api/v1/storage-backends": () => jsonResponse(200, [backend]),
      "GET /api/v1/libraries/lib_docs/nodes": () => jsonResponse(200, { items: [] }),
    });
    const router = await renderApp("/libraries");
    const table = await screen.findByRole("table", { name: "Libraries" });
    const [, plain, encrypted] = within(table).getAllByRole("row");
    expect(within(plain).getByText("Local disk")).toBeDefined();
    expect(within(plain).getByText("Not encrypted")).toBeDefined();
    expect(within(encrypted).getByText("End-to-end encrypted")).toBeDefined();

    fireEvent.click(within(plain).getByRole("link", { name: "Open Documents in Files" }));
    expect(await screen.findByRole("heading", { name: "Files" })).toBeDefined();
    expect(router.state.location.search).toEqual({ library: "lib_docs" });
    expect(await screen.findByText("This folder is empty")).toBeDefined();
  });

  it("tells a user without backends to ask an administrator", async () => {
    stubApi({
      "GET /api/v1/auth/me": () => jsonResponse(200, testUser),
      "GET /api/v1/libraries": noLibraries,
      "GET /api/v1/storage-backends": () => jsonResponse(200, []),
    });
    await renderApp("/libraries");
    fireEvent.click(await screen.findByRole("button", { name: "New library" }));
    const dialog = await screen.findByRole("dialog");
    expect(
      within(dialog).getByText("Ask an administrator to add a storage backend."),
    ).toBeDefined();
    expect(
      within(dialog).getByRole("button", { name: "Create library" }).hasAttribute("disabled"),
    ).toBe(true);
  });

  it("creates an unencrypted library", async () => {
    let body: unknown;
    let libraries: Library[] = [];
    stubApi({
      "GET /api/v1/auth/me": () => jsonResponse(200, { ...testUser, is_admin: true } as User),
      "GET /api/v1/libraries": () => jsonResponse(200, libraries),
      "GET /api/v1/storage-backends": () => jsonResponse(200, [backend]),
      "POST /api/v1/libraries": (init) => {
        body = JSON.parse(init?.body as string);
        libraries = [documents];
        return jsonResponse(201, documents);
      },
    });
    const router = await renderApp("/libraries");
    fireEvent.click(await screen.findByRole("button", { name: "New library" }));
    const dialog = await screen.findByRole("dialog");
    fill(dialog, "Name", "  Documents ");
    fireEvent.click(within(dialog).getByRole("button", { name: "Create library" }));
    expect(await screen.findByRole("table", { name: "Libraries" })).toBeDefined();
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(router.state.location.pathname).toBe("/libraries");
    expect(body).toEqual({ name: "Documents", backend_id: "stb_local", encryption_mode: "none" });
  });

  it("validates the passphrase of an encrypted library", async () => {
    const fetchMock = stubApi({
      "GET /api/v1/auth/me": () => jsonResponse(200, testUser),
      "GET /api/v1/libraries": noLibraries,
      "GET /api/v1/storage-backends": () => jsonResponse(200, [backend]),
    });
    await renderApp("/libraries");
    fireEvent.click(await screen.findByRole("button", { name: "New library" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("radio", { name: /End-to-end encrypted/ }));
    fill(dialog, "Name", "Private");
    fill(dialog, "Passphrase", "short");
    fill(dialog, "Confirm passphrase", "different");
    fireEvent.click(within(dialog).getByRole("button", { name: "Create library" }));
    expect(
      await within(dialog).findByText("Use a passphrase with 12 or more characters."),
    ).toBeDefined();
    expect(within(dialog).getByText("The passphrases do not match.")).toBeDefined();
    expect(fetchMock).toHaveBeenCalledTimes(3);
  });

  it("creates an encrypted library that stays unlocked in Files", async () => {
    let created: Library | undefined;
    stubApi({
      "GET /api/v1/auth/me": () => jsonResponse(200, testUser),
      "GET /api/v1/libraries": () => jsonResponse(200, created ? [created] : []),
      "GET /api/v1/storage-backends": () => jsonResponse(200, [backend]),
      "POST /api/v1/libraries": (init) => {
        const body = JSON.parse(init?.body as string);
        created = {
          ...documents,
          id: "lib_private",
          name: body.name,
          encryption_mode: body.encryption_mode,
          key_envelope: body.key_envelope,
        };
        return jsonResponse(201, created);
      },
      "GET /api/v1/libraries/lib_private/nodes": () => jsonResponse(200, { items: [] }),
    });
    await renderApp("/libraries");
    fireEvent.click(await screen.findByRole("button", { name: "New library" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("radio", { name: /End-to-end encrypted/ }));
    fill(dialog, "Name", "Private");
    fill(dialog, "Passphrase", passphrase);
    fill(dialog, "Confirm passphrase", passphrase);
    fireEvent.click(within(dialog).getByRole("button", { name: "Create library" }));
    fireEvent.click(await screen.findByRole("link", { name: "Open Private in Files" }));
    expect(await screen.findByText("This folder is empty")).toBeDefined();
    expect(screen.queryByText("Private is locked")).toBeNull();
    expect(created?.encryption_mode).toBe("e2ee");
    await expect(openKeyEnvelope(created?.key_envelope ?? "", passphrase)).resolves.toBeDefined();
  });
});
