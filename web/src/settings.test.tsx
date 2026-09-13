import { fireEvent, screen, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vite-plus/test";
import { jsonResponse, renderApp, serverVersion, stubApi, testUser } from "@/test/app";

function fill(label: string, value: string) {
  fireEvent.change(screen.getByLabelText(label), { target: { value } });
}

describe("account settings", () => {
  it("updates the account details", async () => {
    let body: unknown;
    const updated = { ...testUser, name: "Grace Hopper", email: "grace@example.com" };
    stubApi({
      "GET /api/v1/auth/me": () => jsonResponse(200, testUser),
      "GET /api/v1/version": serverVersion,
      "PATCH /api/v1/auth/account": (init) => {
        body = JSON.parse(init?.body as string);
        return jsonResponse(200, updated);
      },
    });
    await renderApp("/settings");
    fill("Name", "  Grace Hopper  ");
    fill("Email", " GRACE@example.com ");
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    expect(await screen.findByText("Account details saved.")).toBeDefined();
    expect(body).toEqual({ name: "Grace Hopper", email: "GRACE@example.com" });
    expect((screen.getByLabelText("Name") as HTMLInputElement).value).toBe("Grace Hopper");
    expect((screen.getByLabelText("Email") as HTMLInputElement).value).toBe("grace@example.com");
    expect(screen.getByRole("button", { name: /Grace Hopper/ })).toBeDefined();
  });

  it("validates password confirmation before sending a request", async () => {
    const fetchMock = stubApi({
      "GET /api/v1/auth/me": () => jsonResponse(200, testUser),
      "GET /api/v1/version": serverVersion,
    });
    await renderApp("/settings");
    fill("Current password", "correct horse battery staple");
    fill("New password", "a different secure password");
    fill("Confirm new password", "another different password");
    fireEvent.click(screen.getByRole("button", { name: "Change password" }));
    expect(await screen.findByText("The passwords do not match.")).toBeDefined();
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("changes the password without sending its confirmation", async () => {
    let body: unknown;
    const fetchMock = stubApi({
      "GET /api/v1/auth/me": () => jsonResponse(200, testUser),
      "GET /api/v1/version": serverVersion,
      "PUT /api/v1/auth/password": (init) => {
        body = JSON.parse(init?.body as string);
        return jsonResponse(204);
      },
    });
    await renderApp("/settings");
    fill("Current password", "correct horse battery staple");
    fill("New password", "a different secure password");
    fill("Confirm new password", "a different secure password");
    fireEvent.click(screen.getByRole("button", { name: "Change password" }));
    expect(
      await screen.findByText("Password changed. Other devices are signed out."),
    ).toBeDefined();
    expect(body).toEqual({
      current_password: "correct horse battery staple",
      new_password: "a different secure password",
    });
    await waitFor(() =>
      expect((screen.getByLabelText("Current password") as HTMLInputElement).value).toBe(""),
    );
    expect(fetchMock).toHaveBeenCalledTimes(3);
  });

  it("shows the server error when the current password is wrong", async () => {
    stubApi({
      "GET /api/v1/auth/me": () => jsonResponse(200, testUser),
      "GET /api/v1/version": serverVersion,
      "PUT /api/v1/auth/password": () =>
        jsonResponse(422, { status: 422, detail: "The current password is incorrect." }),
    });
    await renderApp("/settings");
    fill("Current password", "incorrect password");
    fill("New password", "a different secure password");
    fill("Confirm new password", "a different secure password");
    fireEvent.click(screen.getByRole("button", { name: "Change password" }));
    expect(await screen.findByText("The current password is incorrect.")).toBeDefined();
  });
});
