"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Check, Copy, RefreshCw, Terminal } from "lucide-react";
import { copyText } from "@multica/ui/lib/clipboard";
import { testComputerReleaseOptions } from "@multica/core/releases/computer-metainfo";
import { useLocale } from "../../i18n/context";
import { landingCLICommands } from "./cli-commands";

const ENVIRONMENT =
  process.env.NEXT_PUBLIC_ENVIRONMENT === "test" ? "test" : "production";
/**
 * Scenario-first CLI section. Copy leans into servers / remote dev
 * boxes / headless setups rather than positioning CLI as a
 * lightweight Desktop. Two copy-and-paste command blocks.
 */
export function CliSection() {
  const { t } = useLocale();
  const d = t.download.cli;
  const {
    data: testRelease,
    isError: testReleaseError,
    isFetching: testReleaseFetching,
    refetch: refetchTestRelease,
  } = useQuery(
    testComputerReleaseOptions(ENVIRONMENT === "test"),
  );
  const { installCmd, setupCmd } = landingCLICommands({
    environment: ENVIRONMENT,
    appUrl: process.env.NEXT_PUBLIC_APP_URL ?? "",
    apiUrl: process.env.NEXT_PUBLIC_API_URL ?? "",
    computerVersion: testRelease?.tag ?? "",
  });
  const testReleaseUnavailable =
    ENVIRONMENT === "test" &&
    (!testRelease || testReleaseFetching);

  return (
    <section id="cli" className="bg-[#f7f7f5] py-20 text-[#0a0d12] sm:py-24">
      <div className="mx-auto max-w-[820px] px-4 sm:px-6 lg:px-8">
        <h2 className="font-[family-name:var(--font-serif)] text-[2.2rem] leading-[1.1] tracking-[-0.03em] sm:text-[2.6rem]">
          {d.title}
        </h2>
        <p className="mt-4 max-w-[620px] text-[15px] leading-7 text-[#0a0d12]/72">
          {d.sub}
        </p>

        <div className="mt-10 flex flex-col gap-5">
          <CommandBlock
            label={d.installLabel}
            cmd={installCmd}
            copyLabel={d.copyLabel}
            copiedLabel={d.copiedLabel}
            releaseState={
              testReleaseUnavailable
                ? testReleaseError
                  ? "error"
                  : "loading"
                : undefined
            }
            releaseErrorLabel={d.testVersionFailed}
            retryLabel={d.testVersionRetry}
            onRetry={
              testReleaseError
                ? () => void refetchTestRelease()
                : undefined
            }
          />
          <CommandBlock
            label={d.startLabel}
            cmd={setupCmd}
            copyLabel={d.copyLabel}
            copiedLabel={d.copiedLabel}
          />
        </div>

        <p className="mt-6 text-[13px] text-[#0a0d12]/60">{d.sshNote}</p>
      </div>
    </section>
  );
}

function CommandBlock({
  label,
  cmd,
  copyLabel,
  copiedLabel,
  releaseState,
  releaseErrorLabel,
  retryLabel,
  onRetry,
}: {
  label: string;
  cmd: string;
  copyLabel: string;
  copiedLabel: string;
  releaseState?: "loading" | "error";
  releaseErrorLabel?: string;
  retryLabel?: string;
  onRetry?: () => void;
}) {
  const [copied, setCopied] = useState(false);

  const onCopy = async () => {
    if (await copyText(cmd)) {
      setCopied(true);
      setTimeout(() => setCopied(false), 1800);
    }
  };

  return (
    <div>
      <p className="mb-2 text-[12px] font-medium uppercase tracking-[0.08em] text-[#0a0d12]/55">
        {label}
      </p>
      <div className="flex items-start gap-3 rounded-xl border border-[#0a0d12]/10 bg-white px-4 py-3 font-mono text-[13.5px]">
        <Terminal
          className="mt-0.5 size-4 shrink-0 text-[#0a0d12]/55"
          aria-hidden
        />
        <code className="min-w-0 flex-1 whitespace-pre-wrap break-all">
          {releaseState === "loading" ? (
            <span
              className="my-0.5 block h-4 w-56 animate-pulse rounded bg-[#0a0d12]/10"
              aria-hidden
            />
          ) : releaseState === "error" ? (
            releaseErrorLabel
          ) : (
            cmd
          )}
        </code>
        {releaseState ? (
          onRetry && (
            <button
              type="button"
              onClick={onRetry}
              aria-label={retryLabel}
              className="inline-flex shrink-0 items-center rounded-md p-1 text-[#0a0d12]/70 transition-colors hover:bg-[#0a0d12]/5 hover:text-[#0a0d12]"
            >
              <RefreshCw className="size-3.5" />
            </button>
          )
        ) : (
          <button
            type="button"
            onClick={onCopy}
            aria-label={copied ? copiedLabel : copyLabel}
            className="inline-flex shrink-0 items-center gap-1.5 rounded-md px-2 py-1 text-[12px] font-medium text-[#0a0d12]/70 transition-colors hover:bg-[#0a0d12]/5 hover:text-[#0a0d12]"
          >
            {copied ? (
              <>
                <Check className="size-3.5" />
                {copiedLabel}
              </>
            ) : (
              <>
                <Copy className="size-3.5" />
                {copyLabel}
              </>
            )}
          </button>
        )}
      </div>
    </div>
  );
}
