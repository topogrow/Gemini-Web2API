#!/bin/bash
set -e

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$REPO_DIR"

echo "=========================================================="
echo "          Gemini-Web2API 上游更新与自动构建脚本           "
echo "=========================================================="

# 1. 检查本地工作区是否干净
if [ -n "$(git status --porcelain)" ]; then
    echo "❌ 错误: 本地工作区存在未提交的修改，请先提交或 stash 后再执行更新。"
    git status -s
    exit 1
fi

# 2. 检查 upstream 远端是否存在
if ! git remote get-url upstream >/dev/null 2>&1; then
    echo "⚠️ 未找到 upstream 远端，正在自动关联官方仓库..."
    git remote add upstream https://github.com/Mag1cFall/Gemini-Web2API.git
fi

# 3. 拉取官方最新提交
echo "📥 正在拉取官方仓库最新代码 (upstream/main)..."
git fetch upstream main

CURRENT_BRANCH=$(git branch --show-current)
echo "当前所在分支: $CURRENT_BRANCH"

# 4. 执行变基 (Rebase)
echo "🔄 正在将你的二开分支 rebase 到官方最新主干之上..."
if ! git rebase upstream/main; then
    echo ""
    echo "⚠️ 检测到代码合并冲突，Rebase 已暂停。"
    echo "请在编辑器中解决冲突文件，然后执行:"
    echo "   git add <文件>"
    echo "   git rebase --continue"
    exit 1
fi

echo "✅ Git 代码变基成功！你的二开特性已平滑垫在官方最新版之上。"
echo ""

# 5. 自动构建前端
echo "🔨 正在重新编译前端资源..."
cd "$REPO_DIR/web"
pnpm run build
cd "$REPO_DIR"

# 6. 自动构建 Go 桌面应用
echo "🔨 正在重新编译 macOS 通用二进制..."
CGO_ENABLED=1 go build -tags desktop,production -ldflags="-s -w" -o bin/gemini-web2api ./cmd/gemini-web2api

# 7. 覆盖安装到系统 Applications
echo "📦 正在更新安装到 /Applications/Gemini-Web2API.app..."
pkill -9 -f "gemini-web2api" 2>/dev/null || true
sleep 1
cp bin/gemini-web2api "/Applications/Gemini-Web2API.app/Contents/MacOS/gemini-web2api"
codesign --force --deep --sign - "/Applications/Gemini-Web2API.app"

echo ""
echo "=========================================================="
echo "🎉 恭喜！官方更新已无缝同步，且应用已成功重新编译并安装！"
echo "你可以直接双击 /Applications/Gemini-Web2API.app 启动使用。"
echo "=========================================================="
