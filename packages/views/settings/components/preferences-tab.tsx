"use client";

import { useMemo } from "react";
import { toast } from "sonner";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@multica/ui/components/ui/select";
import { useTheme } from "@multica/ui/components/common/theme-provider";
import { cn } from "@multica/ui/lib/utils";
import {
  DEFAULT_LOCALE,
  SUPPORTED_LOCALES,
  type SupportedLocale,
} from "@multica/core/i18n";
import { useLocaleAdapter } from "@multica/core/i18n/react";
import { useAuthStore } from "@multica/core/auth";
import { api } from "@multica/core/api";
import { browserTimezone, timezoneOptions } from "../../common/timezone-select";
import { useT } from "../../i18n";

/**
 * Theme preview chrome. Colors come only from semantic tokens
 * (`--background` / `--sidebar` / `--muted` / …) via utility classes.
 * Scope with `.light` / `.dark` so System (split) stays correct even when
 * the document root is the opposite theme — do not use `dark:` variants
 * here (`@custom-variant dark (&:is(.dark *))` would still match under
 * a dark html root).
 *
 * Traffic-light dots are OS window chrome, not product palette.
 */
function WindowMockup({
  variant,
  className,
}: {
  variant: "light" | "dark";
  className?: string;
}) {
  return (
    <div
      className={cn(
        variant === "dark" ? "dark" : "light",
        "flex h-full w-full flex-col",
        className,
      )}
      style={{ colorScheme: variant }}
      data-theme-preview={variant}
    >
      {/* Title bar — muted elevates above pane in dark; border separates in light */}
      <div className="flex items-center gap-[3px] border-b border-border bg-muted px-2 py-1.5">
        <span className="size-[6px] rounded-full bg-[#ff5f57]" />
        <span className="size-[6px] rounded-full bg-[#febc2e]" />
        <span className="size-[6px] rounded-full bg-[#28c840]" />
      </div>
      {/* Content area */}
      <div className="flex flex-1 bg-background">
        {/* Sidebar */}
        <div className="w-[30%] space-y-1 bg-sidebar p-2">
          <div className="h-1 w-3/4 rounded-full bg-foreground/20" />
          <div className="h-1 w-1/2 rounded-full bg-foreground/20" />
        </div>
        {/* Main */}
        <div className="flex-1 space-y-1.5 p-2">
          <div className="h-1.5 w-4/5 rounded-full bg-foreground/20" />
          <div className="h-1 w-full rounded-full bg-muted-foreground/40" />
          <div className="h-1 w-3/5 rounded-full bg-muted-foreground/40" />
        </div>
      </div>
    </div>
  );
}

export function PreferencesTab() {
  const { theme, setTheme } = useTheme();
  const { t, i18n } = useT("settings");
  const localeAdapter = useLocaleAdapter();
  const user = useAuthStore((s) => s.user);

  // i18next.language can be a region-tagged BCP-47 string (e.g. "en-US",
  // "zh-Hans-CN") returned by intl-localematcher. Normalize to a supported
  // locale before comparing — otherwise the radio shows neither option active.
  const currentLocale: SupportedLocale = SUPPORTED_LOCALES.includes(
    i18n.language as SupportedLocale,
  )
    ? (i18n.language as SupportedLocale)
    : DEFAULT_LOCALE;

  const themeOptions = [
    { value: "light" as const, label: t(($) => $.preferences.theme.light) },
    { value: "dark" as const, label: t(($) => $.preferences.theme.dark) },
    { value: "system" as const, label: t(($) => $.preferences.theme.system) },
  ];

  const languageOptions: { value: SupportedLocale; label: string }[] = [
    { value: "en", label: t(($) => $.preferences.language.english) },
    { value: "zh-Hans", label: t(($) => $.preferences.language.chinese) },
  ];

  // Persist locally → sync to user.language → reload. Reload (vs in-place
  // changeLanguage) avoids hydration mismatch and is the i18next-recommended
  // pattern for App Router.
  //
  // If the cross-device sync (PATCH /api/me) fails, the local cookie is
  // already written so the new locale will take effect after reload — but
  // the user's other devices won't see the change. Surface that explicitly
  // via a toast and delay the reload long enough for the toast to be read,
  // otherwise the failure would be invisible.
  const handleLanguageChange = async (next: SupportedLocale) => {
    if (next === currentLocale) return;
    localeAdapter.persist(next);

    let syncFailed = false;
    if (user) {
      try {
        await api.updateMe({ language: next });
      } catch {
        syncFailed = true;
      }
    }

    if (syncFailed) {
      toast.warning(t(($) => $.preferences.language.sync_failed));
      // Give the toast 2.5s of visible time before navigating away.
      setTimeout(() => window.location.reload(), 2500);
      return;
    }
    window.location.reload();
  };

  return (
    <div className="space-y-8">
      <section className="space-y-4">
        <h2 className="text-sm font-semibold">
          {t(($) => $.preferences.theme.title)}
        </h2>
        <div className="flex gap-6" role="radiogroup">
          {themeOptions.map((opt) => {
            const active = theme === opt.value;
            return (
              <button
                type="button"
                key={opt.value}
                role="radio"
                aria-checked={active}
                onClick={() => setTheme(opt.value)}
                className="group flex flex-col items-center gap-2"
              >
                <div
                  className={cn(
                    "aspect-[4/3] w-36 overflow-hidden rounded-lg ring-1 transition-all",
                    active
                      ? "ring-2 ring-brand"
                      : "ring-border hover:ring-2 hover:ring-border"
                  )}
                >
                  {opt.value === "system" ? (
                    <div className="relative h-full w-full">
                      <WindowMockup
                        variant="light"
                        className="absolute inset-0"
                      />
                      <WindowMockup
                        variant="dark"
                        className="absolute inset-0 [clip-path:inset(0_0_0_50%)]"
                      />
                    </div>
                  ) : (
                    <WindowMockup variant={opt.value} />
                  )}
                </div>
                <span
                  className={cn(
                    "text-sm transition-colors",
                    active
                      ? "font-medium text-foreground"
                      : "text-muted-foreground"
                  )}
                >
                  {opt.label}
                </span>
              </button>
            );
          })}
        </div>
      </section>

      <section className="space-y-4">
        <h2 className="text-sm font-semibold">
          {t(($) => $.preferences.language.title)}
        </h2>
        <div className="flex gap-3" role="radiogroup">
          {languageOptions.map((opt) => {
            const active = currentLocale === opt.value;
            return (
              <button
                type="button"
                key={opt.value}
                role="radio"
                aria-checked={active}
                onClick={() => handleLanguageChange(opt.value)}
                className={cn(
                  "rounded-md border px-4 py-2 text-sm transition-colors",
                  active
                    ? "border-brand bg-brand/10 font-medium text-foreground"
                    : "border-border text-muted-foreground hover:border-foreground/30"
                )}
              >
                {opt.label}
              </button>
            );
          })}
        </div>
      </section>

      <TimezoneSection />
    </div>
  );
}

// Base UI rejects "" as a SelectItem value, so route the "no preference"
// state through this sentinel and translate at the wire boundary.
const BROWSER_TZ_VALUE = "__browser__";

function TimezoneSection() {
  const { t } = useT("settings");
  const user = useAuthStore((s) => s.user);
  const setUser = useAuthStore((s) => s.setUser);
  const stored = user?.timezone ?? null;
  const browser = browserTimezone();
  const value = stored ?? BROWSER_TZ_VALUE;

  // Full IANA list (from timezoneOptions in common/timezone-select) so a
  // user needing a non-curated zone isn't stuck with ~18 common ones.
  // Memoized — timezoneOptions enumerates ~600 IANA zones per call.
  const options = useMemo(
    () => timezoneOptions(stored ?? browser),
    [stored, browser],
  );

  const handleChange = async (next: string) => {
    if (next === value) return;
    const payload = next === BROWSER_TZ_VALUE ? "" : next;
    try {
      const updated = await api.updateMe({ timezone: payload });
      setUser(updated);
    } catch (err) {
      showErrorToast(
        err instanceof Error && err.message
          ? err.message
          : t(($) => $.preferences.timezone.sync_failed),
      );
    }
  };

  const formatTZLabel = (tz: string) => {
    if (tz === BROWSER_TZ_VALUE) {
      return `${browser}${t(($) => $.preferences.timezone.browser_suffix)}`;
    }
    return tz;
  };

  return (
    <section className="space-y-2">
      <h2 className="text-sm font-semibold">
        {t(($) => $.preferences.timezone.title)}
      </h2>
      <Select
        value={value}
        onValueChange={(next) => {
          if (next) handleChange(next);
        }}
      >
        <SelectTrigger size="sm" className="w-72 rounded-md font-mono text-xs">
          <SelectValue>{formatTZLabel(value)}</SelectValue>
        </SelectTrigger>
        <SelectContent align="start" className="max-h-72">
          <SelectItem value={BROWSER_TZ_VALUE} className="font-mono text-xs">
            {formatTZLabel(BROWSER_TZ_VALUE)}
          </SelectItem>
          {options.map((tz) => (
            <SelectItem key={tz} value={tz} className="font-mono text-xs">
              {formatTZLabel(tz)}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      <p className="text-[11px] leading-snug text-muted-foreground">
        {t(($) => $.preferences.timezone.hint)}
      </p>
    </section>
  );
}
