#!/usr/bin/env python3
"""Update YourLM's COFF version resource without platform-specific tools.

The committed .syso layout is deterministic. RC9 -> RC10 grows two UTF-16
version strings by one code unit, so this parser rebuilds the version tree and
shifts the following manifest resource while preserving the COFF metadata.
"""
from __future__ import annotations

import json
import struct
import sys
from pathlib import Path

ALIGN = lambda value: (value + 3) & ~3


def read_node(data: bytes, start: int = 0) -> dict:
    length, value_length, kind = struct.unpack_from("<HHH", data, start)
    cursor = start + 6
    chars: list[str] = []
    while True:
        code = struct.unpack_from("<H", data, cursor)[0]
        cursor += 2
        if code == 0:
            break
        chars.append(chr(code))
    value_start = ALIGN(cursor)
    value_size = value_length * 2 if kind == 1 else value_length
    value = data[value_start : value_start + value_size]
    child_start = ALIGN(value_start + value_size)
    children: list[dict] = []
    cursor = child_start
    while cursor < start + length:
        child_length = struct.unpack_from("<H", data, cursor)[0]
        if child_length == 0:
            break
        children.append(read_node(data, cursor))
        cursor = ALIGN(cursor + child_length)
    return {"key": "".join(chars), "kind": kind, "value": value, "children": children}


def pack_node(node: dict) -> bytes:
    key = node["key"].encode("utf-16le") + b"\0\0"
    header = bytearray(struct.pack("<HHH", 0, 0, node["kind"]) + key)
    header += b"\0" * (ALIGN(len(header)) - len(header))
    value = node["value"]
    header += value
    header += b"\0" * (ALIGN(len(header)) - len(header))
    for child in node["children"]:
        header += pack_node(child)
    value_length = len(value) // 2 if node["kind"] == 1 else len(value)
    struct.pack_into("<HH", header, 0, len(header), value_length)
    return bytes(header)


def main() -> None:
    if len(sys.argv) != 4:
        raise SystemExit("usage: patch-windows-resource.py INPUT VERSIONINFO_JSON OUTPUT")
    source, spec_path, output = map(Path, sys.argv[1:])
    raw = bytearray(source.read_bytes())
    spec = json.loads(spec_path.read_text())

    section_raw_size, section_raw_offset, reloc_offset = struct.unpack_from("<LLL", raw, 36)
    section = bytearray(raw[section_raw_offset : section_raw_offset + section_raw_size])
    version_offset, old_version_size = struct.unpack_from("<LL", section, 0x80)
    manifest_offset, manifest_size = struct.unpack_from("<LL", section, 0x90)
    root = read_node(section[version_offset : version_offset + old_version_size])

    fixed = root["value"]
    file_build = spec["FixedFileInfo"]["FileVersion"]["Build"]
    product_build = spec["FixedFileInfo"]["ProductVersion"]["Build"]
    fixed = bytearray(fixed)
    struct.pack_into("<L", fixed, 12, file_build)
    struct.pack_into("<L", fixed, 20, product_build)
    root["value"] = bytes(fixed)

    strings = spec["StringFileInfo"]
    table = root["children"][0]["children"][0]
    for entry in table["children"]:
        if entry["key"] in strings:
            entry["value"] = strings[entry["key"]].encode("utf-16le") + b"\0\0"
    version = pack_node(root)
    manifest = bytes(section[manifest_offset : manifest_offset + manifest_size])
    new_manifest_offset = (version_offset + len(version) + 7) & ~7
    rebuilt = bytearray(section[:version_offset])
    rebuilt += version
    rebuilt += b"\0" * (new_manifest_offset - len(rebuilt))
    rebuilt += manifest
    rebuilt += b"\0" * (ALIGN(len(rebuilt)) - len(rebuilt))

    struct.pack_into("<LL", rebuilt, 0x80, version_offset, len(version))
    struct.pack_into("<LL", rebuilt, 0x90, new_manifest_offset, manifest_size)
    delta = len(rebuilt) - section_raw_size
    symbol_offset = struct.unpack_from("<L", raw, 8)[0]
    struct.pack_into("<L", raw, 8, symbol_offset + delta)
    struct.pack_into("<L", raw, 36, len(rebuilt))
    struct.pack_into("<L", raw, 44, reloc_offset + delta)
    raw[section_raw_offset : section_raw_offset + section_raw_size] = rebuilt
    output.write_bytes(raw)


if __name__ == "__main__":
    main()
