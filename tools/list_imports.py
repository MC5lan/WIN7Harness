#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""列出 PE 的 DLL 导入表，用于人工核对 Win7 兼容性。

用法: python3 tools/list_imports.py dist/WIN7Harness-amd64.exe
"""
import struct
import sys

# Win7 SP1 上不存在（或需要额外补丁）的常见 API，命中即提示。
WIN7_MISSING = {
    'SetProcessDpiAwarenessContext': 'Win10 1607+',
    'GetDpiForWindow': 'Win10 1607+',
    'SetThreadDpiAwarenessContext': 'Win10 1607+',
    'GetSystemMetricsForDpi': 'Win10 1607+',
    'AdjustWindowRectExForDpi': 'Win10 1607+',
    'GetDpiForMonitor': 'Win8.1+',
    'GetDpiForSystem': 'Win10 1607+',
    'EnableNonClientDpiScaling': 'Win10 1607+',
    'SetProcessDpiAwareness': 'Win8.1+ (shcore)',
    'GetSystemTimePreciseAsFileTime': 'Win8+',
    'SetThreadDescription': 'Win10 1607+',
    'AddDllDirectory': 'Win7 需 KB2533623',
    'SetDefaultDllDirectories': 'Win7 需 KB2533623',
    'RemoveDllDirectory': 'Win7 需 KB2533623',
    'DiscardVirtualMemory': 'Win8.1+',
    'PrefetchVirtualMemory': 'Win8+',
    'CreateFile2': 'Win8+',
    'WaitOnAddress': 'Win8+',
    'GetSystemCpuSetInformation': 'Win10+',
    'VirtualAlloc2': 'Win10 2004+',
    'MapViewOfFile3': 'Win10 1803+',
    'CreatePseudoConsole': 'Win10 1809+',
    'RtlGetVersion': '',
    'DwmSetWindowAttribute': 'Vista+',
    'SetWindowCompositionAttribute': 'Win7 无（Win8+）',
}


def main():
    path = sys.argv[1]
    d = open(path, 'rb').read()
    pe = struct.unpack_from('<I', d, 0x3C)[0]
    nsec = struct.unpack_from('<H', d, pe + 6)[0]
    optsz = struct.unpack_from('<H', d, pe + 20)[0]
    opt = pe + 24
    magic = struct.unpack_from('<H', d, opt)[0]
    is64 = magic == 0x20B
    dd = opt + (112 if is64 else 96)
    imp_rva, imp_sz = struct.unpack_from('<II', d, dd + 8)
    sec = opt + optsz
    sections = []
    for i in range(nsec):
        o = sec + 40 * i
        vs, va, rs, rp = struct.unpack_from('<IIII', d, o + 8)
        sections.append((va, vs, rp, rs))

    def r2o(rva):
        for va, vs, rp, rs in sections:
            if va <= rva < va + max(vs, rs):
                return rp + (rva - va)
        return None

    print('=== %s ===' % path)
    total = 0
    hits = []
    off = r2o(imp_rva)
    thunk_sz = 8 if is64 else 4
    while True:
        ent = d[off:off + 20]
        ilt, ts, fc, name_rva, iat = struct.unpack('<IIIII', ent)
        if name_rva == 0:
            break
        dll = d[r2o(name_rva):].split(b'\0')[0].decode('latin-1')
        funcs = []
        t = r2o(ilt if ilt else iat)
        while True:
            v = struct.unpack_from('<Q' if is64 else '<I', d, t)[0]
            if v == 0:
                break
            if v & (1 << (63 if is64 else 31)):
                funcs.append('Ordinal#%d' % (v & 0xFFFF))
            else:
                fo = r2o((v & 0x7FFFFFFF) + 2)
                fn = d[fo:].split(b'\0')[0].decode('latin-1')
                funcs.append(fn)
                if fn in WIN7_MISSING:
                    hits.append((dll, fn, WIN7_MISSING[fn]))
            t += thunk_sz
        total += len(funcs)
        print('\n[%s]  %d 个' % (dll, len(funcs)))
        for i in range(0, len(funcs), 4):
            print('   ' + '  '.join('%-34s' % f for f in funcs[i:i + 4]))
        off += 20

    print('\n共导入 %d 个函数' % total)
    if hits:
        print('\n!! Win7 兼容性风险：')
        for dll, fn, why in hits:
            print('   %s!%s  (%s)' % (dll, fn, why))
    else:
        print('未发现 Win7 上缺失的 API')


if __name__ == '__main__':
    main()
