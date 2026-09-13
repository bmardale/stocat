import { useQueryClient } from "@tanstack/react-query";
import { useId } from "react";
import { z } from "zod";
import type {
  Backend,
  CheckSettingsInputBody,
  CreateBackendInputBody,
  UpdateBackendInputBody,
} from "@/api/generated/model";
import {
  getStorageBackendsListQueryKey,
  useStorageBackendsCheckSettings,
  useStorageBackendsCreate,
  useStorageBackendsDelete,
  useStorageBackendsUpdate,
} from "@/api/generated/storage/storage";
import { useAppForm } from "@/components/form";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
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

type BackendType = Backend["type"];

export const backendTypes: { value: BackendType; label: string; description: string }[] = [
  { value: "local", label: "Local disk", description: "A directory on the server." },
  { value: "s3", label: "S3", description: "AWS S3 or a compatible service." },
];

export function backendTypeLabel(type: BackendType) {
  return backendTypes.find((option) => option.value === type)?.label ?? type;
}

function formValues(backend?: Backend) {
  return {
    name: backend?.name ?? "",
    type: backend?.type ?? ("local" as BackendType),
    enabled: backend?.enabled ?? true,
    root: backend?.local?.root ?? "",
    endpoint: backend?.s3?.endpoint ?? "",
    region: backend?.s3?.region ?? "",
    bucket: backend?.s3?.bucket ?? "",
    prefix: backend?.s3?.prefix ?? "",
    forcePathStyle: backend?.s3?.force_path_style ?? false,
    accessKeyId: "",
    secretAccessKey: "",
  };
}

type FormValues = ReturnType<typeof formValues>;

function backendSchema(editing: boolean) {
  return z
    .object({
      name: z.string().trim().min(1, "Enter a name."),
      type: z.enum(["local", "s3"]),
      enabled: z.boolean(),
      root: z.string(),
      endpoint: z.string(),
      region: z.string(),
      bucket: z.string(),
      prefix: z.string(),
      forcePathStyle: z.boolean(),
      accessKeyId: z.string(),
      secretAccessKey: z.string(),
    })
    .superRefine((values, ctx) => {
      const require = (
        path: "root" | "region" | "bucket" | "accessKeyId" | "secretAccessKey",
        message: string,
      ) => {
        if (!values[path].trim()) {
          ctx.addIssue({ code: "custom", path: [path], message });
        }
      };
      if (values.type === "local") {
        require("root", "Enter a directory path.");
        return;
      }
      require("region", "Enter a region.");
      require("bucket", "Enter a bucket name.");
      // An update without credentials keeps the stored credentials.
      if (!editing || values.accessKeyId.trim() || values.secretAccessKey.trim()) {
        require("accessKeyId", "Enter the access key ID.");
        require("secretAccessKey", "Enter the secret access key.");
      }
    });
}

function requestBody(values: FormValues): UpdateBackendInputBody {
  const common = { name: values.name.trim(), enabled: values.enabled };
  if (values.type === "local") {
    return { ...common, local: { root: values.root.trim() } };
  }
  return {
    ...common,
    s3: {
      endpoint: values.endpoint.trim(),
      region: values.region.trim(),
      bucket: values.bucket.trim(),
      prefix: values.prefix.trim(),
      force_path_style: values.forcePathStyle,
      access_key_id: values.accessKeyId.trim(),
      secret_access_key: values.secretAccessKey.trim(),
    },
  };
}

function checkBody(values: FormValues, backend?: Backend): CheckSettingsInputBody {
  const { local, s3 } = requestBody(values);
  return { id: backend?.id, type: values.type, local, s3 };
}

export function StorageBackendDialog({
  open,
  backend,
  onOpenChange,
}: {
  open: boolean;
  backend?: Backend;
  onOpenChange: (open: boolean) => void;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[calc(100svh-2rem)] overflow-y-auto sm:max-w-lg">
        <StorageBackendForm backend={backend} onSaved={() => onOpenChange(false)} />
      </DialogContent>
    </Dialog>
  );
}

function StorageBackendForm({ backend, onSaved }: { backend?: Backend; onSaved: () => void }) {
  const queryClient = useQueryClient();
  const formId = useId();
  const editing = backend !== undefined;
  // Keep the mutation pending until the list shows the result.
  const refresh = () =>
    queryClient.invalidateQueries({ queryKey: getStorageBackendsListQueryKey() });
  const create = useStorageBackendsCreate({
    mutation: {
      onSuccess: async () => {
        toast.add({ type: "success", description: "Backend added." });
        await refresh();
      },
    },
  });
  const update = useStorageBackendsUpdate({
    mutation: {
      onSuccess: async () => {
        toast.add({ type: "success", description: "Changes saved." });
        await refresh();
      },
    },
  });
  const mutation = editing ? update : create;
  const check = useStorageBackendsCheckSettings();
  const form = useAppForm({
    defaultValues: formValues(backend),
    validators: { onSubmit: backendSchema(editing) },
    onSubmit: ({ value }) => {
      if (backend) {
        update.mutate({ id: backend.id, data: requestBody(value) }, { onSuccess: onSaved });
        return;
      }
      const data: CreateBackendInputBody = { ...requestBody(value), type: value.type };
      create.mutate({ data }, { onSuccess: onSaved });
    },
  });
  const testConnection = () => {
    void toast
      .promise(
        check.mutateAsync({ data: checkBody(form.state.values, backend) }).then((result) => {
          if (!result.ok) {
            throw new Error(result.message);
          }
          return result;
        }),
        {
          loading: "Testing the connection…",
          success: "The connection works.",
          error: (error: unknown) =>
            error instanceof Error ? error.message : "The connection failed.",
        },
      )
      .catch(() => {});
  };

  return (
    <>
      <DialogHeader>
        <DialogTitle>{backend ? `Edit ${backend.name}` : "Add storage backend"}</DialogTitle>
        <DialogDescription>
          {backend
            ? `${backendTypeLabel(backend.type)} backend. You cannot change the type.`
            : "Choose where the server keeps the contents of files."}
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
          {!editing && (
            <form.Field name="type">
              {(field) => (
                <FieldSet>
                  <FieldLegend variant="label">Type</FieldLegend>
                  <RadioGroup
                    value={field.state.value}
                    onValueChange={(value) => field.handleChange(value as BackendType)}
                    className="grid-cols-2"
                  >
                    {backendTypes.map((option) => (
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
          )}
          <form.AppField name="name">
            {(field) => <field.TextField label="Name" autoComplete="off" />}
          </form.AppField>
          <form.Subscribe selector={(state) => state.values.type}>
            {(type) =>
              type === "local" ? (
                <form.AppField name="root">
                  {(field) => (
                    <field.TextField
                      label="Directory"
                      placeholder="/var/lib/stocat/objects"
                      description="Use an absolute path. The server process must have write access."
                      autoComplete="off"
                      spellCheck={false}
                    />
                  )}
                </form.AppField>
              ) : (
                <>
                  <form.AppField name="endpoint">
                    {(field) => (
                      <field.TextField
                        label="Endpoint"
                        type="url"
                        placeholder="https://s3.example.com"
                        description="Leave empty for AWS S3."
                        autoComplete="off"
                      />
                    )}
                  </form.AppField>
                  <div className="grid gap-5 sm:grid-cols-2">
                    <form.AppField name="region">
                      {(field) => (
                        <field.TextField
                          label="Region"
                          placeholder="us-east-1"
                          autoComplete="off"
                        />
                      )}
                    </form.AppField>
                    <form.AppField name="bucket">
                      {(field) => (
                        <field.TextField label="Bucket" autoComplete="off" spellCheck={false} />
                      )}
                    </form.AppField>
                  </div>
                  <form.AppField name="prefix">
                    {(field) => (
                      <field.TextField
                        label="Prefix"
                        description="Optional. The server stores objects below this key prefix."
                        autoComplete="off"
                        spellCheck={false}
                      />
                    )}
                  </form.AppField>
                  <form.AppField name="forcePathStyle">
                    {(field) => (
                      <field.SwitchField
                        label="Path-style URLs"
                        description="Put the bucket name in the URL path. RustFS needs this."
                      />
                    )}
                  </form.AppField>
                  <FieldSet>
                    <FieldLegend variant="label">Credentials</FieldLegend>
                    <FieldDescription>
                      {editing
                        ? "Leave both fields empty to keep the stored credentials."
                        : "The server encrypts the credentials before it stores them."}
                    </FieldDescription>
                    <form.AppField name="accessKeyId">
                      {(field) => (
                        <field.TextField
                          label="Access key ID"
                          autoComplete="off"
                          spellCheck={false}
                        />
                      )}
                    </form.AppField>
                    <form.AppField name="secretAccessKey">
                      {(field) => (
                        <field.TextField
                          label="Secret access key"
                          type="password"
                          autoComplete="new-password"
                        />
                      )}
                    </form.AppField>
                  </FieldSet>
                </>
              )
            }
          </form.Subscribe>
          <form.AppField name="enabled">
            {(field) => (
              <field.SwitchField
                label="Enabled"
                description="New libraries can use only enabled backends."
              />
            )}
          </form.AppField>
          {mutation.error && <FieldError>{mutation.error.message}</FieldError>}
        </FieldGroup>
      </form>
      <DialogFooter>
        <Button
          type="button"
          variant="outline"
          className="sm:mr-auto"
          disabled={check.isPending}
          onClick={testConnection}
        >
          {check.isPending ? "Testing…" : "Test connection"}
        </Button>
        <DialogClose render={<Button variant="outline" />}>Cancel</DialogClose>
        <Button type="submit" form={formId} disabled={mutation.isPending}>
          {editing ? "Save changes" : "Add backend"}
        </Button>
      </DialogFooter>
    </>
  );
}

export function DeleteStorageBackendDialog({
  open,
  backend,
  onOpenChange,
}: {
  open: boolean;
  backend?: Backend;
  onOpenChange: (open: boolean) => void;
}) {
  const queryClient = useQueryClient();
  const remove = useStorageBackendsDelete({
    mutation: {
      onSuccess: async () => {
        toast.add({ type: "success", description: "Backend deleted." });
        await queryClient.invalidateQueries({ queryKey: getStorageBackendsListQueryKey() });
      },
    },
  });

  return (
    <AlertDialog
      open={open}
      onOpenChange={(next) => {
        onOpenChange(next);
        if (!next) {
          remove.reset();
        }
      }}
    >
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete {backend?.name}?</AlertDialogTitle>
          <AlertDialogDescription>
            The server removes the settings and the stored credentials. Files in the backend stay
            where they are. You cannot delete a backend that a library uses.
          </AlertDialogDescription>
        </AlertDialogHeader>
        {remove.error && <FieldError>{remove.error.message}</FieldError>}
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            disabled={remove.isPending}
            onClick={() =>
              backend && remove.mutate({ id: backend.id }, { onSuccess: () => onOpenChange(false) })
            }
          >
            Delete
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
