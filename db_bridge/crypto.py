"""Optional at-rest encryption for captured auth tokens.

Pass-through auth means the incoming ``Authorization`` header is stored in the
request row so the executor can replay it. With audit retention enabled those
tokens persist, so this module can encrypt the token value at the stub (capture
time) and decrypt it at the executor (just before replay), keeping plaintext
tokens out of the database.

Encryption is opt-in via ``BRIDGE_HEADER_ENCRYPTION_KEY`` (a urlsafe-base64
Fernet key, e.g. ``Fernet.generate_key()``). The ``cryptography`` package is an
optional dependency: if a key is configured but the package is missing, we fail
fast with a clear error rather than silently storing plaintext.
"""

from __future__ import annotations

from collections.abc import Mapping
from typing import Final

try:  # optional dependency
    from cryptography.fernet import Fernet

    _CRYPTO_AVAILABLE = True
except Exception:  # noqa: BLE001 -- any import failure means "unavailable"
    Fernet = None  # type: ignore[assignment]
    _CRYPTO_AVAILABLE = False

# Marks an encrypted value so decrypt is idempotent and plaintext passes through.
_PREFIX: Final = "enc:v1:"

# Header names whose values are sensitive enough to encrypt.
ENCRYPTED_HEADER_KEYS: Final = ("authorization",)


def encryption_available() -> bool:
    return _CRYPTO_AVAILABLE


class HeaderCipher:
    """Encrypts/decrypts individual header values with Fernet."""

    def __init__(self, key: str):
        if not _CRYPTO_AVAILABLE:
            raise RuntimeError(
                "BRIDGE_HEADER_ENCRYPTION_KEY is set but the 'cryptography' "
                "package is not installed. Install it (pip install cryptography) "
                "or unset the key to use plaintext pass-through."
            )
        self._fernet = Fernet(key.encode() if isinstance(key, str) else key)

    def encrypt(self, plaintext: str) -> str:
        if plaintext.startswith(_PREFIX):
            return plaintext  # already encrypted
        token = self._fernet.encrypt(plaintext.encode("utf-8")).decode("ascii")
        return _PREFIX + token

    def decrypt(self, value: str) -> str:
        if not value.startswith(_PREFIX):
            return value  # plaintext (e.g. captured before encryption was enabled)
        return self._fernet.decrypt(value[len(_PREFIX):].encode("ascii")).decode("utf-8")


def build_cipher(key: str | None) -> HeaderCipher | None:
    """Return a cipher when a key is configured, else None.

    Raises if a key is set but ``cryptography`` is unavailable.
    """
    if not key:
        return None
    return HeaderCipher(key)


def encrypt_headers(
    headers: Mapping[str, str], cipher: HeaderCipher | None
) -> dict[str, str]:
    if cipher is None:
        return dict(headers)
    return {
        k: (cipher.encrypt(v) if k in ENCRYPTED_HEADER_KEYS else v)
        for k, v in headers.items()
    }


def decrypt_headers(
    headers: Mapping[str, str], cipher: HeaderCipher | None
) -> dict[str, str]:
    if cipher is None:
        return dict(headers)
    return {
        k: (cipher.decrypt(v) if k in ENCRYPTED_HEADER_KEYS else v)
        for k, v in headers.items()
    }
