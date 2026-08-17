# 祁连山华隆保护站巡护调度服务

一个用 Go 实现的巡护调度后端，覆盖保护站站长、巡护员、调度员三方协作的完整业务闭环：周计划工单派发、卡口扫码签到与装备申领、火情/病虫害事件上报与闭环处置、巡护轨迹核验，以及无信号场景下的离线暂存与恢复补传。

## 项目用途

围绕巡护网格、巡护装备、卡口登记点三类资源，实现四条核心流程：

1. **工单派发**：调度员按周计划生成巡护工单并派发（同一网格 24 小时内仅一张有效工单）。
2. **签到申领**：巡护员在卡口扫码签到后申领装备，进入核心区须双人结伴且提前报备。
3. **事件处置**：巡护途中上报火情或病虫害，调度员须在 30 分钟内响应指派并跟踪闭环。
4. **归还核验**：巡护结束归还装备（值班结束前完成并登记完好状态）并提交轨迹，偏离既定路线超过 200 米自动预警。

并发边界与失败恢复：

- 两名巡护员同时申领同一编号装备时，按报备优先级锁定，仅一人成功，另一人保留排队位次并提示更换。
- 装备锁定失败立即回滚库存占用，避免脏数据。
- 无网络信号时巡护记录与事件离线暂存，恢复后自动补传并逐条回执确认（幂等）。

## 技术栈与分层

| 层 | 包 | 职责 |
|----|----|------|
| HTTP 接入 | `internal/httpapi` | JSON 接口、路由（Go 1.22+ 方法模式 ServeMux）、错误码映射 |
| 应用编排 | `internal/app` | 用例编排、跨聚合业务规则、装备回滚、离线记录应用 |
| 领域状态 | `internal/domain` | 工单/装备/事件/轨迹状态机与校验、Haversine 偏航计算 |
| 持久化 | `internal/store` | 线程安全内存仓储、24 小时窗口与幂等约束、离线缓存 |
| 后台任务 | `internal/sync` | SLA 监控、离线补传、网络状态模拟 |
| 配置 | `internal/config` | JSON 配置加载、环境变量覆盖、校验 |
| 入口 | `cmd/server` | 启动、信号优雅退出、演示数据种子 |

全部使用 Go 标准库，无外部依赖，自包含可离线构建。

## 启动方式

```bash
go run ./cmd/server
# 或指定配置
go run ./cmd/server -config config.json
```

服务默认监听 `:50253`。

## 主要接口

| 方法 | 路径 | 说明 |
|------|------|------|
| GET  | `/healthz` | 健康检查 |
| POST | `/api/grids` | 创建巡护网格 |
| GET  | `/api/grids` | 列出网格 |
| POST | `/api/checkpoints` | 创建卡口登记点 |
| POST | `/api/equipment` | 录入装备 |
| GET  | `/api/equipment` | 列出装备 |
| POST | `/api/workorders` | 生成巡护工单（支持 `requestKey` 幂等） |
| GET  | `/api/workorders` | 列出工单 |
| GET  | `/api/workorders/{id}` | 查询工单 |
| POST | `/api/workorders/{id}/dispatch` | 派发（校验核心区双人+报备） |
| POST | `/api/workorders/{id}/checkin` | 卡口扫码签到 |
| POST | `/api/workorders/{id}/start` | 开始巡护 |
| POST | `/api/workorders/{id}/complete` | 完成巡护（须已归还装备） |
| POST | `/api/workorders/{id}/verify` | 核验轨迹 |
| POST | `/api/workorders/{id}/cancel` | 取消工单 |
| POST | `/api/equipment/{code}/claim` | 申领（按优先级锁定） |
| POST | `/api/equipment/{code}/issue` | 发放（失败回滚锁定） |
| POST | `/api/equipment/{code}/return` | 归还（校验值班结束前、登记状态） |
| POST | `/api/incidents` | 上报事件（`issueId` 幂等） |
| GET  | `/api/incidents` | 列出事件 |
| POST | `/api/incidents/{id}/assign` | 指派处置（超 30 分钟标记 SLA 违约） |
| POST | `/api/incidents/{id}/handle` | 开始处置 |
| POST | `/api/incidents/{id}/close` | 闭环 |
| POST | `/api/tracks` | 提交轨迹（偏航>200m 预警） |
| GET  | `/api/tracks/{workOrderId}` | 查询轨迹 |
| POST | `/api/sync/offline` | 提交离线记录批量 |
| GET  | `/api/sync/status` | 同步与 SLA 状态 |
| POST | `/api/sync/online` | 切换网络状态（恢复即补传） |

## 端口

默认 `50253`，可通过 `config.json` 的 `listen` 或环境变量 `PATROL_LISTEN` 修改。

## 测试方法

```bash
go fmt ./...
go mod tidy
go mod verify
go list -mod=readonly -m all
go build ./...
go test -timeout=120s -count=1 ./...
```

测试覆盖正常路径、错误路径、状态迁移、并发申领、取消行为与离线恢复，不依赖任何外部服务。

## Docker

使用两阶段构建，构建镜像 `golang:1.26`（非 latest），最终镜像仅保留运行文件并 `EXPOSE 50253`，支持 amd64 与 arm64：

```bash
# 当前架构构建
docker build -t patrol-dispatch:latest .

# 运行
docker run --rm -p 50253:50253 patrol-dispatch:latest

# 多架构构建（amd64 + arm64）
docker buildx build --platform linux/amd64,linux/arm64 -t patrol-dispatch:latest .
```

## 配置说明

`config.json` 字段：

| 字段 | 默认值 | 说明 |
|------|--------|------|
| `listen` | `:50253` | 监听地址 |
| `slaDuration` | `30m` | 事件响应 SLA |
| `deviationThresholdMeters` | `200` | 偏航预警阈值（米） |
| `syncInterval` | `5s` | 后台任务节拍 |
| `gridReuseWindow` | `24h` | 网格工单复用窗口 |
| `coreZoneMinAssignees` | `2` | 核心区最少结伴人数 |
| `equipmentHoldWindow` | `2m` | 装备锁定持有窗口 |

环境变量覆盖：`PATROL_LISTEN`、`PATROL_SLA`、`PATROL_DEVIATION_M`、`PATROL_SYNC_INTERVAL`。
