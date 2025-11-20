#!/bin/bash
# 交叉编译脚本：Mac -> Linux AMD64

echo "开始交叉编译 Linux AMD64 版本..."

cd "$(dirname "$0")"

# 设置交叉编译环境变量
export GOOS=linux
export GOARCH=amd64

# 编译
go build -mod=vendor -o test_read_linux_amd64 test_read.go

if [ $? -eq 0 ]; then
    echo "✓ 编译成功: test_read_linux_amd64"
    echo "文件大小: $(ls -lh test_read_linux_amd64 | awk '{print $5}')"
    echo ""
    echo "传输到 Linux 机器后，运行:"
    echo "  chmod +x test_read_linux_amd64"
    echo "  export KIMIK2_API_KEY='your_api_key'"
    echo "  ./test_read_linux_amd64"
else
    echo "✗ 编译失败"
    exit 1
fi

