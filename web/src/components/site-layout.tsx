import { Link } from "@tanstack/react-router";
import { useAuth } from "@/components/auth-provider";
import { ModeToggle } from "@/components/mode-toggle";
import { Button, buttonVariants } from "@/components/ui/button";

export function SiteLayout({ children }: { children: React.ReactNode }) {
  const { user, logout } = useAuth();

  return (
    <>
      <header className="flex items-center justify-between border-b px-6 py-6 sm:px-12 lg:px-24">
        <Link
          to="/"
          className="rounded-sm font-heading text-2xl font-bold tracking-tighter focus-visible:outline-2 focus-visible:outline-offset-4"
        >
          stocat
        </Link>
        <nav className="flex items-center gap-2">
          {user ? (
            <Button variant="outline" onClick={() => void logout()}>
              Sign out
            </Button>
          ) : (
            <>
              <Link to="/login" className={buttonVariants({ variant: "ghost" })}>
                Sign in
              </Link>
              <Link to="/register" className={buttonVariants()}>
                Create account
              </Link>
            </>
          )}
          <ModeToggle />
        </nav>
      </header>
      <main id="main" className="mx-auto max-w-5xl px-6 py-16 sm:py-24 lg:py-40">
        {children}
      </main>
    </>
  );
}
