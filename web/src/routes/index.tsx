import { createFileRoute } from "@tanstack/react-router";

export const Route = createFileRoute("/")({ component: Home });

function Home() {
  return (
    <section>
      <p className="eyebrow">Welcome to Stocat</p>
      <h1>A little home for your files.</h1>
      <p>Your space starts here.</p>
    </section>
  );
}
