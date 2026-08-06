import type { ReactNode } from "react";
import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../locales/en/common.json";
import enWorkspace from "../locales/en/workspace.json";
import { CreateWorkspaceForm } from "./create-workspace-form";

const TEST_RESOURCES = {
  en: { common: enCommon, workspace: enWorkspace },
};

const mockMutate = vi.fn();
vi.mock("@multica/core/workspace/mutations", () => ({
  useCreateWorkspace: () => ({ mutate: mockMutate, isPending: false }),
}));

function I18nWrapper({ children }: { children: ReactNode }) {
  return (
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      {children}
    </I18nProvider>
  );
}

function renderForm(onSuccess = vi.fn()) {
  const qc = new QueryClient();
  return render(
    <QueryClientProvider client={qc}>
      <CreateWorkspaceForm onSuccess={onSuccess} />
    </QueryClientProvider>,
    { wrapper: I18nWrapper },
  );
}

describe("CreateWorkspaceForm", () => {
  beforeEach(() => mockMutate.mockReset());

  it("auto-generates slug from name until user edits slug", () => {
    renderForm();
    fireEvent.change(screen.getByLabelText(/workspace name/i), {
      target: { value: "Acme Corp" },
    });
    expect(screen.getByDisplayValue("acme-corp")).toBeInTheDocument();
  });

  it("stops auto-generating slug once user edits slug directly", () => {
    renderForm();
    fireEvent.change(screen.getByLabelText(/workspace url/i), {
      target: { value: "custom" },
    });
    fireEvent.change(screen.getByLabelText(/workspace name/i), {
      target: { value: "Different Name" },
    });
    expect(screen.getByDisplayValue("custom")).toBeInTheDocument();
  });



  it("disables submit when slug has invalid format", () => {
    renderForm();
    fireEvent.change(screen.getByLabelText(/workspace name/i), {
      target: { value: "Valid Name" },
    });
    fireEvent.change(screen.getByLabelText(/workspace url/i), {
      target: { value: "Invalid Slug!" },
    });
    expect(
      screen.getByRole("button", { name: /create workspace/i }),
    ).toBeDisabled();
  });

  it("disables submit when slug is reserved", () => {
    renderForm();
    fireEvent.change(screen.getByLabelText(/workspace name/i), {
      target: { value: "Valid Name" },
    });
    fireEvent.change(screen.getByLabelText(/workspace url/i), {
      target: { value: "admin" },
    });
    expect(
      screen.getByRole("button", { name: /create workspace/i }),
    ).toBeDisabled();
    expect(screen.getByText(/reserved and cannot be used/i)).toBeInTheDocument();
  });
});
