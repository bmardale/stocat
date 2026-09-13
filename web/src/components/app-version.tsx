import { useQuery } from "@tanstack/react-query";
import { cn } from "cn";
import { versionQueryOptions } from "@/api/version";

export function AppVersion({ className }: { className?: string }) {
  const { data } = useQuery(versionQueryOptions);
  if (!data) {
    return null;
  }
  return <p className={cn("text-xs text-muted-foreground", className)}>Version {data.version}</p>;
}
