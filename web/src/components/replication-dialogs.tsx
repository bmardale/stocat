import {
  ArrowDataTransferHorizontalIcon,
  Delete02Icon,
  Refresh01Icon,
} from "@hugeicons/core-free-icons";
import { HugeiconsIcon } from "@hugeicons/react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useEffect, useId, useMemo, useState } from "react";
import {
  getReplicationsListQueryKey,
  replicationsCreate,
  replicationsDelete,
  replicationsSync,
} from "@/api/generated/replications/replications";
import type { Library, Replication } from "@/api/generated/model";
import { BackendIcon } from "@/components/library-dialogs";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Field, FieldContent, FieldError, FieldLabel, FieldTitle } from "@/components/ui/field";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { toast } from "@/components/ui/toast";

export function CreateReplicationDialog({
  open,
  libraries,
  replications,
  onOpenChange,
}: {
  open: boolean;
  libraries: Library[];
  replications: Replication[];
  onOpenChange: (open: boolean) => void;
}) {
  const queryClient = useQueryClient();
  const formId = useId();
  const unavailable = useMemo(
    () => new Set(replications.flatMap((item) => [item.source.id, item.destination.id])),
    [replications],
  );
  const choices = libraries.filter((library) => !unavailable.has(library.id));
  const [sourceID, setSourceID] = useState("");
  const [destinationID, setDestinationID] = useState("");
  const source = choices.find((library) => library.id === sourceID);
  const destinations = choices.filter(
    (library) => library.id !== sourceID && library.encryption_mode === source?.encryption_mode,
  );
  useEffect(() => {
    if (!open) {
      setSourceID("");
      setDestinationID("");
    }
  }, [open]);
  const create = useMutation({
    mutationFn: () =>
      replicationsCreate({
        source_library_id: sourceID,
        destination_library_id: destinationID,
      }),
    onSuccess: async () => {
      toast.add({ type: "success", description: "Replication started." });
      onOpenChange(false);
      await queryClient.invalidateQueries({ queryKey: getReplicationsListQueryKey() });
    },
  });

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[calc(100svh-2rem)] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>Set up replication</DialogTitle>
          <DialogDescription>
            Copy the current source library into an empty destination. The destination becomes
            read-only.
          </DialogDescription>
        </DialogHeader>
        <div className="grid gap-5 sm:grid-cols-[1fr_auto_1fr] sm:items-start">
          <LibraryChoice
            id={`${formId}-source`}
            title="Source"
            value={sourceID}
            libraries={choices}
            onChange={(value) => {
              setSourceID(value);
              setDestinationID("");
            }}
          />
          <div className="hidden pt-10 text-muted-foreground sm:block">
            <HugeiconsIcon
              icon={ArrowDataTransferHorizontalIcon}
              strokeWidth={1.8}
              className="size-5"
            />
          </div>
          <LibraryChoice
            id={`${formId}-destination`}
            title="Destination"
            value={destinationID}
            libraries={destinations}
            disabled={!source}
            emptyText={source ? "No compatible library" : "Choose a source first"}
            onChange={setDestinationID}
          />
        </div>
        <p className="text-sm leading-relaxed text-muted-foreground">
          Replication runs after setup and checks for changes every minute. Stop replication to edit
          the destination again.
        </p>
        {create.error && <FieldError>{create.error.message}</FieldError>}
        <DialogFooter>
          <DialogClose render={<Button variant="outline" />}>Cancel</DialogClose>
          <Button
            disabled={!sourceID || !destinationID || create.isPending}
            onClick={() => create.mutate()}
          >
            {create.isPending ? "Starting…" : "Start replication"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function LibraryChoice({
  id,
  title,
  value,
  libraries,
  disabled,
  emptyText = "No available libraries",
  onChange,
}: {
  id: string;
  title: string;
  value: string;
  libraries: Library[];
  disabled?: boolean;
  emptyText?: string;
  onChange: (value: string) => void;
}) {
  return (
    <div className="min-w-0 space-y-2">
      <p className="text-sm font-medium">{title}</p>
      {libraries.length === 0 ? (
        <div className="flex min-h-20 items-center justify-center rounded-lg border border-dashed px-3 text-center text-sm text-muted-foreground">
          {emptyText}
        </div>
      ) : (
        <RadioGroup value={value} onValueChange={onChange} disabled={disabled}>
          {libraries.map((library) => (
            <FieldLabel key={library.id} htmlFor={`${id}-${library.id}`}>
              <Field orientation="horizontal">
                <BackendIcon type={library.backend.type} className="size-4 text-muted-foreground" />
                <FieldContent className="min-w-0">
                  <FieldTitle className="truncate">{library.name}</FieldTitle>
                  <span className="truncate text-xs text-muted-foreground">
                    {library.backend.name}
                  </span>
                </FieldContent>
                <RadioGroupItem id={`${id}-${library.id}`} value={library.id} />
              </Field>
            </FieldLabel>
          ))}
        </RadioGroup>
      )}
    </div>
  );
}

export function ReplicationActions({ replication }: { replication: Replication }) {
  const queryClient = useQueryClient();
  const refresh = () => queryClient.invalidateQueries({ queryKey: getReplicationsListQueryKey() });
  const sync = useMutation({
    mutationFn: () => replicationsSync(replication.id),
    onSuccess: async () => {
      toast.add({ type: "success", description: "Sync queued." });
      await refresh();
    },
    onError: (error) => toast.add({ type: "error", description: error.message }),
  });
  const remove = useMutation({
    mutationFn: () => replicationsDelete(replication.id),
    onSuccess: async () => {
      toast.add({
        type: "success",
        description: "Replication stopped. The destination is editable.",
      });
      await refresh();
    },
    onError: (error) => toast.add({ type: "error", description: error.message }),
  });
  return (
    <div className="flex justify-end gap-1">
      <Button variant="ghost" size="sm" disabled={sync.isPending} onClick={() => sync.mutate()}>
        <HugeiconsIcon icon={Refresh01Icon} strokeWidth={2} />
        Sync now
      </Button>
      <Button
        variant="ghost"
        size="icon-sm"
        aria-label={`Stop replication from ${replication.source.name}`}
        disabled={remove.isPending}
        onClick={() => remove.mutate()}
      >
        <HugeiconsIcon icon={Delete02Icon} strokeWidth={2} />
      </Button>
    </div>
  );
}
