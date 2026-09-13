import { createFileRoute } from "@tanstack/react-router";
import { useAuth } from "@/components/auth-provider";

export const Route = createFileRoute("/_app/_authenticated/dashboard/")({ component: Dashboard });

function Dashboard() {
  const { user } = useAuth();

  return (
    <section className="flex flex-col items-start gap-4">
      <h1 className="font-heading text-4xl font-bold tracking-tighter sm:text-6xl">Dashboard</h1>
      {user && <p className="leading-relaxed text-muted-foreground">Signed in as {user.email}.</p>}
    </section>
  );
}
