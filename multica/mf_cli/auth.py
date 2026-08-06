"""Modelfactory authentication — login via Playwright, token cache."""

import json
import os
import time
from pathlib import Path
from playwright.sync_api import sync_playwright

from .config import (
    BASE_URL, LOGIN_URL, COOKIE_NAME, TOKEN_FILE, SESSION_FILE, CACHE_DIR,
)


def _load_env_credentials() -> tuple[str | None, str | None]:
    """Try to read MF_USERNAME / MF_PASSWORD from .env file next to this module."""
    env_file = Path(__file__).parent / ".env"
    if not env_file.exists():
        return None, None
    username = None
    password = None
    for line in env_file.read_text().splitlines():
        line = line.strip()
        if line.startswith("MF_USERNAME="):
            username = line.split("=", 1)[1].strip()
        elif line.startswith("MF_PASSWORD="):
            password = line.split("=", 1)[1].strip()
    return username, password


def _read_cookie_from_session(session_path: Path) -> str | None:
    """Extract the auth token from a Playwright storage state file."""
    if not session_path.exists():
        return None
    try:
        state = json.loads(session_path.read_text())
        for cookie in state.get("cookies", []):
            if cookie.get("name") == COOKIE_NAME:
                return cookie.get("value")
    except (json.JSONDecodeError, KeyError):
        pass
    return None


def _login_with_playwright(
    username: str, password: str, headless: bool = True
) -> str:
    """Log into Modelfactory using Playwright, return the auth token."""
    CACHE_DIR.mkdir(parents=True, exist_ok=True)
    with sync_playwright() as p:
        browser = p.chromium.launch(
            headless=headless,
            args=["--no-sandbox", "--disable-setuid-sandbox", "--disable-dev-shm-usage"],
        )
        ctx = browser.new_context(
            viewport={"width": 1440, "height": 900},
            ignore_https_errors=True,
        )
        page = ctx.new_page()

        # Navigate to home (will redirect to login)
        page.goto(LOGIN_URL, wait_until="domcontentloaded", timeout=45000)
        page.wait_for_timeout(3500)

        # Fill login form
        page.fill("#userName", username)
        page.fill("#pwd", password)
        try:
            page.check("#remember", timeout=3000)
        except Exception:
            pass

        # Submit
        page.click("button[type=submit]")
        page.wait_for_timeout(6000)

        # Check login success
        if "/login" in page.url:
            raise RuntimeError(
                f"Login failed — still on login page. Check credentials."
            )

        # Save session to cache
        ctx.storage_state(path=str(SESSION_FILE))
        browser.close()

    # Read and cache the token
    token = _read_cookie_from_session(SESSION_FILE)
    if not token:
        raise RuntimeError("Login succeeded but auth cookie not found.")

    TOKEN_FILE.write_text(token)
    TOKEN_FILE.chmod(0o600)
    return token


def get_token(
    username: str | None = None,
    password: str | None = None,
    headless: bool = True,
    force_login: bool = False,
) -> str:
    """Get a valid auth token.

    Uses cached token if available and fresh, otherwise logs in.
    Returns the JWT auth token string.
    """
    # Try cached token first (simple TTL: token valid as long as file < 6 hours old)
    if not force_login and TOKEN_FILE.exists():
        age = time.time() - TOKEN_FILE.stat().st_mtime
        if age < 6 * 3600:  # 6 hours
            return TOKEN_FILE.read_text().strip()

    # Try session file cookie
    if not force_login and SESSION_FILE.exists():
        token = _read_cookie_from_session(SESSION_FILE)
        if token:
            TOKEN_FILE.write_text(token)
            TOKEN_FILE.chmod(0o600)
            return token

    # Try .env fallback
    if not username or not password:
        env_u, env_p = _load_env_credentials()
        username = username or env_u
        password = password or env_p

    # Need to login
    if not username or not password:
        raise ValueError(
            "No cached token and no credentials provided. "
            "Run 'mf login -u USERNAME -p PASSWORD' first, "
            "set MF_USERNAME / MF_PASSWORD in mf_cli/.env, "
            "or pass --username / --password."
        )

    return _login_with_playwright(username, password, headless=headless)


def login(username: str, password: str, headless: bool = True) -> str:
    """Login and cache the token. Returns the token."""
    return get_token(username, password, headless=headless, force_login=True)
