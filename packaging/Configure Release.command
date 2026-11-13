#!/bin/bash
# Run by the user in the Mac's own Terminal so macOS can present authorization.
set -euo pipefail
cd "$(dirname "$0")/.."
trap 'printf "\n按回车关闭此窗口。"; read -r reply' EXIT
printf 'Shioji GitHub 发布配置\n目标仓库：senshinya/shioji\n\n先允许系统导出选定的开发者证书；随后录入 Apple 公证专用密码。\n密码不会显示，也不会写入源码或聊天。\n\n'
python3 scripts/configure-github-release.py certificate
python3 scripts/configure-github-release.py notarization
printf '\n配置已完成。GitHub Release 发布后将自动生成签名并公证的 DMG。\n'
