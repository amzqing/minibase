#!/bin/sh
# 版本号自动迭代：根据最近的 tag 和新增提交算出下一个版本。
# 规则：BREAKING CHANGE → major；feat: → minor；fix: → patch；其他不变。
# 没有任何 tag 时，起始版本是 v0.0.1。
# 用法: version.sh next    输出下一个版本号
set -eu

LATEST=$(git tag -l 'v*' --sort=-v:refname | head -1)

if [ -z "$LATEST" ]; then
  echo "v0.0.1"
  exit 0
fi

VERSION=${LATEST#v}
MAJOR=$(echo "$VERSION" | cut -d. -f1)
MINOR=$(echo "$VERSION" | cut -d. -f2)
PATCH=$(echo "$VERSION" | cut -d. -f3)

# 只看上一个 tag 之后的新提交
COMMITS=$(git log "$LATEST..HEAD" --pretty=format:'%s%n%b' 2>/dev/null || true)

BUMP=none
if echo "$COMMITS" | grep -qiE 'BREAKING CHANGE|^[a-z]+(\(.+\))?!:'; then
  BUMP=major
elif echo "$COMMITS" | grep -qiE '^feat(\(.+\))?:'; then
  BUMP=minor
elif echo "$COMMITS" | grep -qiE '^fix(\(.+\))?:'; then
  BUMP=patch
fi

case "$BUMP" in
  major) echo "v$((MAJOR+1)).0.0" ;;
  minor) echo "v$MAJOR.$((MINOR+1)).0" ;;
  patch) echo "v$MAJOR.$MINOR.$((PATCH+1))" ;;
  *)     echo "$LATEST" ;;
esac
