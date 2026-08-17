# BENZHI_README

## 项目说明

- 项目：11DingKing/go-ecfc00-t018-04
- 项目用途：一个用 Go 实现的巡护调度后端，覆盖保护站站长、巡护员、调度员三方协作的完整业务闭环：周计划工单派发、卡口扫码签到与装备申领、火情/病虫害事件上报与闭环处置、巡护轨迹核验，以及无信号场景下的离线暂存与恢复补传。
- Go 工具链：`golang:1.26`
- 前端工具链：无

## 标准构建、运行和测试命令

进入容器后执行：

```bash
# 编译
cd '/app' && GOTOOLCHAIN=local go build ./...

# 启动
cd '/app' && GOTOOLCHAIN=local go run ./cmd/server

# 测试
cd '/app' && GOTOOLCHAIN=local go test ./...
```

## Docker 构建和进入容器

```bash
chmod +x build_benzhi_docker.sh
./build_benzhi_docker.sh benzhi-task-74-amd64 linux/amd64
./build_benzhi_docker.sh benzhi-task-74-arm64 linux/arm64
docker run -it benzhi-task-74-amd64:latest
docker run -it --platform linux/arm64 benzhi-task-74-arm64:latest
```

## 题目验证命令

1. 预期退出码 0：`go test -race ./internal/sync -run "^TestConcurrentTicksReportEverySyncedRecord$" -count=1 -v`
2. 预期退出码 0：`go test -buildvcs=false -count=1 ./...`
3. 预期退出码 0：`GOTOOLCHAIN=local go build -buildvcs=false ./... && GOTOOLCHAIN=local go vet ./...`

## Bug 复现

Bug 现象、触发步骤和完整错误信息见 `BUG_REPRO.md`。
