import { createFileRoute } from "@tanstack/react-router";
import { useAuth } from "@/components/auth-provider";

export const Route = createFileRoute("/_app/")({ component: Home });

function Home() {
  const { user } = useAuth();

  return (
    <section className="flex flex-col items-start gap-4">
      <h1 className="font-heading text-4xl font-bold tracking-tighter sm:text-6xl">Home</h1>
      {user && <p className="leading-relaxed text-muted-foreground">Signed in as {user.email}.</p>}
    </section>
  );
}
