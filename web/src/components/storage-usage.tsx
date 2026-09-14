import { ArrowRight01Icon } from "@hugeicons/core-free-icons";
import { HugeiconsIcon } from "@hugeicons/react";
import { queryOptions, useQuery } from "@tanstack/react-query";
import type { Usage } from "@/api/generated/model";
import { getStorageUsageListQueryKey, storageUsageList } from "@/api/generated/quota/quota";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { SidebarGroup, SidebarGroupLabel } from "@/components/ui/sidebar";
import { formatBytes } from "@/lib/utils";
import { cn } from "cn";

export const storageUsageQueryOptions = queryOptions({
  queryKey: getStorageUsageListQueryKey(),
  queryFn: ({ signal }) => storageUsageList({ signal }),
});

export function StorageUsage() {
  const { data: usage } = useQuery(storageUsageQueryOptions);
  if (!usage || usage.length === 0) {
    return null;
  }

  return (
    <SidebarGroup className="mt-auto group-data-[collapsible=icon]:hidden">
      <Collapsible defaultOpen>
        <SidebarGroupLabel
          render={<CollapsibleTrigger />}
          className="group/storage-trigger w-full hover:bg-sidebar-accent hover:text-sidebar-accent-foreground"
        >
          Storage
          <HugeiconsIcon
            icon={ArrowRight01Icon}
            strokeWidth={2}
            className="ml-auto transition-transform group-data-[panel-open]/storage-trigger:rotate-90"
          />
        </SidebarGroupLabel>
        <CollapsibleContent>
          <ul className="flex flex-col gap-3 px-2 pt-1">
            {usage.map((item) => (
              <UsageMeter key={item.backend.id} usage={item} />
            ))}
          </ul>
        </CollapsibleContent>
      </Collapsible>
    </SidebarGroup>
  );
}

function UsageMeter({ usage }: { usage: Usage }) {
  const limit = usage.limit_bytes;
  const used = formatBytes(usage.used_bytes);
  const summary = limit === null ? `${used} used` : `${used} of ${formatBytes(limit)}`;
  const ratio = limit === null ? 0 : limit === 0 ? 1 : Math.min(usage.used_bytes / limit, 1);

  return (
    <li className="flex flex-col gap-1.5 text-xs">
      <span className="truncate font-medium">{usage.backend.name}</span>
      {limit !== null && (
        <div
          role="meter"
          aria-label={`${usage.backend.name} storage`}
          aria-valuemin={0}
          aria-valuemax={limit}
          aria-valuenow={Math.min(usage.used_bytes, limit)}
          aria-valuetext={summary}
          className="h-1.5 overflow-hidden rounded-full bg-sidebar-accent"
        >
          <div
            className={cn(
              "h-full rounded-full bg-primary transition-[width]",
              ratio >= 0.9 && "bg-destructive",
            )}
            style={{ width: `${ratio * 100}%` }}
          />
        </div>
      )}
      <span className="text-muted-foreground tabular-nums">{summary}</span>
    </li>
  );
}
