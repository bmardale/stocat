import { fireEvent, screen, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vite-plus/test";
import { jsonResponse, noLibraries, renderApp, stubApi, testUser, unauthorized } from "@/test/app";

function fill(label: string, value: string) {
  fireEvent.change(screen.getByLabelText(label), { target: { value } });
}

describe("auth", () => {
  it("sends a guest from home to sign in without a redirect path", async () => {
    stubApi({ "GET /api/v1/auth/me": unauthorized });
    const router = await renderApp("/");
    expect(await screen.findByRole("button", { name: "Sign in" })).toBeDefined();
    expect(router.state.location.pathname).toBe("/login");
    expect(router.state.location.search).toEqual({});
  });

  it("keeps the requested path when it sends a guest to sign in", async () => {
    stubApi({ "GET /api/v1/auth/me": unauthorized });
    const router = await renderApp("/?view=grid");
    expect(await screen.findByRole("button", { name: "Sign in" })).toBeDefined();
    expect(router.state.location.pathname).toBe("/login");
    expect(router.state.location.search).toEqual({ redirect: "/?view=grid" });
  });

  it("sends a signed-in user from sign in to home", async () => {
    stubApi({
      "GET /api/v1/auth/me": () => jsonResponse(200, testUser),
      "GET /api/v1/libraries": noLibraries,
    });
    const router = await renderApp("/login");
    expect(await screen.findByRole("heading", { name: "Files" })).toBeDefined();
    expect(router.state.location.pathname).toBe("/");
  });

  it("validates the sign-in form before it sends a request", async () => {
    const fetchMock = stubApi({ "GET /api/v1/auth/me": unauthorized });
    await renderApp("/login");
    fireEvent.click(screen.getByRole("button", { name: "Sign in" }));
    expect(await screen.findByText("Enter a valid email address.")).toBeDefined();
    expect(screen.getByText("Enter your password.")).toBeDefined();
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("signs in and opens the redirect path", async () => {
    let body: unknown;
    stubApi({
      "GET /api/v1/auth/me": unauthorized,
      "GET /api/v1/libraries": noLibraries,
      "POST /api/v1/auth/login": (init) => {
        body = JSON.parse(init?.body as string);
        return jsonResponse(200, testUser);
      },
    });
    const router = await renderApp("/login?redirect=%2F");
    fill("Email", "ada@example.com");
    fill("Password", "correct horse battery staple");
    fireEvent.click(screen.getByRole("button", { name: "Sign in" }));
    expect(await screen.findByRole("button", { name: /Ada Lovelace/ })).toBeDefined();
    expect(router.state.location.pathname).toBe("/");
    expect(body).toEqual({ email: "ada@example.com", password: "correct horse battery staple" });
  });

  it("shows the server error when sign-in fails", async () => {
    stubApi({
      "GET /api/v1/auth/me": unauthorized,
      "POST /api/v1/auth/login": () =>
        jsonResponse(401, { status: 401, detail: "The email or password is incorrect." }),
    });
    await renderApp("/login");
    fill("Email", "ada@example.com");
    fill("Password", "wrong password");
    fireEvent.click(screen.getByRole("button", { name: "Sign in" }));
    expect(await screen.findByText("The email or password is incorrect.")).toBeDefined();
  });

  it("rejects a short password on registration", async () => {
    const fetchMock = stubApi({
      "GET /api/v1/auth/me": unauthorized,
      "GET /api/v1/auth/config": () => jsonResponse(200, { invite_only: false }),
    });
    await renderApp("/register");
    fill("Name", "Ada Lovelace");
    fill("Email", "ada@example.com");
    fill("Password", "too short");
    fireEvent.click(screen.getByRole("button", { name: "Create account" }));
    expect(await screen.findByText("The password is too short.")).toBeDefined();
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("registers and opens home", async () => {
    let body: unknown;
    stubApi({
      "GET /api/v1/auth/me": unauthorized,
      "GET /api/v1/auth/config": () => jsonResponse(200, { invite_only: false }),
      "GET /api/v1/libraries": noLibraries,
      "POST /api/v1/auth/register": (init) => {
        body = JSON.parse(init?.body as string);
        return jsonResponse(201, testUser);
      },
    });
    const router = await renderApp("/register");
    fill("Name", "Ada Lovelace");
    fill("Email", "ada@example.com");
    fill("Password", "correct horse battery staple");
    fireEvent.click(screen.getByRole("button", { name: "Create account" }));
    expect(await screen.findByRole("heading", { name: "Files" })).toBeDefined();
    expect(router.state.location.pathname).toBe("/");
    expect(body).toEqual({
      name: "Ada Lovelace",
      email: "ada@example.com",
      password: "correct horse battery staple",
    });
  });

  it("requires and sends an invite code when registration is restricted", async () => {
    let body: unknown;
    stubApi({
      "GET /api/v1/auth/me": unauthorized,
      "GET /api/v1/auth/config": () => jsonResponse(200, { invite_only: true }),
      "GET /api/v1/libraries": noLibraries,
      "POST /api/v1/auth/register": (init) => {
        body = JSON.parse(init?.body as string);
        return jsonResponse(201, testUser);
      },
    });
    const router = await renderApp("/register");
    expect(screen.getByLabelText("Invite code")).toBeDefined();
    fill("Name", "Ada Lovelace");
    fill("Email", "ada@example.com");
    fill("Invite code", "TEST-INVITE");
    fill("Password", "correct horse battery staple");
    fireEvent.click(screen.getByRole("button", { name: "Create account" }));
    expect(await screen.findByRole("heading", { name: "Files" })).toBeDefined();
    expect(router.state.location.pathname).toBe("/");
    expect(body).toEqual({
      name: "Ada Lovelace",
      email: "ada@example.com",
      password: "correct horse battery staple",
      invite_code: "TEST-INVITE",
    });
  });

  it("signs out and opens sign in", async () => {
    stubApi({
      "GET /api/v1/auth/me": () => jsonResponse(200, testUser),
      "GET /api/v1/libraries": noLibraries,
      "POST /api/v1/auth/logout": () => jsonResponse(204),
    });
    const router = await renderApp("/");
    fireEvent.click(await screen.findByRole("button", { name: /Ada Lovelace/ }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "Sign out" }));
    expect(await screen.findByRole("button", { name: "Sign in" })).toBeDefined();
    await waitFor(() => expect(router.state.location.pathname).toBe("/login"));
  });
});
