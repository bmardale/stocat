import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vite-plus/test";
import type { Session } from "@/api/generated/model";
import { jsonResponse, noLibraries, renderApp, stubApi, testUser } from "@/test/app";

const currentSession: Session = {
  id: "ses_current",
  user_agent:
    "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36",
  ip_address: "203.0.113.10",
  created_at: "2026-09-13T15:02:58Z",
  current: true,
};

const phoneSession: Session = {
  id: "ses_phone",
  user_agent:
    "Mozilla/5.0 (iPhone; CPU iPhone OS 18_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.6 Mobile/15E148 Safari/604.1",
  ip_address: null,
  created_at: "2026-09-01T08:30:00Z",
  current: false,
};

const laptopSession: Session = {
  id: "ses_laptop",
  user_agent: "Mozilla/5.0 (X11; Linux x86_64; rv:143.0) Gecko/20100101 Firefox/143.0",
  ip_address: "198.51.100.7",
  created_at: "2026-08-20T12:00:00Z",
  current: false,
};

function stubSessions(initial: Session[]) {
  let sessions = initial;
  const fetchMock = stubApi({
    "GET /api/v1/auth/me": () => jsonResponse(200, testUser),
    "GET /api/v1/libraries": noLibraries,
    "GET /api/v1/auth/sessions": () => jsonResponse(200, sessions),
    "DELETE /api/v1/auth/sessions": () => {
      sessions = sessions.filter((session) => session.current);
      return jsonResponse(204);
    },
    "DELETE /api/v1/auth/sessions/ses_phone": () => {
      sessions = sessions.filter((session) => session.id !== "ses_phone");
      return jsonResponse(204);
    },
  });
  return fetchMock;
}

const sessionItems = () =>
  within(screen.getByRole("list", { name: "Active sessions" })).getAllByRole("listitem");

describe("sessions", () => {
  it("opens settings from the user menu and navigates to sessions", async () => {
    stubSessions([currentSession]);
    const router = await renderApp("/");
    fireEvent.click(await screen.findByRole("button", { name: /Ada Lovelace/ }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "Settings" }));
    expect(await screen.findByRole("heading", { name: "Account" })).toBeDefined();
    expect(router.state.location.pathname).toBe("/settings");
    fireEvent.click(screen.getByRole("link", { name: "Sessions" }));
    expect(await screen.findByRole("heading", { name: "Sessions" })).toBeDefined();
    expect(router.state.location.pathname).toBe("/settings/sessions");
  });

  it("lists sessions with parsed user agents and creation dates", async () => {
    stubSessions([currentSession, phoneSession]);
    await renderApp("/settings/sessions");
    await screen.findByRole("list", { name: "Active sessions" });
    const items = sessionItems();
    expect(items).toHaveLength(2);
    expect(within(items[0]).getByText("Chrome 140 on macOS")).toBeDefined();
    expect(within(items[0]).getByText("This device")).toBeDefined();
    expect(within(items[0]).getByText(/203\.0\.113\.10/)).toBeDefined();
    expect(within(items[0]).queryByRole("button")).toBeNull();
    expect(within(items[1]).getByText("Safari 18 on iOS")).toBeDefined();
    expect(items[1].querySelector("time")?.getAttribute("dateTime")).toBe(phoneSession.created_at);
    expect(within(items[1]).getByText(/^Signed in/).textContent).not.toContain("·");
  });

  it("revokes one session", async () => {
    const fetchMock = stubSessions([currentSession, phoneSession, laptopSession]);
    await renderApp("/settings/sessions");
    fireEvent.click(await screen.findByRole("button", { name: "Revoke Safari 18 on iOS" }));
    await waitFor(() => expect(sessionItems()).toHaveLength(2));
    expect(screen.queryByText("Safari 18 on iOS")).toBeNull();
    expect(screen.getByText("Firefox 143 on Linux")).toBeDefined();
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/v1/auth/sessions/ses_phone",
      expect.objectContaining({ method: "DELETE" }),
    );
  });

  it("signs out other sessions", async () => {
    stubSessions([currentSession, phoneSession, laptopSession]);
    await renderApp("/settings/sessions");
    fireEvent.click(await screen.findByRole("button", { name: "Sign out other sessions" }));
    await waitFor(() => expect(sessionItems()).toHaveLength(1));
    expect(screen.getByText("This device")).toBeDefined();
    expect(
      screen.getByRole("button", { name: "Sign out other sessions" }).hasAttribute("disabled"),
    ).toBe(true);
  });

  it("shows the server error when a revoke fails", async () => {
    stubApi({
      "GET /api/v1/auth/me": () => jsonResponse(200, testUser),
      "GET /api/v1/auth/sessions": () => jsonResponse(200, [currentSession, phoneSession]),
      "DELETE /api/v1/auth/sessions/ses_phone": () =>
        jsonResponse(404, { status: 404, detail: "The session does not exist." }),
    });
    await renderApp("/settings/sessions");
    fireEvent.click(await screen.findByRole("button", { name: "Revoke Safari 18 on iOS" }));
    expect(await screen.findByText("The session does not exist.")).toBeDefined();
  });
});
