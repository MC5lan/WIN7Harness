# 第三方组件与许可 / Third-Party Notices

WIN7 Harness 使用 Go 模块构建。发布二进制时，下列组件的代码会被静态链接进 exe。
源码仓库的 `go.mod` / `go.sum` 固定了版本；完整许可文本可在对应上游仓库或
Go module cache（`$(go env GOMODCACHE)`）中查看。

## 直接依赖

| 模块 | 版本 | 许可 |
|---|---|---|
| `github.com/alexbrainman/sspi` | v0.0.0-20250919150558-7d374ff0d59e | BSD-3-Clause |
| `github.com/tdewolff/canvas` | v0.0.0-20231121141746-0503a63df052 | MIT |
| `go.etcd.io/bbolt` | v1.3.8 | MIT |
| `golang.org/x/image` | v0.13.0 | BSD-3-Clause |
| `golang.org/x/net` | v0.17.0 | BSD-3-Clause |
| `golang.org/x/text` | v0.13.0 | BSD-3-Clause |

间接依赖（字体排版、压缩、图像解码、TeX 解析等）见 `go.mod` 的 `require` 块；
均为 MIT / BSD / Apache-2.0 等宽松许可。发布时请保留本文件和上游版权声明。

## 构建工具

- **Inno Setup 6.7.x**：用于生成 Windows 安装包，不随源码包分发。
  Copyright (C) 1997-2026 Jordan Russell；Portions Copyright (C) 2000-2026 Martijn Laan。
  许可见 <https://jrsoftware.org/isinfo.php>。
- **简体中文语言文件** `build/installer-lang/ChineseSimplified.isl`：
  来自 Inno Setup 官方翻译（维护者 Zhenghan Yang），随安装器构建使用。
- **rsrc**：把图标嵌入 PE 的构建工具，不随源码包分发。
- **Go 1.20.14**：构建工具链，不随源码包分发；Go 自身使用 BSD-3-Clause。

## 字体

`_render/fonts/NotoSansSC-Regular.otf` 仅用于开发期渲染核对，**不包含在发布源码包中**。
如你从历史副本中取得该字体，它使用 SIL Open Font License 1.1，版权归 Google/Noto 项目所有。

## 根证书

`internal/llm` 内置的根证书是公开 CA 的根证书，用于 Win7 老系统 TLS 兜底；
证书本身不是代码，版权/使用条款归各 CA 所有。

## MIT License 文本

```text
Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

## BSD-3-Clause 文本

```text
Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are
met:

   * Redistributions of source code must retain the above copyright
notice, this list of conditions and the following disclaimer.
   * Redistributions in binary form must reproduce the above
copyright notice, this list of conditions and the following disclaimer
in the documentation and/or other materials provided with the
distribution.
   * Neither the name of the copyright holder nor the names of its
contributors may be used to endorse or promote products derived from
this software without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS
"AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT
LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR
A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT
OWNER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL,
SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT
LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE,
DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY
THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
(INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
```
