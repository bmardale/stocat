import { z } from "zod";
import type { Quota, QuotaMode } from "@/api/generated/model";
import { formatBytes } from "@/lib/utils";

const bytesPerGiB = 1024 ** 3;

export type QuotaValue = { mode: QuotaMode; gib: string };

export const quotaValueSchema = z
  .object({ mode: z.enum(["inherit", "unlimited", "limited"]), gib: z.string() })
  .superRefine((value, context) => {
    if (value.mode === "limited" && !/^\d+(\.\d{1,3})?$/.test(value.gib.trim())) {
      context.addIssue({
        code: "custom",
        message: "Enter a size in GiB with at most three decimals.",
      });
    }
  });

export function quotaValue(quota: Quota): QuotaValue {
  const gib =
    quota.limit_bytes === undefined
      ? ""
      : String(Number((quota.limit_bytes / bytesPerGiB).toFixed(3)));
  return { mode: quota.mode, gib };
}

export function quotaFromValue(value: QuotaValue): Quota {
  if (value.mode !== "limited") {
    return { mode: value.mode };
  }
  return { mode: "limited", limit_bytes: Math.round(Number(value.gib.trim()) * bytesPerGiB) };
}

export function quotaLabel(quota: Quota, inheritedLabel: string) {
  switch (quota.mode) {
    case "inherit":
      return inheritedLabel;
    case "unlimited":
      return "No limit";
    default:
      return formatBytes(quota.limit_bytes ?? 0);
  }
}
