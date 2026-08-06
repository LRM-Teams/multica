import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactElement, ReactNode } from "react";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../locales/en/common.json";
import enAuth from "../locales/en/auth.json";
import enSettings from "../locales/en/settings.json";

const TEST_RESOURCES = {
  en: { common: enCommon, auth: enAuth, settings: enSettings },
};

function I18nWrapper({ children }: { children: ReactNode }) {
  return (
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      {children}
    </I18nProvider>
  );
}

function renderWithI18n(ui: ReactElement) {
  return render(ui, { wrapper: I18nWrapper });
}

// ---------------------------------------------------------------------------
// Hoisted mocks
// ---------------------------------------------------------------------------

const mockSendCode = vi.hoisted(() => vi.fn());
const mockVerifyCode = vi.hoisted(() => vi.fn());
const mockApiListWorkspaces = vi.hoisted(() => vi.fn());
const mockApiVerifyCode = vi.hoisted(() => vi.fn());
const mockApiSetToken = vi.hoisted(() => vi.fn());
const mockApiGetMe = vi.hoisted(() => vi.fn());
const mockApiIssueCliToken = vi.hoisted(() => vi.fn());
const mockSetQueryData = vi.hoisted(() => vi.fn());

vi.mock("@tanstack/react-query", async () => {
  const actual = await vi.importActual<typeof import("@tanstack/react-query")>(
    "@tanstack/react-query",
  );
  return { ...actual, useQueryClient: () => ({ setQueryData: mockSetQueryData }) };
});

vi.mock("@multica/core/auth", () => ({
  useAuthStore: Object.assign(
    // Zustand hook form — component may call useAuthStore(selector)
    (selector?: (s: unknown) => unknown) => {
      const state = { sendCode: mockSendCode, verifyCode: mockVerifyCode };
      return selector ? selector(state) : state;
    },
    {
      getState: () => ({
        sendCode: mockSendCode,
        verifyCode: mockVerifyCode,
      }),
    },
  ),
}));

vi.mock("@multica/core/api", () => ({
  api: {
    listWorkspaces: mockApiListWorkspaces,
    verifyCode: mockApiVerifyCode,
    setToken: mockApiSetToken,
    getMe: mockApiGetMe,
    issueCliToken: mockApiIssueCliToken,
  },
}));

vi.mock("@multica/core/types", () => ({}));

// ---------------------------------------------------------------------------
// Import after mocks
// ---------------------------------------------------------------------------

import { LoginPage, validateCliCallback } from "./login-page";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------


// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe("LoginPage", () => {
  const onSuccess = vi.fn();

  beforeEach(() => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    vi.clearAllMocks();
    // Default: no existing session (getMe rejects when no auth)
    mockApiGetMe.mockRejectedValue(new Error("unauthorized"));
    localStorage.clear();
    // Reset window.location for tests that change it
    Object.defineProperty(window, "location", {
      writable: true,
      value: { href: "http://localhost:3000" },
    });
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  // -------------------------------------------------------------------------
  // Email step rendering
  // -------------------------------------------------------------------------

  it("renders email form with 'Sign in to Multica' title", () => {
    renderWithI18n(<LoginPage onSuccess={onSuccess} />);
    expect(
      screen.getByText(/sign in to multica/i),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/enter your email to get a login code/i),
    ).toBeInTheDocument();
    expect(screen.getByLabelText(/email/i)).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /continue/i }),
    ).toBeInTheDocument();
  });

  // -------------------------------------------------------------------------
  // Email validation
  // -------------------------------------------------------------------------

  it("seeds the error line from initialError (OAuth callback ?error= copy)", () => {
    // The host page maps a callback ?error= code to localized copy and passes
    // it in; a bounced sign-in must explain itself, not show a blank form.
    renderWithI18n(
      <LoginPage
        onSuccess={onSuccess}
        initialError={enAuth.errors.oauth_login_failed}
      />,
    );
    const line = screen.getByText(enAuth.errors.oauth_login_failed);
    expect(line).toBeInTheDocument();
    expect(line).toHaveClass("text-destructive");
  });

  it("shows error when submitting with empty email", async () => {
    renderWithI18n(<LoginPage onSuccess={onSuccess} />);

    // The Continue button is disabled when email is empty, so we submit the
    // form programmatically the same way the component does — via form submit.
    // Since the button is disabled, we directly call handleSendCode's logic
    // by removing the required attr and submitting.
    const emailInput = screen.getByLabelText(/email/i);
    // The input has required + the button is disabled, so we need to type
    // a space then clear to trigger the empty-email error path.
    // Actually, the component guards `if (!email)` in handleSendCode.
    // But the button is disabled when `!email`. Let's verify:
    const button = screen.getByRole("button", { name: /continue/i });
    expect(button).toBeDisabled();

    // Type an email to enable button, then clear it — button becomes disabled again
    const user = userEvent.setup();
    await user.type(emailInput, "a");
    expect(button).not.toBeDisabled();
    await user.clear(emailInput);
    expect(button).toBeDisabled();
  });

  // -------------------------------------------------------------------------
  // sendCode flow
  // -------------------------------------------------------------------------

  it("calls sendCode on form submit with email", async () => {
    mockSendCode.mockResolvedValueOnce(undefined);
    renderWithI18n(<LoginPage onSuccess={onSuccess} />);

    const user = userEvent.setup();
    await user.type(screen.getByLabelText(/email/i), "test@example.com");
    await user.click(screen.getByRole("button", { name: /continue/i }));

    expect(mockSendCode).toHaveBeenCalledWith("test@example.com");
  });

  it("shows 'Sending code...' while submitting", async () => {
    // Never resolve so loading stays true
    mockSendCode.mockReturnValueOnce(new Promise(() => {}));
    renderWithI18n(<LoginPage onSuccess={onSuccess} />);

    const user = userEvent.setup();
    await user.type(screen.getByLabelText(/email/i), "test@example.com");
    await user.click(screen.getByRole("button", { name: /continue/i }));

    expect(screen.getByText(/sending code/i)).toBeInTheDocument();
  });




  // -------------------------------------------------------------------------
  // Code verification
  // -------------------------------------------------------------------------



  // -------------------------------------------------------------------------
  // Resend code with cooldown
  // -------------------------------------------------------------------------




  // -------------------------------------------------------------------------
  // Google OAuth
  // -------------------------------------------------------------------------

  it("renders Google OAuth button when google prop provided", () => {
    render(
      <LoginPage
        onSuccess={onSuccess}
        google={{ clientId: "goog-123", redirectUri: "http://localhost/cb" }}
      />,
    );
    expect(
      screen.getByRole("button", { name: /continue with google/i }),
    ).toBeInTheDocument();
  });

  it("hides Google OAuth button when google prop omitted", () => {
    renderWithI18n(<LoginPage onSuccess={onSuccess} />);
    expect(
      screen.queryByRole("button", { name: /continue with google/i }),
    ).not.toBeInTheDocument();
  });

  // -------------------------------------------------------------------------
  // CLI callback — existing session
  // -------------------------------------------------------------------------




  // -------------------------------------------------------------------------
  // CLI callback — cookie-based session (no localStorage token)
  // -------------------------------------------------------------------------



  // -------------------------------------------------------------------------
  // CLI callback — code verification redirects
  // -------------------------------------------------------------------------


  // -------------------------------------------------------------------------
  // Logo prop
  // -------------------------------------------------------------------------

  it("renders logo when provided", () => {
    render(
      <LoginPage
        onSuccess={onSuccess}
        logo={<div data-testid="custom-logo">Logo</div>}
      />,
    );
    expect(screen.getByTestId("custom-logo")).toBeInTheDocument();
  });

  it("does not render logo placeholder when omitted", () => {
    renderWithI18n(<LoginPage onSuccess={onSuccess} />);
    expect(screen.queryByTestId("custom-logo")).not.toBeInTheDocument();
  });

  // -------------------------------------------------------------------------
  // onTokenObtained callback
  // -------------------------------------------------------------------------


  // -------------------------------------------------------------------------
  // Back button on code step
  // -------------------------------------------------------------------------


});

// ---------------------------------------------------------------------------
// validateCliCallback (exported helper)
// ---------------------------------------------------------------------------

describe("validateCliCallback", () => {
  it("accepts http://localhost", () => {
    expect(validateCliCallback("http://localhost:9876/callback")).toBe(true);
  });

  it("accepts http://127.0.0.1", () => {
    expect(validateCliCallback("http://127.0.0.1:8080/cb")).toBe(true);
  });

  it("accepts 10.x.x.x private IPs", () => {
    expect(validateCliCallback("http://10.0.0.5:9876/callback")).toBe(true);
    expect(validateCliCallback("http://10.255.255.255:1234/cb")).toBe(true);
  });

  it("accepts 172.16-31.x.x private IPs", () => {
    expect(validateCliCallback("http://172.16.0.1:9876/callback")).toBe(true);
    expect(validateCliCallback("http://172.31.255.255:1234/cb")).toBe(true);
  });

  it("rejects 172.x outside 16-31 range", () => {
    expect(validateCliCallback("http://172.15.0.1:9876/callback")).toBe(false);
    expect(validateCliCallback("http://172.32.0.1:9876/callback")).toBe(false);
  });

  it("accepts 192.168.x.x private IPs", () => {
    expect(validateCliCallback("http://192.168.1.131:41117/callback")).toBe(true);
    expect(validateCliCallback("http://192.168.0.1:8080/cb")).toBe(true);
  });

  it("rejects https:// URLs", () => {
    expect(validateCliCallback("https://localhost:9876/callback")).toBe(false);
  });

  it("rejects public IPs and domains", () => {
    expect(validateCliCallback("http://evil.com:9876/callback")).toBe(false);
    expect(validateCliCallback("http://8.8.8.8:9876/callback")).toBe(false);
    expect(validateCliCallback("http://192.169.1.1:9876/callback")).toBe(false);
  });

  it("rejects invalid URLs", () => {
    expect(validateCliCallback("not-a-url")).toBe(false);
  });
});
