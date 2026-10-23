"""Filesystem spelling for owned validation artifacts on Windows.

Extended paths let Python retain deep fixtures in an immutable source copy.
They do not change the destination, permissions, or machine configuration.
Keep ordinary paths in subprocess arguments and human-readable reports.
"""
import os
from pathlib import Path


def filesystem_path(path):
    path = Path(path).absolute()
    if os.name != "nt":
        return path
    value = str(path)
    if value.startswith("\\\\?\\"):
        return path
    if value.startswith("\\\\"):
        return Path("\\\\?\\UNC\\" + value[2:])
    return Path("\\\\?\\" + value)
