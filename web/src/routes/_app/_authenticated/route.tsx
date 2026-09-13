import { createFileRoute, redirect } from "@tanstack/react-router";

export const Route = createFileRoute("/_app/_authenticated")({
  beforeLoad: ({ context, location }) => {
    if (!context.user) {
      throw redirect({ to: "/login", search: { redirect: location.href } });
    }
  },
});
