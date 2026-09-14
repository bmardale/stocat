import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vite-plus/test";
import type { Library, TrashedFile } from "@/api/generated/model";
import { jsonResponse, renderApp, stubApi, testUser } from "@/test/app";

const library: Library = {
  id: "lib_docs",
  name: "Documents",
  encryption_mode: "none",
  root_node_id: "nod_root",
  backend: { id: "stb_local", name: "Local disk", type: "local" },
  quota_mb: null,
  created_at: "2026-09-10T10:00:00Z",
  updated_at: "2026-09-10T10:00:00Z",
};

function trashed(id: string, name: string): TrashedFile {
  return {
    id,
    name,
    library_id: library.id,
    library_name: library.name,
    parent_id: library.root_node_id,
    revision: 1,
    encryption_mode: "none",
    trashed_at: "2026-09-12T10:00:00Z",
    delete_after: "2026-10-12T10:00:00Z",
  };
}

describe("trash", () => {
  it("restores a file and permanently deletes another file", async () => {
    let items = [trashed("nod_notes", "notes.txt"), trashed("nod_old", "old.txt")];
    let restored = false;
    let deleted = false;
    stubApi({
      "GET /api/v1/auth/me": () => jsonResponse(200, testUser),
      "GET /api/v1/libraries": () => jsonResponse(200, [library]),
      "GET /api/v1/files/trash": () => jsonResponse(200, { items }),
      "POST /api/v1/files/nod_notes/restore": () => {
        restored = true;
        items = items.filter((item) => item.id !== "nod_notes");
        return jsonResponse(200, {
          id: "nod_notes",
          name: "notes.txt",
          kind: "file",
          library_id: library.id,
          parent_id: library.root_node_id,
          revision: 1,
          created_at: "2026-09-10T10:00:00Z",
          updated_at: "2026-09-14T10:00:00Z",
        });
      },
      "DELETE /api/v1/files/nod_old/permanent": () => {
        deleted = true;
        items = [];
        return new Response(null, { status: 204 });
      },
    });

    await renderApp("/trash");
    expect(await screen.findByRole("heading", { name: "Trash" })).toBeDefined();
    const notesRow = screen.getByText("notes.txt").closest("tr");
    if (!notesRow) throw new Error("Missing notes row.");
    fireEvent.click(within(notesRow).getByRole("button", { name: "Restore" }));
    await waitFor(() => expect(restored).toBe(true));
    await waitFor(() => expect(screen.queryByText("notes.txt")).toBeNull());

    const oldRow = screen.getByText("old.txt").closest("tr");
    if (!oldRow) throw new Error("Missing old file row.");
    fireEvent.click(within(oldRow).getByRole("button", { name: "Delete" }));
    const dialog = await screen.findByRole("alertdialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Delete permanently" }));
    await waitFor(() => expect(deleted).toBe(true));
    expect(await screen.findByText("Trash is empty")).toBeDefined();
  });
});
