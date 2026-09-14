import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vite-plus/test";
import type { Library, TrashedFile } from "@/api/generated/model";
import { createKeyEnvelope, encryptName } from "@/lib/library-crypto";
import { jsonResponse, renderApp, stubApi, testUser } from "@/test/app";

const passphrase = "correct horse battery staple";

const library: Library = {
  id: "lib_docs",
  name: "Documents",
  encryption_mode: "none",
  root_node_id: "nod_root",
  backend: { id: "stb_local", name: "Local disk", type: "local" },
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

  it("decrypts trashed names once after the library unlocks", async () => {
    const { envelope, keys } = await createKeyEnvelope(passphrase);
    const privateLibrary: Library = {
      ...library,
      id: "lib_private",
      name: "Private",
      encryption_mode: "e2ee",
      root_node_id: "nod_private_root",
      key_envelope: envelope,
    };
    const secret: TrashedFile = {
      ...trashed("nod_secret", ""),
      name: undefined,
      encrypted_name: await encryptName(keys, "taxes.pdf"),
      library_id: privateLibrary.id,
      library_name: privateLibrary.name,
      parent_id: privateLibrary.root_node_id,
      encryption_mode: "e2ee",
    };
    stubApi({
      "GET /api/v1/auth/me": () => jsonResponse(200, testUser),
      "GET /api/v1/libraries": () => jsonResponse(200, [privateLibrary]),
      "GET /api/v1/files/trash": () => jsonResponse(200, { items: [secret] }),
    });

    await renderApp("/trash");
    expect(await screen.findByText("Name cannot be decrypted")).toBeDefined();
    fireEvent.click(screen.getByRole("button", { name: "Unlock" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.change(within(dialog).getByLabelText("Passphrase"), {
      target: { value: passphrase },
    });
    fireEvent.click(within(dialog).getByRole("button", { name: "Unlock" }));
    expect(await screen.findByText("taxes.pdf")).toBeDefined();

    // A render loop decrypts the names again and again.
    const decrypt = vi.spyOn(crypto.subtle, "decrypt");
    await new Promise((resolve) => setTimeout(resolve, 100));
    expect(decrypt).not.toHaveBeenCalled();
  });

  it("shows a permanent deletion error in the dialog", async () => {
    stubApi({
      "GET /api/v1/auth/me": () => jsonResponse(200, testUser),
      "GET /api/v1/libraries": () => jsonResponse(200, [library]),
      "GET /api/v1/files/trash": () =>
        jsonResponse(200, { items: [trashed("nod_old", "old.txt")] }),
      "DELETE /api/v1/files/nod_old/permanent": () =>
        jsonResponse(503, {
          title: "Service Unavailable",
          status: 503,
          detail: "File deletion is temporarily unavailable.",
        }),
    });

    await renderApp("/trash");
    fireEvent.click(await screen.findByRole("button", { name: "Delete" }));
    const dialog = await screen.findByRole("alertdialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Delete permanently" }));
    expect(
      await within(dialog).findByText("File deletion is temporarily unavailable."),
    ).toBeDefined();
  });
});
