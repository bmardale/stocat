import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vite-plus/test";
import type { FileDetails, Library, LibraryBackend, Node, User } from "@/api/generated/model";
import { encryptFile } from "@/lib/file-crypto";
import {
  createKeyEnvelope,
  decryptName,
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

function fileDetails(id: string, size: number): FileDetails {
  return {
    id,
    library_id: "lib_docs",
    version_id: `ver_${id}`,
    revision: 1,
    size,
    stored_size: size,
    content_url: `/api/v1/files/${id}/content`,
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
    await waitFor(() => expect(document.querySelector('[data-slot="dialog-content"]')).toBeNull());
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
    await waitFor(() => expect(document.querySelector('[data-slot="dialog-content"]')).toBeNull());

    fireEvent.click(within(folderPath()).getByRole("link", { name: "Documents" }));
    expect(await screen.findByRole("link", { name: "Photos" })).toBeDefined();
    expect(router.state.location.search).toEqual({ library: "lib_docs" });
  });

  it("uploads a file and shows it after publication", async () => {
    let published = false;
    let createBody: Record<string, unknown> | undefined;
    const uploaded: Node = { ...folder("nod_notes", "notes.txt"), kind: "file" };
    stubApi({
      "GET /api/v1/auth/me": () => jsonResponse(200, testUser),
      "GET /api/v1/libraries": () => jsonResponse(200, [documents]),
      "GET /api/v1/libraries/lib_docs/nodes": () =>
        jsonResponse(200, { items: published ? [uploaded] : [] }),
      "POST /api/v1/uploads": (init) => {
        createBody = JSON.parse(init?.body as string);
        return jsonResponse(201, {
          id: "upl_notes",
          state: "created",
          upload_url: "/api/v1/uploads/upl_notes/content",
          declared_size: 5,
          offset: 0,
          expires_at: "2026-09-15T10:00:00Z",
        });
      },
      "HEAD /api/v1/uploads/upl_notes/content": () =>
        new Response(null, { status: 200, headers: { "Upload-Offset": "0" } }),
      "PATCH /api/v1/uploads/upl_notes/content": (init) => {
        expect(new Headers(init?.headers).get("Upload-Offset")).toBe("0");
        expect((init?.body as Blob | undefined)?.size).toBe(5);
        return new Response(null, { status: 204, headers: { "Upload-Offset": "5" } });
      },
      "POST /api/v1/uploads/upl_notes/complete": () => {
        return jsonResponse(200, {
          id: "upl_notes",
          state: "finalizing",
          upload_url: "/api/v1/uploads/upl_notes/content",
          declared_size: 5,
          offset: 5,
          expires_at: "2026-09-15T10:00:00Z",
        });
      },
      "GET /api/v1/uploads/upl_notes": () => {
        published = true;
        return jsonResponse(200, {
          id: "upl_notes",
          state: "completed",
          upload_url: "/api/v1/uploads/upl_notes/content",
          declared_size: 5,
          offset: 5,
          expires_at: "2026-09-15T10:00:00Z",
          node_id: "nod_notes",
          version_id: "ver_notes",
        });
      },
    });
    await renderApp("/");
    const input = await screen.findByLabelText("Choose files to upload");
    fireEvent.change(input, { target: { files: [new File(["hello"], "notes.txt")] } });

    expect(await screen.findByText("Complete")).toBeDefined();
    await waitFor(() => expect(screen.getAllByText("notes.txt").length).toBeGreaterThanOrEqual(2));
    expect(createBody).toEqual({
      library_id: "lib_docs",
      parent_id: "nod_root",
      name: "notes.txt",
      size: 5,
    });
  });

  it("cancels an active upload and releases its session", async () => {
    let patchStarted = false;
    let cancelled = false;
    stubApi({
      "GET /api/v1/auth/me": () => jsonResponse(200, testUser),
      "GET /api/v1/libraries": () => jsonResponse(200, [documents]),
      "GET /api/v1/libraries/lib_docs/nodes": () => jsonResponse(200, { items: [] }),
      "POST /api/v1/uploads": () =>
        jsonResponse(201, {
          id: "upl_large",
          state: "created",
          upload_url: "/api/v1/uploads/upl_large/content",
          declared_size: 5,
          offset: 0,
          expires_at: "2026-09-15T10:00:00Z",
        }),
      "HEAD /api/v1/uploads/upl_large/content": () =>
        new Response(null, { status: 200, headers: { "Upload-Offset": "0" } }),
      "PATCH /api/v1/uploads/upl_large/content": (init) => {
        patchStarted = true;
        return new Promise<Response>((_resolve, reject) => {
          init?.signal?.addEventListener("abort", () =>
            reject(new DOMException("The upload was cancelled.", "AbortError")),
          );
        });
      },
      "DELETE /api/v1/uploads/upl_large": () => {
        cancelled = true;
        return new Response(null, { status: 204 });
      },
    });
    await renderApp("/");
    fireEvent.change(await screen.findByLabelText("Choose files to upload"), {
      target: { files: [new File(["hello"], "large.bin")] },
    });
    await waitFor(() => expect(patchStarted).toBe(true));
    fireEvent.click(screen.getByRole("button", { name: "Cancel large.bin" }));

    expect(await screen.findByText("Cancelled")).toBeDefined();
    await waitFor(() => expect(cancelled).toBe(true));
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
    await waitFor(() => expect(document.querySelector('[data-slot="dialog-content"]')).toBeNull());

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
    await waitFor(() => expect(document.querySelector('[data-slot="dialog-content"]')).toBeNull());

    fireEvent.click(screen.getByRole("button", { name: "Lock" }));
    expect(await screen.findByText("Private is locked")).toBeDefined();
    expect(screen.queryByText("Tax returns")).toBeNull();
  });

  it("encrypts file content and metadata before an upload", async () => {
    const { library } = await encryptedLibrary();
    let createBody: Record<string, unknown> | undefined;
    let completeBody: Record<string, unknown> | undefined;
    let published = false;
    let offset = 0;
    stubApi({
      "GET /api/v1/auth/me": () => jsonResponse(200, testUser),
      "GET /api/v1/libraries": () => jsonResponse(200, [library]),
      "GET /api/v1/libraries/lib_private/nodes": () =>
        jsonResponse(200, {
          items:
            published && createBody
              ? [
                  {
                    ...folder("nod_report", "", library.root_node_id),
                    library_id: library.id,
                    kind: "file",
                    name: undefined,
                    encrypted_name: createBody.encrypted_name,
                    name_token: createBody.name_token,
                  },
                ]
              : [],
        }),
      "POST /api/v1/uploads": (init) => {
        createBody = JSON.parse(init?.body as string);
        return jsonResponse(201, {
          id: "upl_report",
          state: "created",
          upload_url: "/api/v1/uploads/upl_report/content",
          declared_size: createBody?.size,
          offset: 0,
          expires_at: "2026-09-15T10:00:00Z",
        });
      },
      "HEAD /api/v1/uploads/upl_report/content": () =>
        new Response(null, { status: 200, headers: { "Upload-Offset": String(offset) } }),
      "PATCH /api/v1/uploads/upl_report/content": (init) => {
        expect(Number(new Headers(init?.headers).get("Upload-Offset"))).toBe(offset);
        offset += (init?.body as Blob | undefined)?.size ?? 0;
        return new Response(null, {
          status: 204,
          headers: { "Upload-Offset": String(offset) },
        });
      },
      "POST /api/v1/uploads/upl_report/complete": (init) => {
        completeBody = JSON.parse(init?.body as string);
        published = true;
        return jsonResponse(200, {
          id: "upl_report",
          state: "completed",
          upload_url: "/api/v1/uploads/upl_report/content",
          declared_size: createBody?.size,
          offset,
          expires_at: "2026-09-15T10:00:00Z",
          node_id: "nod_report",
          version_id: "ver_report",
        });
      },
    });
    await renderApp("/");
    fireEvent.click(await screen.findByRole("button", { name: "Unlock" }));
    const dialog = await screen.findByRole("dialog");
    fill(dialog, "Passphrase", passphrase);
    fireEvent.click(within(dialog).getByRole("button", { name: "Unlock" }));
    await screen.findByText("This folder is empty");

    fireEvent.change(screen.getByLabelText("Choose files to upload"), {
      target: { files: [new File(["hello"], "report.txt")] },
    });

    expect(await screen.findByText("Complete")).toBeDefined();
    await waitFor(() => expect(screen.getAllByText("report.txt").length).toBeGreaterThanOrEqual(2));
    expect(createBody?.name).toBeUndefined();
    expect(createBody?.encrypted_name).not.toContain("report.txt");
    expect(fromBase64(createBody?.name_token as string)).toHaveLength(32);
    expect(createBody?.size).toBe(53);
    expect(offset).toBe(53);
    expect(completeBody?.encryption_format).toBe("stocat-framed-v1");
    expect(fromBase64(completeBody?.dedup_fingerprint as string)).toHaveLength(32);
    expect(fromBase64(completeBody?.encrypted_file_key as string)).toHaveLength(61);
  });

  it("previews a text file and downloads it", async () => {
    const note: Node = { ...folder("nod_note", "notes.txt"), kind: "file" };
    stubApi({
      "GET /api/v1/auth/me": () => jsonResponse(200, testUser),
      "GET /api/v1/libraries": () => jsonResponse(200, [documents]),
      "GET /api/v1/libraries/lib_docs/nodes": () => jsonResponse(200, { items: [note] }),
      "GET /api/v1/files/nod_note": () => jsonResponse(200, fileDetails("nod_note", 11)),
      "GET /api/v1/files/nod_note/content?disposition=inline": () => new Response("hello world"),
    });
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
    await renderApp("/");
    fireEvent.click(await screen.findByRole("button", { name: "notes.txt" }));
    const dialog = await screen.findByRole("dialog");
    expect(await within(dialog).findByText("hello world")).toBeDefined();
    expect(within(dialog).getByText("11 B")).toBeDefined();

    fireEvent.click(within(dialog).getByRole("button", { name: "Download" }));
    await waitFor(() => expect(click).toHaveBeenCalledTimes(1));
    const anchor = click.mock.contexts[0] as HTMLAnchorElement;
    expect(anchor.getAttribute("href")).toBe(
      "/api/v1/files/nod_note/content?disposition=attachment",
    );
    expect(anchor.download).toBe("notes.txt");
  });

  it("explains when a file type has no preview", async () => {
    const archive: Node = { ...folder("nod_archive", "backup.zip"), kind: "file" };
    stubApi({
      "GET /api/v1/auth/me": () => jsonResponse(200, testUser),
      "GET /api/v1/libraries": () => jsonResponse(200, [documents]),
      "GET /api/v1/libraries/lib_docs/nodes": () => jsonResponse(200, { items: [archive] }),
      "GET /api/v1/files/nod_archive": () => jsonResponse(200, fileDetails("nod_archive", 2048)),
    });
    await renderApp("/");
    fireEvent.click(await screen.findByRole("button", { name: "Actions for backup.zip" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "Preview" }));
    const dialog = await screen.findByRole("dialog");
    expect(
      await within(dialog).findByText("This file type does not have a preview."),
    ).toBeDefined();
    expect(within(dialog).getByText("2.0 KiB")).toBeDefined();
  });

  it("renames a file and moves it to the trash after confirmation", async () => {
    let items: Node[] = [{ ...folder("nod_note", "notes.txt"), kind: "file" }];
    let renameBody: unknown;
    let trashed = false;
    stubApi({
      "GET /api/v1/auth/me": () => jsonResponse(200, testUser),
      "GET /api/v1/libraries": () => jsonResponse(200, [documents]),
      "GET /api/v1/libraries/lib_docs/nodes": () => jsonResponse(200, { items }),
      "PATCH /api/v1/files/nod_note": (init) => {
        renameBody = JSON.parse(init?.body as string);
        items = [{ ...items[0], name: "todo.txt" }];
        return jsonResponse(200, items[0]);
      },
      "DELETE /api/v1/files/nod_note": () => {
        trashed = true;
        items = [];
        return new Response(null, { status: 204 });
      },
    });
    await renderApp("/");
    fireEvent.click(await screen.findByRole("button", { name: "Actions for notes.txt" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "Rename" }));
    const dialog = await screen.findByRole("dialog");
    fill(dialog, "Name", "todo.txt");
    fireEvent.click(within(dialog).getByRole("button", { name: "Rename" }));
    expect(await screen.findByRole("button", { name: "todo.txt" })).toBeDefined();
    expect(renameBody).toEqual({ name: "todo.txt" });
    await waitFor(() => expect(document.querySelector('[data-slot="dialog-content"]')).toBeNull());

    fireEvent.click(screen.getByRole("button", { name: "Actions for todo.txt" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "Move to trash" }));
    const confirm = await screen.findByRole("alertdialog");
    expect(within(confirm).getByText("Move todo.txt to the trash?")).toBeDefined();
    fireEvent.click(within(confirm).getByRole("button", { name: "Move to trash" }));
    expect(await screen.findByText("This folder is empty")).toBeDefined();
    expect(trashed).toBe(true);
  });

  it("moves a folder to the trash", async () => {
    let items: Node[] = [folder("nod_photos", "Photos")];
    let trashed = false;
    stubApi({
      "GET /api/v1/auth/me": () => jsonResponse(200, testUser),
      "GET /api/v1/libraries": () => jsonResponse(200, [documents]),
      "GET /api/v1/libraries/lib_docs/nodes": () => jsonResponse(200, { items }),
      "DELETE /api/v1/libraries/lib_docs/folders/nod_photos": () => {
        trashed = true;
        items = [];
        return new Response(null, { status: 204 });
      },
    });
    await renderApp("/");
    fireEvent.click(await screen.findByRole("button", { name: "Actions for Photos" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "Move to trash" }));
    const confirm = await screen.findByRole("alertdialog");
    expect(within(confirm).getByText("Move Photos to the trash?")).toBeDefined();
    fireEvent.click(within(confirm).getByRole("button", { name: "Move to trash" }));
    expect(await screen.findByText("This folder is empty")).toBeDefined();
    expect(trashed).toBe(true);
  });

  it("encrypts the new name of a file in an encrypted library", async () => {
    const { library, keys } = await encryptedLibrary();
    const report: Node = {
      ...folder("nod_report", "", library.root_node_id),
      library_id: library.id,
      kind: "file",
      name: undefined,
      encrypted_name: await encryptName(keys, "report.txt"),
    };
    let body: Record<string, string> | undefined;
    stubApi({
      "GET /api/v1/auth/me": () => jsonResponse(200, testUser),
      "GET /api/v1/libraries": () => jsonResponse(200, [library]),
      "GET /api/v1/libraries/lib_private/nodes": () => jsonResponse(200, { items: [report] }),
      "PATCH /api/v1/files/nod_report": (init) => {
        body = JSON.parse(init?.body as string);
        return jsonResponse(200, { ...report, ...body });
      },
    });
    await renderApp("/");
    fireEvent.click(await screen.findByRole("button", { name: "Unlock" }));
    const unlock = await screen.findByRole("dialog");
    fill(unlock, "Passphrase", passphrase);
    fireEvent.click(within(unlock).getByRole("button", { name: "Unlock" }));
    const actions = await screen.findByRole("button", { name: "Actions for report.txt" });
    await waitFor(() => expect(document.querySelector('[data-slot="dialog-content"]')).toBeNull());

    fireEvent.click(actions);
    fireEvent.click(await screen.findByRole("menuitem", { name: "Rename" }));
    const dialog = await screen.findByRole("dialog");
    fill(dialog, "Name", "summary.txt");
    fireEvent.click(within(dialog).getByRole("button", { name: "Rename" }));
    await waitFor(() => expect(body).toBeDefined());
    expect(body?.name).toBeUndefined();
    expect(body?.name_token).toBe(await nameToken(keys, library.root_node_id, "summary.txt"));
    expect(await decryptName(keys, body?.encrypted_name ?? "")).toBe("summary.txt");
  });

  it("decrypts an encrypted file for preview and download", async () => {
    const { library, keys } = await encryptedLibrary();
    const encrypted = await encryptFile(new Blob(["top secret"]), keys, 64 * 1024);
    const ciphertext = new Uint8Array(await new Response(encrypted.stream).arrayBuffer());
    const report: Node = {
      ...folder("nod_report", "", library.root_node_id),
      library_id: library.id,
      kind: "file",
      name: undefined,
      encrypted_name: await encryptName(keys, "report.txt"),
    };
    const details: FileDetails = {
      ...fileDetails("nod_report", 10),
      library_id: library.id,
      stored_size: ciphertext.length,
      encryption_format: encrypted.encryptionFormat,
      encrypted_file_key: encrypted.encryptedFileKey,
    };
    stubApi({
      "GET /api/v1/auth/me": () => jsonResponse(200, testUser),
      "GET /api/v1/libraries": () => jsonResponse(200, [library]),
      "GET /api/v1/libraries/lib_private/nodes": () => jsonResponse(200, { items: [report] }),
      "GET /api/v1/files/nod_report": () => jsonResponse(200, details),
      "GET /api/v1/files/nod_report/content?disposition=inline": () => new Response(ciphertext),
      "GET /api/v1/files/nod_report/content?disposition=attachment": () => new Response(ciphertext),
    });
    const written: string[] = [];
    Object.defineProperty(window, "showSaveFilePicker", {
      configurable: true,
      value: async () => ({
        createWritable: async () =>
          new WritableStream<Uint8Array>({
            write: (chunk) => {
              written.push(new TextDecoder().decode(chunk));
            },
          }),
      }),
    });
    try {
      await renderApp("/");
      fireEvent.click(await screen.findByRole("button", { name: "Unlock" }));
      const unlock = await screen.findByRole("dialog");
      fill(unlock, "Passphrase", passphrase);
      fireEvent.click(within(unlock).getByRole("button", { name: "Unlock" }));
      const name = await screen.findByRole("button", { name: "report.txt" });
      await waitFor(() =>
        expect(document.querySelector('[data-slot="dialog-content"]')).toBeNull(),
      );

      fireEvent.click(name);
      const dialog = await screen.findByRole("dialog");
      expect(await within(dialog).findByText("top secret")).toBeDefined();
      expect(within(dialog).getByText("10 B · End-to-end encrypted")).toBeDefined();
      fireEvent.click(within(dialog).getByRole("button", { name: "Download" }));
      await waitFor(() => expect(written.join("")).toBe("top secret"));
    } finally {
      Reflect.deleteProperty(window, "showSaveFilePicker");
    }
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
    await waitFor(() => expect(document.querySelector('[data-slot="dialog-content"]')).toBeNull());
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
