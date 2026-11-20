#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""WIN7 Harness 代码卫生门禁（CI / 提交前自检）

两类规则：

forbidden（exit 1）：
  1. 禁止 Win8+ API —— 违反 Win7 SP1 兼容红线（本项目硬约束）
     SetProcessDpiAwareness / DwmSetWindowAttribute /
     SetThreadDpiAwarenessContext / DirectCompositionCreateObject /
     CreateDXGIFactory / IDCompositionDevice
  2. unsafe 只准出现在 Win32/GDI 互操作文件里（internal/win32、internal/ui）

review（默认警告，--strict 时 exit 1）：
  3. os.WriteFile / os.Create / ioutil.WriteFile 的非测试、非白名单文件
     ——崩坏时写半截文件，鼓励改用 core.WriteFileAtomic

用法：
  python scripts/check.py            # 警告+必败检查都跑
  python scripts/check.py --strict   # review 项也当错误处理
"""
import os
import re
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

FORBIDDEN = [
    # Win8+ 显示/DPI API 禁用，Win7 SP1 不支持
    (r'\bSetProcessDpiAwareness\b', 'Win8+ DPI API，Win7 SP1 不支持（含 d3dcompiler）'),
    (r'\bDwmSetWindowAttribute\b', 'Win8+ DWM 属性 API，Win7 SP1 无此能力'),
    (r'\bSetThreadDpiAwarenessContext\b', 'Win10+ DPI 感知 API'),
    (r'\bDirectCompositionCreateObject\b', 'Win8+ DirectComposition'),
    (r'\bIDCompositionDevice\b', 'Win8+ DirectComposition'),
    (r'\bIDCompositionTarget\b', 'Win8+ DirectComposition'),
    (r'\bCreateDXGIFactory\b', 'd3dcompiler_47 依赖（Win7 不自带）'),
    (r'\bIDirect3D11Device\b', 'D3D11 硬件（Gio/Ebiten 路线已否决）'),
]

ALLOWED_UNSAFE_DIRS = ('internal/win32/', 'internal/ui/')
ALLOWED_ATOMIC_FILES = {
    'internal/core/atomic.go',  # 实现 WriteFileAtomic 自身
    # 流式写入场景：WriteFileAtomic 要求完整 []byte，无法适用；
    # 这两处要么整文件复制（io.Copy），要么边生成边压缩（zip.NewWriter），
    # 中间文件一旦失败都有显式 Close 错误处理。
    'internal/agent/parallel.go',
    'internal/core/diag.go',
}


def unsafe_allowed(rp):
    if any(rp.startswith(d) for d in ALLOWED_UNSAFE_DIRS):
        return True
    # Windows 专属的 Win32 互操作文件（build 标签 //go:build windows）
    if os.path.basename(rp).endswith('_windows.go'):
        return True
    # 测试里用于结构布局断言
    if is_test_file(rp):
        return True
    return False

WRITE_RE = re.compile(r'\b(?:os\.WriteFile|os\.Create|ioutil\.WriteFile)\s*\(')


def rel(path):
    return os.path.relpath(path, ROOT).replace(os.sep, '/')


def is_test_file(path):
    return os.path.basename(path).endswith('_test.go')


def iter_go_files():
    for dp, dn, fn in os.walk(ROOT):
        if '/.git' in dp or '\\.git' in dp:
            dn[:] = []
            continue
        for n in fn:
            if n.endswith('.go'):
                yield os.path.join(dp, n)


def main():
    strict = '--strict' in sys.argv[1:]
    forbidden_hits = []
    review_hits = []

    for f in iter_go_files():
        rp = rel(f)
        try:
            text = open(f, encoding='utf-8').read()
        except (UnicodeDecodeError, OSError):
            print('警告：无法读取 %s（跳过）' % rp)
            continue

        # 1. 禁止 API（任何文件都禁止，含注释？不：只接受 Go 代码里的标识符）
        for pat, why in FORBIDDEN:
            for m in re.finditer(pat, text):
                forbidden_hits.append((rp, why))

        # 2. unsafe 只允许在 Win32/GDI 互操作域内（Directory + *_windows.go + *_test.go）
        if 'unsafe' not in text:
            pass
        elif not unsafe_allowed(rp):
            if re.search(r'\bunsafe\s*\.', text):
                forbidden_hits.append((rp, 'unsafe 只允许出现在 Win32 互操作文件（internal/win32、internal/ui、*_windows.go、*_test.go）'))

        # 3. 非测试文件直接 os.WriteFile：列为 review
        #    internal/selftest 的 fixture 允许（测试沙箱里的一次性写入，与用户数据无关）
        if not is_test_file(rp) and rp not in ALLOWED_ATOMIC_FILES and '/selftest/' not in rp and '/agent/parallel_fixture.go' != rp:
            for m in WRITE_RE.finditer(text):
                # 允许从注释里出现的名字（例如 atomic.go 自身与注释中的解释）
                line_start = text.rfind('\n', 0, m.start()) + 1
                line = text[line_start:m.start()]
                if line.lstrip().startswith('//'):
                    continue
                review_hits.append((rp, '非原子写入（建议 core.WriteFileAtomic）'))

    if review_hits:
        print('review 项（非阻断，除非 --strict）：')
        for rp, msg in review_hits:
            print('  %s  %s' % (rp, msg))
        print('共 %d 处' % len(review_hits))

    if forbidden_hits:
        print('\nforbidden 项（必须修）：')
        for rp, why in forbidden_hits:
            print('  %s  %s' % (rp, why))
        return 1
    print('forbidden 检查通过')
    if strict and review_hits:
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main())
