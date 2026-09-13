import {
  Cancel01Icon,
  CheckmarkCircle02Icon,
  File01Icon,
  Loading03Icon,
  Upload01Icon,
} from "@hugeicons/core-free-icons";
import { HugeiconsIcon } from "@hugeicons/react";
import { useQueryClient } from "@tanstack/react-query";
import type { DragEvent, RefObject } from "react";
import { useCallback, useEffect, useRef, useState } from "react";
import type { Library } from "@/api/generated/model";
import { getNodesListQueryKey } from "@/api/generated/libraries/libraries";
import {
  cancelUpload,
  uploadEncryptedFile,
  uploadPlainFile,
  type UploadPhase,
} from "@/api/uploads";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { toast } from "@/components/ui/toast";
import type { LibraryKeys } from "@/lib/library-crypto";
import { formatBytes } from "@/lib/utils";
import { cn } from "cn";

type UploadState = "queued" | UploadPhase | "completed" | "failed" | "cancelled";

type UploadItem = {
  id: number;
  file: File;
  parentId: string;
  state: UploadState;
  uploaded: number;
  total: number;
  error?: string;
};

const concurrentUploads = 2;

export function useFileUploads({
  library,
  parentId,
  keys,
}: {
  library: Library;
  parentId: string;
  keys?: LibraryKeys;
}) {
  const queryClient = useQueryClient();
  const inputRef = useRef<HTMLInputElement>(null);
  const nextId = useRef(1);
  const controllers = useRef(new Map<number, AbortController>());
  const sessions = useRef(new Map<number, string>());
  const [items, setItems] = useState<UploadItem[]>([]);
  const [dragging, setDragging] = useState(false);

  const update = useCallback((id: number, values: Partial<UploadItem>) => {
    setItems((current) => current.map((item) => (item.id === id ? { ...item, ...values } : item)));
  }, []);

  const start = useCallback(
    async (item: UploadItem) => {
      const controller = new AbortController();
      controllers.current.set(item.id, controller);
      update(item.id, { state: "uploading" });
      const options = {
        signal: controller.signal,
        onSession: (session: { id: string }) => sessions.current.set(item.id, session.id),
        onPhase: (state: UploadPhase) => update(item.id, { state }),
        onProgress: (uploaded: number, total: number) => update(item.id, { uploaded, total }),
      };
      try {
        if (library.encryption_mode === "e2ee") {
          if (!keys) throw new Error("Unlock the library before you upload files.");
          await uploadEncryptedFile(
            item.file,
            { libraryId: library.id, parentId: item.parentId },
            keys,
            options,
          );
        } else {
          await uploadPlainFile(
            item.file,
            { libraryId: library.id, parentId: item.parentId },
            options,
          );
        }
        update(item.id, { state: "completed" });
        void queryClient.invalidateQueries({ queryKey: getNodesListQueryKey(library.id) });
      } catch (error) {
        if (controller.signal.aborted) {
          update(item.id, { state: "cancelled" });
        } else {
          const message = error instanceof Error ? error.message : "The upload failed.";
          update(item.id, { state: "failed", error: message });
          toast.add({ type: "error", title: item.file.name, description: message });
        }
      } finally {
        controllers.current.delete(item.id);
      }
    },
    [keys, library.encryption_mode, library.id, queryClient, update],
  );

  useEffect(() => {
    const active = items.filter(
      (item) => item.state === "uploading" || item.state === "publishing",
    ).length;
    const queued = items.filter((item) => item.state === "queued");
    for (const item of queued.slice(0, Math.max(0, concurrentUploads - active))) {
      void start(item);
    }
  }, [items, start]);

  useEffect(
    () => () => {
      for (const controller of controllers.current.values()) controller.abort();
    },
    [],
  );

  const add = useCallback(
    (files: FileList | File[]) => {
      const added = Array.from(files).map((file): UploadItem => ({
        id: nextId.current++,
        file,
        parentId,
        state: "queued",
        uploaded: 0,
        total: file.size,
      }));
      if (added.length > 0) setItems((current) => [...current, ...added]);
      if (inputRef.current) inputRef.current.value = "";
    },
    [parentId],
  );

  const cancel = useCallback(
    (id: number) => {
      controllers.current.get(id)?.abort();
      const session = sessions.current.get(id);
      if (session) void cancelUpload(session).catch(() => undefined);
      update(id, { state: "cancelled" });
    },
    [update],
  );

  const removeFinished = useCallback(() => {
    setItems((current) =>
      current.filter((item) => !["completed", "failed", "cancelled"].includes(item.state)),
    );
  }, []);

  const onDrop = useCallback(
    (event: DragEvent<HTMLDivElement>) => {
      event.preventDefault();
      setDragging(false);
      add(event.dataTransfer.files);
    },
    [add],
  );

  return {
    items,
    inputRef,
    dragging,
    add,
    cancel,
    removeFinished,
    choose: () => inputRef.current?.click(),
    dropProps: {
      onDragEnter: (event: DragEvent<HTMLDivElement>) => {
        event.preventDefault();
        setDragging(true);
      },
      onDragOver: (event: DragEvent<HTMLDivElement>) => event.preventDefault(),
      onDragLeave: (event: DragEvent<HTMLDivElement>) => {
        if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setDragging(false);
      },
      onDrop,
    },
  };
}

export function UploadInput({
  inputRef,
  onFiles,
}: {
  inputRef: RefObject<HTMLInputElement | null>;
  onFiles: (files: FileList) => void;
}) {
  return (
    <input
      ref={inputRef}
      type="file"
      multiple
      className="sr-only"
      aria-label="Choose files to upload"
      onChange={(event) => event.target.files && onFiles(event.target.files)}
    />
  );
}

export function UploadDropOverlay({ visible }: { visible: boolean }) {
  if (!visible) return null;
  return (
    <div className="pointer-events-none absolute inset-0 z-10 grid place-items-center rounded-xl border-2 border-dashed border-primary bg-background/90">
      <div className="flex flex-col items-center gap-2 text-center">
        <HugeiconsIcon icon={Upload01Icon} strokeWidth={2} className="size-7 text-primary" />
        <p className="font-medium">Drop files to upload</p>
      </div>
    </div>
  );
}

export function UploadQueue({
  items,
  onCancel,
  onClear,
}: {
  items: UploadItem[];
  onCancel: (id: number) => void;
  onClear: () => void;
}) {
  if (items.length === 0) return null;
  const hasFinished = items.some((item) =>
    ["completed", "failed", "cancelled"].includes(item.state),
  );
  return (
    <Card className="gap-0 overflow-hidden py-0" aria-label="Uploads">
      <div className="flex items-center justify-between border-b px-4 py-3">
        <p className="text-sm font-medium">Uploads</p>
        {hasFinished && (
          <Button variant="ghost" size="sm" onClick={onClear}>
            Clear finished
          </Button>
        )}
      </div>
      <ul className="divide-y">
        {items.map((item) => (
          <UploadRow key={item.id} item={item} onCancel={() => onCancel(item.id)} />
        ))}
      </ul>
    </Card>
  );
}

function UploadRow({ item, onCancel }: { item: UploadItem; onCancel: () => void }) {
  const active = item.state === "uploading" || item.state === "publishing";
  const progress = item.total === 0 ? (active ? 100 : 0) : (item.uploaded / item.total) * 100;
  const boundedProgress = Math.max(0, Math.min(100, progress));
  return (
    <li className="relative flex items-center gap-3 px-4 py-3">
      <span className="grid size-8 shrink-0 place-items-center rounded-lg bg-muted text-muted-foreground">
        {item.state === "completed" ? (
          <HugeiconsIcon icon={CheckmarkCircle02Icon} strokeWidth={2} className="text-primary" />
        ) : active ? (
          <HugeiconsIcon icon={Loading03Icon} strokeWidth={2} className="animate-spin" />
        ) : (
          <HugeiconsIcon icon={File01Icon} strokeWidth={2} />
        )}
      </span>
      <div className="min-w-0 flex-1">
        <div className="flex items-baseline justify-between gap-3">
          <p className="truncate text-sm font-medium">{item.file.name}</p>
          <p className="shrink-0 text-xs text-muted-foreground">{statusText(item)}</p>
        </div>
        <div
          className="mt-1.5 h-1 overflow-hidden rounded-full bg-muted"
          role="progressbar"
          aria-label={`Upload progress for ${item.file.name}`}
          aria-valuemin={0}
          aria-valuemax={100}
          aria-valuenow={Math.round(boundedProgress)}
        >
          <div
            className={cn(
              "h-full origin-left bg-primary transition-transform duration-150 ease-[cubic-bezier(0.23,1,0.32,1)]",
              item.state === "failed" && "bg-destructive",
              item.state === "cancelled" && "bg-muted-foreground/40",
            )}
            style={{ transform: `scaleX(${boundedProgress / 100})` }}
          />
        </div>
        {item.error && <p className="mt-1 text-xs text-destructive">{item.error}</p>}
      </div>
      {(item.state === "queued" || active) && (
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label={`Cancel ${item.file.name}`}
          onClick={onCancel}
        >
          <HugeiconsIcon icon={Cancel01Icon} strokeWidth={2} />
        </Button>
      )}
    </li>
  );
}

function statusText(item: UploadItem) {
  if (item.state === "queued") return "Waiting";
  if (item.state === "publishing") return "Publishing";
  if (item.state === "completed") return "Complete";
  if (item.state === "failed") return "Failed";
  if (item.state === "cancelled") return "Cancelled";
  return `${formatBytes(item.uploaded)} of ${formatBytes(item.total)}`;
}
