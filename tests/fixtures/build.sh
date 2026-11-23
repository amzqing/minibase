#!/bin/sh
# 重建测试用的 zip 包。纯 Node 内置模块，无需联网装依赖。
set -e
cd "$(dirname "$0")"
for name in hello bad echo alloc hang delayhang; do
  rm -f "$name.zip"
  (cd "src/$name" && zip -qr "../../$name.zip" .)
done
echo "已生成 hello.zip bad.zip echo.zip alloc.zip"
