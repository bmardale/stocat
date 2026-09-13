import { createFileRoute } from "@tanstack/react-router";

export const Route = createFileRoute("/")({ component: Home });

function Home() {
  return (
    <section className="flex flex-col items-start gap-4">
      <p className="text-sm font-semibold text-primary">Welcome to Stocat</p>
      <h1 className="max-w-[14ch] font-heading text-4xl leading-[1.05] font-bold tracking-tighter sm:text-6xl lg:text-7xl">
        A little home for your files.
      </h1>
      <p className="leading-relaxed text-muted-foreground">Your space starts here.</p>
    </section>
  );
}
