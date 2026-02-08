#!/usr/bin/env python3
"""
Generate age encryption key from passphrase.
Matches the Go implementation in claude-sync for cross-device compatibility.

Usage: python3 generate-key.py <passphrase> <output_path>
"""

import sys
import os
import hashlib

try:
    from argon2.low_level import hash_secret_raw, Type
except ImportError:
    print("Error: argon2-cffi package required. Install with: pip3 install argon2-cffi", file=sys.stderr)
    sys.exit(1)


def bech32_polymod(values: list[int]) -> int:
    """Internal function that computes the Bech32 checksum."""
    generator = [0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3]
    chk = 1
    for value in values:
        top = chk >> 25
        chk = (chk & 0x1ffffff) << 5 ^ value
        for i in range(5):
            chk ^= generator[i] if ((top >> i) & 1) else 0
    return chk


def bech32_hrp_expand(hrp: str) -> list[int]:
    """Expand the HRP into values for checksum computation."""
    return [ord(x) >> 5 for x in hrp] + [0] + [ord(x) & 31 for x in hrp]


def bech32_create_checksum(hrp: str, data: list[int]) -> list[int]:
    """Compute the checksum values given HRP and data."""
    values = bech32_hrp_expand(hrp) + data
    polymod = bech32_polymod(values + [0, 0, 0, 0, 0, 0]) ^ 1
    return [(polymod >> 5 * (5 - i)) & 31 for i in range(6)]


def convert_bits(data: bytes, from_bits: int, to_bits: int, pad: bool = True) -> list[int]:
    """Convert between bit sizes."""
    acc = 0
    bits = 0
    result = []
    maxv = (1 << to_bits) - 1
    max_acc = (1 << (from_bits + to_bits - 1)) - 1
    for value in data:
        acc = ((acc << from_bits) | value) & max_acc
        bits += from_bits
        while bits >= to_bits:
            bits -= to_bits
            result.append((acc >> bits) & maxv)
    if pad:
        if bits:
            result.append((acc << (to_bits - bits)) & maxv)
    elif bits >= from_bits or ((acc << (to_bits - bits)) & maxv):
        return []
    return result


def bech32_encode(hrp: str, data: list[int]) -> str:
    """Encode a Bech32 string."""
    charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"
    combined = data + bech32_create_checksum(hrp, data)
    return hrp + "1" + "".join([charset[d] for d in combined])


def generate_key_from_passphrase(passphrase: str) -> str:
    """
    Derive a deterministic age X25519 identity from a passphrase.
    Matches the Go implementation in claude-sync.
    """
    # Use fixed salt derived from "claude-sync-v1"
    salt = hashlib.sha256(b"claude-sync-v1").digest()

    # Derive 32 bytes using Argon2id
    # Parameters: 64MB memory, 3 iterations, 4 threads
    key = hash_secret_raw(
        secret=passphrase.encode("utf-8"),
        salt=salt,
        time_cost=3,
        memory_cost=64 * 1024,
        parallelism=4,
        hash_len=32,
        type=Type.ID,
    )

    # Convert to mutable bytearray for clamping
    key = bytearray(key)

    # Clamp the scalar for X25519 (per RFC 7748)
    key[0] &= 248
    key[31] &= 127
    key[31] |= 64

    # Encode as age identity string (Bech32 with AGE-SECRET-KEY- prefix)
    hrp = "age-secret-key-"
    converted = convert_bits(bytes(key), 8, 5, True)
    encoded = bech32_encode(hrp, converted)

    # Age uses uppercase for secret keys
    return encoded.upper()


def main():
    if len(sys.argv) != 3:
        print("Usage: python3 generate-key.py <passphrase> <output_path>", file=sys.stderr)
        sys.exit(1)

    passphrase = sys.argv[1]
    output_path = sys.argv[2]

    if len(passphrase) < 8:
        print("Error: passphrase must be at least 8 characters", file=sys.stderr)
        sys.exit(1)

    identity = generate_key_from_passphrase(passphrase)

    # Ensure parent directory exists
    os.makedirs(os.path.dirname(output_path), exist_ok=True)

    # Write with restricted permissions
    with open(output_path, "w") as f:
        f.write(identity + "\n")
    os.chmod(output_path, 0o600)

    print(f"Key generated at {output_path}")


if __name__ == "__main__":
    main()
