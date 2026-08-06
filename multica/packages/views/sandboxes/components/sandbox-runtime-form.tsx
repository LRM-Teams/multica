"use client";

import { Plus, Trash2 } from "lucide-react";
import {
  SANDBOX_RUNTIME_PROVIDER_PRESETS,
  createEmptyRuntimeProviderEntry,
  type SandboxRuntimeFormState,
  type SandboxRuntimeProviderFormEntry,
} from "@multica/core/sandboxes/utils";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { RadioGroup, RadioGroupItem } from "@multica/ui/components/ui/radio-group";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@multica/ui/components/ui/select";
import { useT } from "../../i18n/use-t";

const CUSTOM_PROVIDER = "__custom__";

function providerSelectValue(provider: string): string {
  const trimmed = provider.trim();
  if (
    SANDBOX_RUNTIME_PROVIDER_PRESETS.includes(
      trimmed as (typeof SANDBOX_RUNTIME_PROVIDER_PRESETS)[number],
    )
  ) {
    return trimmed;
  }
  return CUSTOM_PROVIDER;
}

type SandboxRuntimeFormProps = {
  value: SandboxRuntimeFormState;
  onChange: (next: SandboxRuntimeFormState) => void;
};

export function SandboxRuntimeForm({ value, onChange }: SandboxRuntimeFormProps) {
  const { t } = useT("layout");

  const updateEntry = (
    key: string,
    patch: Partial<SandboxRuntimeProviderFormEntry>,
  ) => {
    onChange({
      ...value,
      entries: value.entries.map((entry) =>
        entry.key === key ? { ...entry, ...patch } : entry,
      ),
    });
  };

  const addEntry = () => {
    const entry = createEmptyRuntimeProviderEntry("openai");
    onChange({
      entries: [...value.entries, entry],
      defaultKey: value.defaultKey || entry.key,
    });
  };

  const removeEntry = (key: string) => {
    const entries = value.entries.filter((entry) => entry.key !== key);
    const first = entries[0];
    if (!first) {
      const empty = createEmptyRuntimeProviderEntry("openai");
      onChange({ entries: [empty], defaultKey: empty.key });
      return;
    }
    onChange({
      entries,
      defaultKey: value.defaultKey === key ? first.key : value.defaultKey,
    });
  };

  return (
    <div className="space-y-3">
      <div>
        <div className="text-sm font-medium">{t(($) => $.sandboxes_page.runtime_model_title)}</div>
        <p className="mt-1 text-xs text-muted-foreground">
          {t(($) => $.sandboxes_page.runtime_model_multi_hint)}
        </p>
      </div>

      <RadioGroup
        value={value.defaultKey}
        onValueChange={(defaultKey) => {
          if (typeof defaultKey === "string") onChange({ ...value, defaultKey });
        }}
        className="gap-3"
      >
        {value.entries.map((entry, index) => {
          const selectValue = providerSelectValue(entry.provider);
          return (
            <div key={entry.key} className="space-y-2 rounded-md border p-3">
              <div className="flex items-center justify-between gap-2">
                <Label className="flex cursor-pointer items-center gap-2 text-sm font-medium">
                  <RadioGroupItem value={entry.key} />
                  {t(($) => $.sandboxes_page.runtime_provider_default_label)}
                </Label>
                <div className="flex items-center gap-1">
                  <span className="text-xs text-muted-foreground">
                    {t(($) => $.sandboxes_page.runtime_provider_index, {
                      index: index + 1,
                    })}
                  </span>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    className="size-7"
                    onClick={() => removeEntry(entry.key)}
                    aria-label={t(($) => $.sandboxes_page.runtime_provider_remove)}
                  >
                    <Trash2 className="size-3.5" />
                  </Button>
                </div>
              </div>

              <div className="grid gap-2 sm:grid-cols-2">
                <div className="space-y-1.5">
                  <Label className="text-xs text-muted-foreground">
                    {t(($) => $.sandboxes_page.runtime_provider_label)}
                  </Label>
                  <Select
                    value={selectValue}
                    onValueChange={(next) => {
                      if (typeof next !== "string") return;
                      if (next === CUSTOM_PROVIDER) {
                        updateEntry(entry.key, {
                          provider:
                            selectValue === CUSTOM_PROVIDER ? entry.provider : "",
                        });
                        return;
                      }
                      updateEntry(entry.key, { provider: next });
                    }}
                  >
                    <SelectTrigger className="h-9 w-full">
                      <SelectValue
                        placeholder={t(($) => $.sandboxes_page.runtime_provider_placeholder)}
                      />
                    </SelectTrigger>
                    <SelectContent>
                      {SANDBOX_RUNTIME_PROVIDER_PRESETS.map((name) => (
                        <SelectItem key={name} value={name}>
                          {name}
                        </SelectItem>
                      ))}
                      <SelectItem value={CUSTOM_PROVIDER}>
                        {t(($) => $.sandboxes_page.runtime_provider_custom)}
                      </SelectItem>
                    </SelectContent>
                  </Select>
                </div>
                {selectValue === CUSTOM_PROVIDER ? (
                  <div className="space-y-1.5">
                    <Label className="text-xs text-muted-foreground">
                      {t(($) => $.sandboxes_page.runtime_provider_custom_name)}
                    </Label>
                    <Input
                      className="h-9"
                      placeholder={t(($) => $.sandboxes_page.runtime_provider_custom_placeholder)}
                      value={entry.provider}
                      onChange={(e) => updateEntry(entry.key, { provider: e.target.value })}
                    />
                  </div>
                ) : (
                  <div className="space-y-1.5">
                    <Label className="text-xs text-muted-foreground">
                      {t(($) => $.sandboxes_page.model_placeholder)}
                    </Label>
                    <Input
                      className="h-9"
                      placeholder={t(($) => $.sandboxes_page.model_placeholder)}
                      value={entry.model}
                      onChange={(e) => updateEntry(entry.key, { model: e.target.value })}
                    />
                  </div>
                )}
              </div>

              {selectValue === CUSTOM_PROVIDER ? (
                <Input
                  className="h-9"
                  placeholder={t(($) => $.sandboxes_page.model_placeholder)}
                  value={entry.model}
                  onChange={(e) => updateEntry(entry.key, { model: e.target.value })}
                />
              ) : null}

              <Input
                className="h-9"
                type="password"
                autoComplete="off"
                placeholder={t(($) => $.sandboxes_page.api_key_placeholder)}
                value={entry.apiKey}
                onChange={(e) => updateEntry(entry.key, { apiKey: e.target.value })}
              />
              <Input
                className="h-9"
                placeholder={t(($) => $.sandboxes_page.base_url_placeholder)}
                value={entry.baseUrl}
                onChange={(e) => updateEntry(entry.key, { baseUrl: e.target.value })}
              />
            </div>
          );
        })}
      </RadioGroup>

      <Button type="button" variant="outline" size="sm" onClick={addEntry}>
        <Plus className="mr-1.5 size-3.5" />
        {t(($) => $.sandboxes_page.runtime_provider_add)}
      </Button>
    </div>
  );
}
