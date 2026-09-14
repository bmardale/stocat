import type { QuotaMode } from "@/api/generated/model";
import { Field, FieldError, FieldLabel, FieldLegend, FieldSet } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import type { QuotaValue } from "@/lib/quota";

export function QuotaPicker({
  id,
  label,
  value,
  onChange,
  errors,
  inheritLabel,
}: {
  id: string;
  label: string;
  value: QuotaValue;
  onChange: (value: QuotaValue) => void;
  errors?: Array<{ message?: string } | undefined>;
  // Omit inheritLabel when the quota has no level to inherit from.
  inheritLabel?: string;
}) {
  const modes: { value: QuotaMode; label: string }[] = [
    ...(inheritLabel ? [{ value: "inherit" as const, label: inheritLabel }] : []),
    { value: "unlimited", label: "No limit" },
    { value: "limited", label: "Limit" },
  ];
  const invalid = Boolean(errors?.length);

  return (
    <FieldSet>
      <FieldLegend variant="label">{label}</FieldLegend>
      <RadioGroup
        value={value.mode}
        onValueChange={(mode) => onChange({ ...value, mode: mode as QuotaMode })}
        className="flex flex-wrap gap-x-5 gap-y-2"
      >
        {modes.map((mode) => (
          <Field key={mode.value} orientation="horizontal" className="w-auto">
            <RadioGroupItem id={`${id}-${mode.value}`} value={mode.value} />
            <FieldLabel htmlFor={`${id}-${mode.value}`} className="font-normal">
              {mode.label}
            </FieldLabel>
          </Field>
        ))}
      </RadioGroup>
      {value.mode === "limited" && (
        <Field orientation="horizontal" data-invalid={invalid}>
          <FieldLabel htmlFor={`${id}-gib`} className="sr-only">
            {label} in GiB
          </FieldLabel>
          <Input
            id={`${id}-gib`}
            type="number"
            min="0"
            step="any"
            inputMode="decimal"
            autoComplete="off"
            className="max-w-40"
            value={value.gib}
            aria-invalid={invalid}
            onChange={(event) => onChange({ ...value, gib: event.target.value })}
          />
          <span className="text-sm text-muted-foreground">GiB</span>
        </Field>
      )}
      <FieldError errors={errors} />
    </FieldSet>
  );
}
