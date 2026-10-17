#!/bin/bash
set -e

ACCOUNT_ID="$1"
if [ -z "$ACCOUNT_ID" ]; then
    echo "用法: ./login_account.sh <账号ID>"
    echo "例如: ./login_account.sh c4993764-gmail-com"
    exit 1
fi

PROFILE_DIR="$HOME/Library/Application Support/Gemini-Web2API/profiles/$ACCOUNT_ID"
mkdir -p "$PROFILE_DIR"

PROXY="http://127.0.0.1:7897"
CHROME="/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"

echo "=========================================================="
echo "正在为账号 [$ACCOUNT_ID] 启动专属独立的 Chrome 窗口..."
echo "Profile 目录: $PROFILE_DIR"
echo "代理地址: $PROXY"
echo "请在弹出的浏览器中登录对应的 Google 账号。"
echo "登录成功后，Google 会自动保存凭据至该 Profile，关闭浏览器即可！"
echo "=========================================================="

"$CHROME" \
    --user-data-dir="$PROFILE_DIR" \
    --proxy-server="$PROXY" \
    --no-first-run \
    --no-default-browser-check \
    --window-size=1280,800 \
    "https://gemini.google.com/app"
