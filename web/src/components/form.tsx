import { createFormHook, createFormHookContexts } from "@tanstack/react-form";
import { useId } from "react";
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldError,
  FieldLabel,
} from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";

const { fieldContext, formContext, useFieldContext } = createFormHookContexts();

type TextFieldProps = Omit<
  React.ComponentProps<"input">,
  "id" | "name" | "value" | "onChange" | "onBlur"
> & {
  label: string;
  description?: string;
};

function TextField({ label, description, ...props }: TextFieldProps) {
  const field = useFieldContext<string>();
  const id = useId();
  const isInvalid = !field.state.meta.isValid;

  return (
    <Field data-invalid={isInvalid}>
      <FieldLabel htmlFor={id}>{label}</FieldLabel>
      <Input
        id={id}
        name={field.name}
        value={field.state.value}
        onBlur={field.handleBlur}
        onChange={(event) => field.handleChange(event.target.value)}
        aria-invalid={isInvalid}
        {...props}
      />
      {description && <FieldDescription>{description}</FieldDescription>}
      <FieldError errors={field.state.meta.errors} />
    </Field>
  );
}

function SwitchField({
  label,
  description,
  disabled,
}: {
  label: string;
  description?: string;
  disabled?: boolean;
}) {
  const field = useFieldContext<boolean>();
  const id = useId();

  return (
    <Field orientation="horizontal">
      <FieldContent>
        <FieldLabel htmlFor={id}>{label}</FieldLabel>
        {description && <FieldDescription>{description}</FieldDescription>}
      </FieldContent>
      <Switch
        id={id}
        name={field.name}
        checked={field.state.value}
        disabled={disabled}
        onCheckedChange={(checked) => field.handleChange(checked)}
        onBlur={field.handleBlur}
      />
    </Field>
  );
}

export const { useAppForm } = createFormHook({
  fieldContext,
  formContext,
  fieldComponents: { TextField, SwitchField },
  formComponents: {},
});
