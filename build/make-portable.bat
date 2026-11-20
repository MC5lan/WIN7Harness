@echo off
chcp 936 >nul
cd /d "%~dp0"
if exist portable.flag goto revert

echo.> "portable.flag"
echo [已切换] 便携模式：配置、会话、快照都保存在本目录的 data 文件夹。
echo 换台机器时整个文件夹拷走即可，U盘即插即用。
goto done

:revert
del /q "portable.flag"
echo [已切回] 标准模式：数据保存在 %%APPDATA%%\WIN7Harness。
echo 本目录 data 里的内容不会自动搬回去，需要的话手动复制。

:done
echo.
echo 请重新启动 WIN7 Harness 使设置生效。
pause
