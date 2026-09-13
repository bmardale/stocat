import {
  Alert02Icon,
  Download04Icon,
  FileNotFoundIcon,
  Loading03Icon,
} from "@hugeicons/core-free-icons";
import { HugeiconsIcon } from "@hugeicons/react";
import { useCallback, useEffect, useState } from "react";
import { downloadFile, loadPreview, previewUnavailable, type Preview } from "@/api/files";
import { filesGet } from "@/api/generated/files/files";
import type { FileDetails } from "@/api/generated/model";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty";
import { toast } from "@/components/ui/toast";
import type { LibraryKeys } from "@/lib/library-crypto";
import { formatBytes } from "@/lib/utils";

export type FileTarget = { id: string; name: string; parentId?: string };

// The dialog keeps the file while it closes, so the content does not change during the animation.
export type PreviewState = { open: boolean; file?: FileTarget };

type LoadState =
  | { status: "loading" }
  | { status: "ready"; details: FileDetails; preview: Preview }
  | { status: "unavailable"; details: FileDetails; reason: string }
  | { status: "failed"; message: string };

export function useFileDownload(keys?: LibraryKeys) {
  const [pending, setPending] = useState<ReadonlySet<string>>(() => new Set());

  const download = useCallback(
    async (file: FileTarget) => {
      setPending((current) => new Set(current).add(file.id));
      try {
        await downloadFile(await filesGet(file.id), file.name, keys);
      } catch (error) {
        toast.add({
          type: "error",
          title: file.name,
          description: error instanceof Error ? error.message : "The download failed.",
        });
      } finally {
        setPending((current) => {
          const next = new Set(current);
          next.delete(file.id);
          return next;
        });
      }
    },
    [keys],
  );

  return { download, downloading: (id: string) => pending.has(id) };
}

export function FilePreviewDialog({
  state,
  onOpenChange,
  keys,
  downloading,
  onDownload,
}: {
  state: PreviewState;
  onOpenChange: (open: boolean) => void;
  keys?: LibraryKeys;
  downloading: (id: string) => boolean;
  onDownload: (file: FileTarget) => void;
}) {
  const file = state.file;
  return (
    <Dialog open={state.open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-4xl">
        {file && (
          <PreviewBody
            key={file.id}
            file={file}
            keys={keys}
            downloading={downloading(file.id)}
            onDownload={() => onDownload(file)}
          />
        )}
      </DialogContent>
    </Dialog>
  );
}

function PreviewBody({
  file,
  keys,
  downloading,
  onDownload,
}: {
  file: FileTarget;
  keys?: LibraryKeys;
  downloading: boolean;
  onDownload: () => void;
}) {
  const [state, setState] = useState<LoadState>({ status: "loading" });

  useEffect(() => {
    const controller = new AbortController();
    let objectURL: string | undefined;
    void (async () => {
      try {
        const details = await filesGet(file.id, { signal: controller.signal });
        const reason = previewUnavailable(details, file.name);
        if (reason) {
          if (!controller.signal.aborted) setState({ status: "unavailable", details, reason });
          return;
        }
        const preview = await loadPreview(details, file.name, keys, controller.signal);
        const created = preview.kind !== "text" && preview.revoke ? preview.url : undefined;
        if (controller.signal.aborted) {
          if (created) URL.revokeObjectURL(created);
          return;
        }
        objectURL = created;
        setState({ status: "ready", details, preview });
      } catch (error) {
        if (controller.signal.aborted) return;
        setState({
          status: "failed",
          message: error instanceof Error ? error.message : "The preview did not load.",
        });
      }
    })();
    return () => {
      controller.abort();
      if (objectURL) URL.revokeObjectURL(objectURL);
    };
  }, [file.id, file.name, keys]);

  const details = state.status === "ready" || state.status === "unavailable" ? state.details : null;

  return (
    <>
      <DialogHeader className="min-w-0 pr-8">
        <DialogTitle className="truncate leading-normal">{file.name}</DialogTitle>
        <DialogDescription>
          {details
            ? [formatBytes(details.size), details.encryption_format && "End-to-end encrypted"]
                .filter(Boolean)
                .join(" · ")
            : " "}
        </DialogDescription>
      </DialogHeader>
      <div
        className="grid min-h-72 place-items-center overflow-hidden rounded-lg bg-muted/50"
        aria-busy={state.status === "loading"}
      >
        <PreviewContent file={file} state={state} />
      </div>
      <DialogFooter>
        <Button onClick={onDownload} disabled={downloading}>
          <HugeiconsIcon
            icon={downloading ? Loading03Icon : Download04Icon}
            strokeWidth={2}
            data-icon="inline-start"
            className={downloading ? "animate-spin" : undefined}
          />
          {downloading ? "Downloading…" : "Download"}
        </Button>
      </DialogFooter>
    </>
  );
}

function PreviewContent({ file, state }: { file: FileTarget; state: LoadState }) {
  if (state.status === "loading") {
    return (
      <div role="status">
        <HugeiconsIcon
          icon={Loading03Icon}
          strokeWidth={2}
          className="size-6 animate-spin text-muted-foreground"
        />
        <span className="sr-only">Loading preview</span>
      </div>
    );
  }
  if (state.status === "failed" || state.status === "unavailable") {
    const failed = state.status === "failed";
    return (
      <Empty className="border-0">
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <HugeiconsIcon icon={failed ? Alert02Icon : FileNotFoundIcon} strokeWidth={2} />
          </EmptyMedia>
          <EmptyTitle>{failed ? "The preview did not load" : "No preview"}</EmptyTitle>
          <EmptyDescription>{failed ? state.message : state.reason}</EmptyDescription>
        </EmptyHeader>
      </Empty>
    );
  }
  const { preview } = state;
  switch (preview.kind) {
    case "text":
      return (
        <pre className="max-h-[70vh] self-stretch justify-self-stretch overflow-auto p-4 font-mono text-xs leading-relaxed break-words whitespace-pre-wrap">
          {preview.text}
        </pre>
      );
    case "image":
      return (
        <img src={preview.url} alt={file.name} className="max-h-[70vh] max-w-full object-contain" />
      );
    case "video":
      return (
        <video src={preview.url} controls className="max-h-[70vh] w-full bg-black">
          <track kind="captions" />
        </video>
      );
    case "audio":
      return (
        <audio src={preview.url} controls aria-label={file.name} className="w-full max-w-md" />
      );
    case "pdf":
      return <iframe src={preview.url} title={file.name} className="h-[70vh] w-full bg-white" />;
  }
}
