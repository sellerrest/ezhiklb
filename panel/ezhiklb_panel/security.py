"""Small security primitives: constant-time comparison, hashing, password
hashing (scrypt), ID/token generation, and the pinned-TLS ssl.SSLContext
builder used to dial nodes.

The TLS-pinning approach (point 2 of the fork) is TOFU: a node prints its own
self-signed certificate once at first boot; the operator pastes that exact
PEM into the panel's "Добавить узел" dialog; the panel then trusts *only*
that certificate as its own private CA for that one node. Standard TLS
verification then succeeds if and only if the node presents exactly that
certificate — no public CA involved, and a MITM without the pinned private
key cannot pass verification.
"""

from __future__ import annotations

import hashlib
import hmac
import secrets
import ssl


def constant_time_equal(a: str, b: str) -> bool:
    if not a or not b or len(a) != len(b):
        return False
    return hmac.compare_digest(a.encode(), b.encode())


def sha256_hex(value: str) -> str:
    return hashlib.sha256(value.encode()).hexdigest()


def new_token(nbytes: int = 32) -> str:
    return secrets.token_hex(nbytes)


def new_id(prefix: str) -> str:
    return f"{prefix}_{secrets.token_hex(8)}"


_SCRYPT_N, _SCRYPT_R, _SCRYPT_P, _SCRYPT_DKLEN = 2**14, 8, 1, 32


def hash_password(password: str) -> str:
    """scrypt (stdlib, no extra dependency) with a random salt — fine for a
    single-admin login, no need for a heavier KDF library."""
    salt = secrets.token_bytes(16)
    derived = hashlib.scrypt(password.encode(), salt=salt, n=_SCRYPT_N, r=_SCRYPT_R, p=_SCRYPT_P, dklen=_SCRYPT_DKLEN)
    return f"{salt.hex()}${derived.hex()}"


def verify_password(password: str, stored: str) -> bool:
    salt_hex, _, hash_hex = stored.partition("$")
    if not hash_hex:
        return False
    try:
        salt = bytes.fromhex(salt_hex)
    except ValueError:
        return False
    derived = hashlib.scrypt(password.encode(), salt=salt, n=_SCRYPT_N, r=_SCRYPT_R, p=_SCRYPT_P, dklen=_SCRYPT_DKLEN)
    return hmac.compare_digest(derived.hex(), hash_hex)


def cert_fingerprint(cert_pem: str) -> str:
    """SHA-256 fingerprint of the DER-encoded certificate, for display/audit
    only — the actual pinning uses the full PEM, not just the fingerprint."""
    der = ssl.PEM_cert_to_DER_cert(cert_pem)
    return hashlib.sha256(der).hexdigest()


def build_pinned_ssl_context(cert_pem: str) -> ssl.SSLContext:
    """An SSLContext whose only trusted CA is the node's own pinned
    certificate. check_hostname is disabled because a self-signed node
    certificate has no meaningful DNS name to match — the exact-certificate
    pin is what provides the security property here, not hostname matching."""
    context = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)
    context.check_hostname = False
    context.verify_mode = ssl.CERT_REQUIRED
    context.load_verify_locations(cadata=cert_pem)
    return context
