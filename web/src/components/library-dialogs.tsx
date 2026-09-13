import {
  AlertCircleIcon,
  CloudServerIcon,
  HardDriveIcon,
  SquareLock02Icon,
} from "@hugeicons/core-free-icons";
import { HugeiconsIcon } from "@hugeicons/react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useId } from "react";
import { z } from "zod";
import {
  foldersCreate,
  getLibrariesListQueryKey,
  getNodesListQueryKey,
  librariesCreate,
} from "@/api/generated/libraries/libraries";
import type {
  CreateLibraryInputBody,
  FolderInputBody,
  Library,
  LibraryBackend,
} from "@/api/generated/model";
import { useAuth } from "@/components/auth-provider";
import { useAppForm } from "@/components/form";
import { useLibraryKeys } from "@/components/library-keys";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
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
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
  FieldLegend,
  FieldSet,
  FieldTitle,
} from "@/components/ui/field";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { toast } from "@/components/ui/toast";
import {
  createKeyEnvelope,
  encryptName,
  type LibraryKeys,
  nameToken,
  openKeyEnvelope,
} from "@/lib/library-crypto";

type EncryptionMode = Library["encryption_mode"];

const encryptionModes: { value: EncryptionMode; label: string; description: string }[] = [
  {
    value: "none",
    label: "Not encrypted",
    description: "The server can read file and folder names.",
  },
  {
    value: "e2ee",
    label: "End-to-end encrypted",
    description: "This browser encrypts names with your passphrase.",
  },
];

export function BackendIcon({
  type,
  className,
}: {
  type: LibraryBackend["type"];
  className?: string;
}) {
  return (
    <HugeiconsIcon
      icon={type === "s3" ? CloudServerIcon : HardDriveIcon}
      strokeWidth={2}
      className={className}
    />
  );
}

const librarySchema = z
  .object({
    name: z
      .string()
      .trim()
      .min(1, "Enter a library name.")
      .max(100, "Use 100 characters or fewer."),
    backendId: z.string().min(1, "Choose a storage backend."),
    encryption: z.enum(["none", "e2ee"]),
    passphrase: z.string(),
    confirmPassphrase: z.string(),
  })
  .superRefine((values, ctx) => {
    if (values.encryption !== "e2ee") {
      return;
    }
    if (values.passphrase.length < 12) {
      ctx.addIssue({
        code: "custom",
        path: ["passphrase"],
        message: "Use a passphrase with 12 or more characters.",
      });
    }
    if (values.confirmPassphrase !== values.passphrase) {
      ctx.addIssue({
        code: "custom",
        path: ["confirmPassphrase"],
        message: "The passphrases do not match.",
      });
    }
  });

type LibraryFormValues = z.input<typeof librarySchema>;

export function CreateLibraryDialog({
  open,
  backends,
  onOpenChange,
}: {
  open: boolean;
  backends: LibraryBackend[];
  onOpenChange: (open: boolean) => void;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[calc(100svh-2rem)] overflow-y-auto sm:max-w-lg">
        <CreateLibraryForm backends={backends} onCreated={() => onOpenChange(false)} />
      </DialogContent>
    </Dialog>
  );
}

function CreateLibraryForm({
  backends,
  onCreated,
}: {
  backends: LibraryBackend[];
  onCreated: () => void;
}) {
  const queryClient = useQueryClient();
  const { user } = useAuth();
  const { unlock } = useLibraryKeys();
  const formId = useId();
  const create = useMutation({
    mutationFn: async (values: LibraryFormValues) => {
      const data: CreateLibraryInputBody = {
        name: values.name.trim(),
        backend_id: values.backendId,
        encryption_mode: values.encryption,
      };
      let keys: LibraryKeys | undefined;
      if (values.encryption === "e2ee") {
        const created = await createKeyEnvelope(values.passphrase);
        data.key_envelope = created.envelope;
        keys = created.keys;
      }
      return { library: await librariesCreate(data), keys };
    },
    // Keep the mutation pending until the list shows the result.
    onSuccess: async ({ library, keys }) => {
      if (keys) {
        unlock(library.id, keys);
      }
      toast.add({ type: "success", description: "Library created." });
      await queryClient.invalidateQueries({ queryKey: getLibrariesListQueryKey() });
    },
  });
  const defaultValues: LibraryFormValues = {
    name: "",
    backendId: backends[0]?.id ?? "",
    encryption: "none",
    passphrase: "",
    confirmPassphrase: "",
  };
  const form = useAppForm({
    defaultValues,
    validators: { onSubmit: librarySchema },
    onSubmit: ({ value }) => create.mutate(value, { onSuccess: onCreated }),
  });

  return (
    <>
      <DialogHeader>
        <DialogTitle>New library</DialogTitle>
        <DialogDescription>
          You cannot change the storage backend or the encryption after you create the library.
        </DialogDescription>
      </DialogHeader>
      {backends.length === 0 ? (
        <Alert>
          <HugeiconsIcon icon={AlertCircleIcon} strokeWidth={2} />
          <AlertTitle>No storage backends are available</AlertTitle>
          <AlertDescription>
            {user?.is_admin ? (
              <>
                Add or enable a backend on the <Link to="/admin/storage">Storage</Link> page.
              </>
            ) : (
              "Ask an administrator to add a storage backend."
            )}
          </AlertDescription>
        </Alert>
      ) : (
        <form
          id={formId}
          noValidate
          onSubmit={(event) => {
            event.preventDefault();
            void form.handleSubmit();
          }}
        >
          <FieldGroup>
            <form.AppField name="name">
              {(field) => <field.TextField label="Name" autoComplete="off" />}
            </form.AppField>
            <form.Field name="backendId">
              {(field) => (
                <FieldSet>
                  <FieldLegend variant="label">Storage backend</FieldLegend>
                  <RadioGroup
                    value={field.state.value}
                    onValueChange={(value) => field.handleChange(value as string)}
                  >
                    {backends.map((backend) => (
                      <FieldLabel key={backend.id} htmlFor={`${formId}-${backend.id}`}>
                        <Field orientation="horizontal">
                          <BackendIcon
                            type={backend.type}
                            className="size-4 text-muted-foreground"
                          />
                          <FieldContent>
                            <FieldTitle>{backend.name}</FieldTitle>
                          </FieldContent>
                          <RadioGroupItem id={`${formId}-${backend.id}`} value={backend.id} />
                        </Field>
                      </FieldLabel>
                    ))}
                  </RadioGroup>
                  <FieldError errors={field.state.meta.errors} />
                </FieldSet>
              )}
            </form.Field>
            <form.Field name="encryption">
              {(field) => (
                <FieldSet>
                  <FieldLegend variant="label">Encryption</FieldLegend>
                  <RadioGroup
                    value={field.state.value}
                    onValueChange={(value) => field.handleChange(value as EncryptionMode)}
                    className="sm:grid-cols-2"
                  >
                    {encryptionModes.map((option) => (
                      <FieldLabel key={option.value} htmlFor={`${formId}-${option.value}`}>
                        <Field orientation="horizontal">
                          <FieldContent>
                            <FieldTitle>{option.label}</FieldTitle>
                            <FieldDescription>{option.description}</FieldDescription>
                          </FieldContent>
                          <RadioGroupItem id={`${formId}-${option.value}`} value={option.value} />
                        </Field>
                      </FieldLabel>
                    ))}
                  </RadioGroup>
                </FieldSet>
              )}
            </form.Field>
            <form.Subscribe selector={(state) => state.values.encryption}>
              {(encryption) =>
                encryption === "e2ee" && (
                  <>
                    <form.AppField name="passphrase">
                      {(field) => (
                        <field.TextField
                          label="Passphrase"
                          type="password"
                          autoComplete="new-password"
                        />
                      )}
                    </form.AppField>
                    <form.AppField name="confirmPassphrase">
                      {(field) => (
                        <field.TextField
                          label="Confirm passphrase"
                          type="password"
                          autoComplete="new-password"
                        />
                      )}
                    </form.AppField>
                    <Alert>
                      <HugeiconsIcon icon={SquareLock02Icon} strokeWidth={2} />
                      <AlertTitle>Keep the passphrase safe</AlertTitle>
                      <AlertDescription>
                        The server never receives the passphrase and cannot recover it. Without the
                        passphrase, nobody can read the names in this library.
                      </AlertDescription>
                    </Alert>
                  </>
                )
              }
            </form.Subscribe>
            {create.error && <FieldError>{create.error.message}</FieldError>}
          </FieldGroup>
        </form>
      )}
      <DialogFooter>
        <DialogClose render={<Button variant="outline" />}>Cancel</DialogClose>
        <Button type="submit" form={formId} disabled={backends.length === 0 || create.isPending}>
          Create library
        </Button>
      </DialogFooter>
    </>
  );
}

const folderSchema = z.object({
  name: z
    .string()
    .trim()
    .min(1, "Enter a folder name.")
    .max(255, "Use 255 characters or fewer.")
    .refine(
      (name) => name !== "." && name !== ".." && !/[/\\\0]/.test(name),
      "Do not use slashes, NUL, dot, or dot-dot.",
    ),
});

type FolderTarget = { library: Library; parentId?: string; keys?: LibraryKeys };

export function CreateFolderDialog({
  open,
  onOpenChange,
  ...target
}: FolderTarget & { open: boolean; onOpenChange: (open: boolean) => void }) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <CreateFolderForm {...target} onCreated={() => onOpenChange(false)} />
      </DialogContent>
    </Dialog>
  );
}

function CreateFolderForm({
  library,
  parentId,
  keys,
  onCreated,
}: FolderTarget & { onCreated: () => void }) {
  const queryClient = useQueryClient();
  const formId = useId();
  const create = useMutation({
    mutationFn: async (name: string) => {
      const data: FolderInputBody = { parent_id: parentId };
      if (library.encryption_mode === "none") {
        data.name = name;
        return foldersCreate(library.id, data);
      }
      if (!keys) {
        throw new Error("Unlock the library first.");
      }
      const normalized = name.normalize("NFC");
      // The server resolves an empty parent to the root folder, so the token must use the root ID.
      [data.encrypted_name, data.name_token] = await Promise.all([
        encryptName(keys, normalized),
        nameToken(keys, parentId ?? library.root_node_id, normalized),
      ]);
      return foldersCreate(library.id, data);
    },
    // Keep the mutation pending until the list shows the result.
    onSuccess: () => {
      toast.add({ type: "success", description: "Folder created." });
      return queryClient.invalidateQueries({ queryKey: getNodesListQueryKey(library.id) });
    },
  });
  const form = useAppForm({
    defaultValues: { name: "" },
    validators: { onSubmit: folderSchema },
    onSubmit: ({ value }) => create.mutate(value.name.trim(), { onSuccess: onCreated }),
  });

  return (
    <>
      <DialogHeader>
        <DialogTitle>New folder</DialogTitle>
        <DialogDescription>
          {library.encryption_mode === "e2ee"
            ? "This browser encrypts the name before it sends it to the server."
            : `Create a folder in ${library.name}.`}
        </DialogDescription>
      </DialogHeader>
      <form
        id={formId}
        noValidate
        onSubmit={(event) => {
          event.preventDefault();
          void form.handleSubmit();
        }}
      >
        <FieldGroup>
          <form.AppField name="name">
            {(field) => <field.TextField label="Name" autoComplete="off" />}
          </form.AppField>
          {create.error && <FieldError>{create.error.message}</FieldError>}
        </FieldGroup>
      </form>
      <DialogFooter>
        <DialogClose render={<Button variant="outline" />}>Cancel</DialogClose>
        <Button type="submit" form={formId} disabled={create.isPending}>
          Create folder
        </Button>
      </DialogFooter>
    </>
  );
}

export function UnlockLibraryDialog({
  open,
  library,
  onOpenChange,
}: {
  open: boolean;
  library?: Library;
  onOpenChange: (open: boolean) => void;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        {library && <UnlockLibraryForm library={library} onUnlocked={() => onOpenChange(false)} />}
      </DialogContent>
    </Dialog>
  );
}

function UnlockLibraryForm({ library, onUnlocked }: { library: Library; onUnlocked: () => void }) {
  const { unlock } = useLibraryKeys();
  const formId = useId();
  const open = useMutation({
    mutationFn: (passphrase: string) => openKeyEnvelope(library.key_envelope ?? "", passphrase),
    onSuccess: (keys) => unlock(library.id, keys),
  });
  const form = useAppForm({
    defaultValues: { passphrase: "" },
    validators: { onSubmit: z.object({ passphrase: z.string().min(1, "Enter the passphrase.") }) },
    onSubmit: ({ value }) => open.mutate(value.passphrase, { onSuccess: onUnlocked }),
  });

  return (
    <>
      <DialogHeader>
        <DialogTitle>Unlock {library.name}</DialogTitle>
        <DialogDescription>
          Enter the passphrase to show the files and folders. The passphrase stays in this browser.
        </DialogDescription>
      </DialogHeader>
      <form
        id={formId}
        noValidate
        onSubmit={(event) => {
          event.preventDefault();
          void form.handleSubmit();
        }}
      >
        <FieldGroup>
          <form.AppField name="passphrase">
            {(field) => (
              <field.TextField
                label="Passphrase"
                type="password"
                autoComplete="current-password"
                autoFocus
              />
            )}
          </form.AppField>
          {open.error && <FieldError>{open.error.message}</FieldError>}
        </FieldGroup>
      </form>
      <DialogFooter>
        <DialogClose render={<Button variant="outline" />}>Cancel</DialogClose>
        <Button type="submit" form={formId} disabled={open.isPending}>
          {open.isPending ? "Unlocking…" : "Unlock"}
        </Button>
      </DialogFooter>
    </>
  );
}
