# pb/gen 目录说明

该目录用于存放 `proto/` 生成的 Go 绑定代码。

生成命令：

```bash
cd ltfp
make proto
```

如果本地缺少工具链，请先安装：

```bash
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
```
