"""材料解析（MVP：仅 txt / md 纯文本）。

parse_file 返回解析后的纯文本；任何失败一律返回 None，由调用方返回 400，
从而保证"解析失败不产生脏数据"。
"""


def _decode(raw_bytes):
    for encoding in ("utf-8", "gbk"):
        try:
            return raw_bytes.decode(encoding)
        except UnicodeDecodeError:
            continue
    return None


def parse_file(filename, raw_bytes):
    ext = filename.rsplit(".", 1)[-1].lower() if "." in filename else ""
    if ext not in {"txt", "md"}:
        return None
    text = _decode(raw_bytes)
    if text is None:
        return None
    text = text.strip()
    return text or None
