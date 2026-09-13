import { createFormHook, createFormHookContexts } from "@tanstack/react-form";
import { useId } from "react";
import { Field, FieldDescription, FieldError, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";

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

export const { useAppForm } = createFormHook({
  fieldContext,
  formContext,
  fieldComponents: { TextField },
  formComponents: {},
});
