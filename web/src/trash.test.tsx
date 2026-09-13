import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vite-plus/test";
import type { Library, LibraryBackend, TrashItem } from "@/api/generated/model";
import { jsonResponse, renderApp, stubApi, testUser } from "@/test/app";

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

const deletedFile: TrashItem = {
  id: "nod_note",
  library_id: "lib_docs",
  library_name: "Documents",
  encryption_mode: "none",
  parent_id: "nod_root",
  kind: "file",
  name: "notes.txt",
  revision: 1,
  trashed_at: "2026-09-01T10:00:00Z",
  expires_at: "2099-10-01T10:00:00Z",
  created_at: "2026-08-01T10:00:00Z",
  updated_at: "2026-09-01T10:00:00Z",
};

describe("trash", () => {
  it("lists trashed items and restores one", async () => {
    let items = [deletedFile];
    stubApi({
      "GET /api/v1/auth/me": () => jsonResponse(200, testUser),
      "GET /api/v1/libraries": () => jsonResponse(200, [documents]),
      "GET /api/v1/trash?limit=100": () => jsonResponse(200, { items }),
      "POST /api/v1/trash/nod_note/restore": () => {
        items = [];
        return new Response(null, { status: 204 });
      },
    });
    await renderApp("/trash");
    const table = await screen.findByRole("table", { name: "Trashed items" });
    expect(within(table).getByText("notes.txt")).toBeDefined();
    expect(within(table).getByText("Documents")).toBeDefined();

    fireEvent.click(screen.getByRole("button", { name: "Actions for notes.txt" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "Restore" }));
    expect(await screen.findByText("The trash is empty")).toBeDefined();
  });

  it("permanently deletes a trashed item after confirmation", async () => {
    let items = [deletedFile];
    let deleted = false;
    stubApi({
      "GET /api/v1/auth/me": () => jsonResponse(200, testUser),
      "GET /api/v1/libraries": () => jsonResponse(200, [documents]),
      "GET /api/v1/trash?limit=100": () => jsonResponse(200, { items }),
      "DELETE /api/v1/trash/nod_note": () => {
        deleted = true;
        items = [];
        return new Response(null, { status: 204 });
      },
    });
    await renderApp("/trash");
    await screen.findByRole("table", { name: "Trashed items" });

    fireEvent.click(screen.getByRole("button", { name: "Actions for notes.txt" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "Delete permanently" }));
    const confirm = await screen.findByRole("alertdialog");
    expect(within(confirm).getByText("Permanently delete notes.txt?")).toBeDefined();
    fireEvent.click(within(confirm).getByRole("button", { name: "Delete permanently" }));
    expect(await screen.findByText("The trash is empty")).toBeDefined();
    expect(deleted).toBe(true);
  });

  it("empties the trash", async () => {
    let items = [deletedFile];
    let emptied = false;
    stubApi({
      "GET /api/v1/auth/me": () => jsonResponse(200, testUser),
      "GET /api/v1/libraries": () => jsonResponse(200, [documents]),
      "GET /api/v1/trash?limit=100": () => jsonResponse(200, { items }),
      "DELETE /api/v1/trash": () => {
        emptied = true;
        items = [];
        return new Response(null, { status: 204 });
      },
    });
    await renderApp("/trash");
    await screen.findByRole("table", { name: "Trashed items" });

    fireEvent.click(screen.getByRole("button", { name: "Empty trash" }));
    const confirm = await screen.findByRole("alertdialog");
    fireEvent.click(within(confirm).getByRole("button", { name: "Empty trash" }));
    await waitFor(() => expect(screen.getByText("The trash is empty")).toBeDefined());
    expect(emptied).toBe(true);
  });
});
