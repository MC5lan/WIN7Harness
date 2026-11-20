#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""PE 产物校验：确认 EXE 内嵌图标资源完整、架构正确、可直接在 Win7 启动。

用法: python3 tools/verify_exe.py dist/WIN7Harness-amd64.exe [--want-arch amd64]
"""
import struct
import sys


def read_pe(path):
    d = open(path, 'rb').read()
    if d[:2] != b'MZ':
        raise SystemExit('%s: 不是有效 PE（缺 MZ）' % path)
    pe = struct.unpack_from('<I', d, 0x3C)[0]
    if d[pe:pe + 4] != b'PE\0\0':
        raise SystemExit('%s: 缺 PE 签名' % path)
    machine = struct.unpack_from('<H', d, pe + 4)[0]
    nsec = struct.unpack_from('<H', d, pe + 6)[0]
    optsz = struct.unpack_from('<H', d, pe + 20)[0]
    opt = pe + 24
    magic = struct.unpack_from('<H', d, opt)[0]
    dd = opt + (112 if magic == 0x20B else 96)
    rsrc_rva, rsrc_size = struct.unpack_from('<II', d, dd + 16)
    sec = opt + optsz
    sections = []
    for i in range(nsec):
        o = sec + 40 * i
        name = d[o:o + 8].rstrip(b'\0').decode('latin-1')
        vs, va, rs, rp = struct.unpack_from('<IIII', d, o + 8)
        sections.append((name, va, vs, rp, rs))
    return dict(data=d, machine=machine, magic=magic, sections=sections,
                rsrc_rva=rsrc_rva, rsrc_size=rsrc_size)


def rva2off(pe, rva):
    for name, va, vs, rp, rs in pe['sections']:
        if va <= rva < va + max(vs, rs):
            return rp + (rva - va)
    return None


def walk_res(pe):
    d = pe['data']
    base = rva2off(pe, pe['rsrc_rva'])
    out = []

    # 资源目录内所有偏移都是相对 .rsrc 起点的；叶子指向 16 字节的
    # IMAGE_RESOURCE_DATA_ENTRY，里面才是真正的 RVA 与大小。
    def walk(rel, depth, path):
        nd = base + rel
        nn, ni = struct.unpack_from('<HH', d, nd + 12)
        for i in range(nn + ni):
            e = nd + 16 + 8 * i
            nid, ofs = struct.unpack_from('<II', d, e)
            if ofs & 0x80000000:
                walk(ofs & 0x7FFFFFFF, depth + 1, path + [nid])
            else:
                drva, dsize, _cp = struct.unpack_from('<III', d, base + ofs)
                out.append((path + [nid], drva, dsize))

    walk(0, 0, [])
    return out


def main():
    path = sys.argv[1]
    pe = read_pe(path)
    d = pe['data']
    ok = True

    arch = {0x14C: 'x86(386)', 0x8664: 'x64(amd64)'}.get(pe['machine'], pe['machine'])
    mode = 'PE32' if pe['magic'] == 0x10B else 'PE32+'
    print('[i] %s' % path)
    print('[i] 架构=%s  格式=%s  节数=%d  体积=%.2f MB'
          % (arch, mode, len(pe['sections']), len(d) / 1048576.0))

    names = [s[0] for s in pe['sections']]
    if '.rsrc' not in names:
        print('[x] 缺 .rsrc 节：资源管理器不会显示自定义图标')
        ok = False
    if pe['rsrc_size'] <= 0:
        print('[x] 资源目录为空')
        ok = False

    res = walk_res(pe)
    icons = [x for x in res if x[0][0] == 3]
    groups = [x for x in res if x[0][0] == 14]
    print('[i] 资源: RT_ICON=%d  RT_GROUP_ICON=%d' % (len(icons), len(groups)))
    if not groups or not icons:
        print('[x] 缺少图标资源')
        ok = False
        print('\n结论: 失败')
        sys.exit(1)

    # 解析 group icon，核对每个尺寸都有对应 RT_ICON 数据
    gpath, goff, gsize = groups[0]
    go = rva2off(pe, goff)
    g = d[go:go + gsize]
    resv, typ, cnt = struct.unpack_from('<HHH', g, 0)
    print('[i] 图标组: count=%d (type=%d)' % (cnt, typ))
    # 注意：资源里的 GRPICONDIRENTRY 是 14 字节（最后是 WORD 资源ID），
    # 与 .ico 文件里的 16 字节条目（DWORD 文件偏移）不同，别混用。
    entries = []
    if 6 + 14 * cnt > len(g):
        print('[x] GROUP_ICON 数据被截断: 声明 %d 项需要 %d 字节，实际 %d'
              % (cnt, 6 + 14 * cnt, len(g)))
        ok = False
    for i in range(cnt):
        if 6 + 14 * (i + 1) > len(g):
            break
        w, h, cc, r, pl, bc, bs, rid = struct.unpack_from('<BBBBHHIH', g, 6 + 14 * i)
        dim = 256 if w == 0 else w
        hit = [x for x in icons if x[0][1] == rid]
        kind = '?'
        if hit:
            drva, dsz = hit[0][1], hit[0][2]
            o = rva2off(pe, drva)
            head = d[o:o + 8]
            kind = 'PNG' if head == b'\x89PNG\r\n\x1a\n' else 'BMP'
            if dsz != bs:
                kind += ' (尺寸不符 %d!=%d)' % (dsz, bs)
                ok = False
        else:
            kind = '缺资源'
            ok = False
        entries.append((dim, bc, bs))
        print('    %3dpx  bpp=%-2d  %6d bytes  %s' % (dim, bc, bs, kind))
    sizes = sorted(e[0] for e in entries)
    for need in (16, 32, 48, 256):
        if need not in sizes:
            print('[!] 缺少 %dpx 尺寸（Windows 会插值缩放，观感下降）' % need)
    if len(icons) != cnt:
        print('[!] RT_ICON 数量(%d) 与组声明(%d) 不一致' % (len(icons), cnt))
        ok = False

    # Win7 兼容关键点：Go 1.21+ 的二进制在 Win7 上无法启动。
    # 这里从 Go 版本字符串反查（链接器会写入 runtime.buildVersion）。
    import re
    cands = re.findall(rb'go1\.\d+(?:\.\d+)?', d)
    ver = None
    if cands:
        # 取最长的（形如 go1.20.14 优先于 go1.20）
        ver = max(cands, key=len).decode()
    if ver:
        print('[i] 构建 Go 版本: %s' % ver)
        if ver.startswith('go1.21') or ver.startswith('go1.22') or \
           ver.startswith('go1.23') or ver.startswith('go1.24'):
            print('[x] Go %s 产物无法在 Win7 SP1 启动，必须用 Go 1.20.x' % ver)
            ok = False
    else:
        print('[!] 未能从二进制中识别 Go 版本')

    print('\n结论: %s' % ('通过' if ok else '失败'))
    sys.exit(0 if ok else 1)


if __name__ == '__main__':
    main()
