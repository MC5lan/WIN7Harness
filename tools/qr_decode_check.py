#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""用 zxing-cpp（真实扫码器）解码 Go 端生成的二维码，验证可扫且内容一致。

用法：python3 qr_decode_check.py <qrdump> [用例数]
"""
import random
import string
import subprocess
import sys

from PIL import Image
import zxingcpp


def render(rows, scale=8, quiet=4):
    n = len(rows)
    w = (n + quiet * 2) * scale
    img = Image.new("L", (w, w), 255)
    px = img.load()
    for y, row in enumerate(rows):
        for x, ch in enumerate(row):
            if ch == "1":
                for dy in range(scale):
                    for dx in range(scale):
                        px[(x + quiet) * scale + dx, (y + quiet) * scale + dy] = 0
    return img


def go_matrix(binary, text):
    out = subprocess.run([binary, text], capture_output=True, check=True)
    lines = out.stdout.decode().strip().split("\n")
    head = lines[0]
    ver = int(head.split("version=")[1].split()[0])
    mask = int(head.split("mask=")[1].split()[0])
    return ver, mask, lines[1:]


def main():
    binary = sys.argv[1]
    cases = int(sys.argv[2]) if len(sys.argv) > 2 else 10
    random.seed(7)
    samples = [
        "http://192.168.1.7:8765/p?c=123456",
        "A",
        "http://127.0.0.1:8765/pair?code=000000",
    ]
    alphabet = string.ascii_letters + string.digits + ":/?=&._-"
    for _ in range(cases):
        samples.append("".join(random.choice(alphabet) for _ in range(random.randint(1, 200))))

    bad = 0
    for text in samples:
        ver, mask, rows = go_matrix(binary, text)
        img = render(rows)
        res = zxingcpp.read_barcodes(img)
        if not res:
            print("[FAIL] 无法解码 ver=%d mask=%d len=%d" % (ver, mask, len(text)))
            bad += 1
            continue
        got = res[0].text
        if got != text:
            print("[FAIL] 内容不符 ver=%d mask=%d\n  期望 %.40s\n  实得 %.40s" % (ver, mask, text, got))
            bad += 1
        else:
            print("[OK] ver=%d mask=%d len=%d" % (ver, mask, len(text)))
    print("== 共 %d 例，失败 %d 例" % (len(samples), bad))
    sys.exit(1 if bad else 0)


if __name__ == "__main__":
    main()
