# Bug Reproduction

## 包的性质

当前 test_model_fix 保存的是被测模型修复后的结果源码，不是初始含 Bug 源码。要复现原始缺陷，必须检出下面固定的 parent SHA；不要在当前修复结果源码上期待重新出现修复前失败。生成系统使用的可信验证补丁和完整验证日志仅在本地留存，不提交到结果分支。

## 问题现象

开了 -race 之后离线补传一压就报 data race，请帮我修复。

后台任务按 syncInterval 周期跑补传，同时巡护端 POST /api/sync/offline 和 POST /api/sync/online 也会立刻触发一次补传，另有监控在轮询 GET /api/sync/status。两边并发时：

  - -race 版本直接打出 WARNING: DATA RACE，测试环境里紧接着就是 race detected during execution of test 然后失败退出。
  - 关掉 -race 时功能看着正常，但 GET /api/sync/status 里的 syncedCount 有时会比实际已同步（stats.Synced）的记录数少，日报对不上。
  - 对照：单线程按顺序触发补传时一切正常，计数也准。

期望：并发触发补传不能出现任何 data race；所有缓存记录都要被补传完；对外暴露的同步计数不能漏计。修复后请保证 go test -race -timeout=300s -count=1 ./... 全绿（-race 需要 CGO_ENABLED=1），不要修改或跳过测试。

## 含 Bug 版本

- 仓库：11DingKing/go-ecfc00-t018-04
- 仓库地址：https://github.com/11DingKing/go-ecfc00-t018-04.git
- parent SHA：2b645f038b1fb800e3363b94b3ddec4226dc45ef

## 复现步骤

```bash
git clone -- https://github.com/11DingKing/go-ecfc00-t018-04.git bug-repro
cd bug-repro
git checkout --detach 2b645f038b1fb800e3363b94b3ddec4226dc45ef
go test -race ./internal/sync -run "^TestConcurrentTicksReportEverySyncedRecord$" -count=1 -v
```

## 双架构完整错误信息

### linux/amd64

- 容器内复现预期退出码：1
- 容器内复现实际退出码：1

stdout：

```text
$ go test -race ./internal/sync -run "^TestConcurrentTicksReportEverySyncedRecord$" -count=1 -v
=== RUN   TestConcurrentTicksReportEverySyncedRecord
==================
WARNING: DATA RACE
Write at 0x00c000164330 by goroutine 12:
  qilian-patrol/internal/sync.(*Syncer).monitorSLA()
      /app/internal/sync/syncer.go:83 +0xa4
  qilian-patrol/internal/sync.(*Syncer).TickOnce()
      /app/internal/sync/syncer.go:75 +0x26
  qilian-patrol/internal/sync.TestConcurrentTicksReportEverySyncedRecord.func1()
      /app/internal/sync/concurrent_tick_test.go:52 +0x99

Previous write at 0x00c000164330 by goroutine 11:
  qilian-patrol/internal/sync.(*Syncer).monitorSLA()
      /app/internal/sync/syncer.go:83 +0xa4
  qilian-patrol/internal/sync.(*Syncer).TickOnce()
      /app/internal/sync/syncer.go:75 +0x26
  qilian-patrol/internal/sync.TestConcurrentTicksReportEverySyncedRecord.func1()
      /app/internal/sync/concurrent_tick_test.go:52 +0x99

Goroutine 12 (running) created at:
  qilian-patrol/internal/sync.TestConcurrentTicksReportEverySyncedRecord()
      /app/internal/sync/concurrent_tick_test.go:49 +0x6d2
  testing.tRunner()
      /usr/local/go/src/testing/testing.go:2036 +0x21c
  testing.(*T).Run.gowrap1()
      /usr/local/go/src/testing/testing.go:2101 +0x38

Goroutine 11 (running) created at:
  qilian-patrol/internal/sync.TestConcurrentTicksReportEverySyncedRecord()
      /app/internal/sync/concurrent_tick_test.go:49 +0x6d2
  testing.tRunner()
      /usr/local/go/src/testing/testing.go:2036 +0x21c
  testing.(*T).Run.gowrap1()
      /usr/local/go/src/testing/testing.go:2101 +0x38
==================
==================
WARNING: DATA RACE
Write at 0x00c000164348 by goroutine 10:
  qilian-patrol/internal/sync.(*Syncer).reconcileOffline()
      /app/internal/sync/syncer.go:96 +0xd0
  qilian-patrol/internal/sync.(*Syncer).TickOnce()
      /app/internal/sync/syncer.go:76 +0x35
  qilian-patrol/internal/sync.TestConcurrentTicksReportEverySyncedRecord.func1()
      /app/internal/sync/concurrent_tick_test.go:52 +0x99

Previous write at 0x00c000164348 by goroutine 11:
  qilian-patrol/internal/sync.(*Syncer).reconcileOffline()
      /app/internal/sync/syncer.go:96 +0xd0
  qilian-patrol/internal/sync.(*Syncer).TickOnce()
      /app/internal/sync/syncer.go:76 +0x35
  qilian-patrol/internal/sync.TestConcurrentTicksReportEverySyncedRecord.func1()
      /app/internal/sync/concurrent_tick_test.go:52 +0x99

Goroutine 10 (running) created at:
  qilian-patrol/internal/sync.TestConcurrentTicksReportEverySyncedRecord()
      /app/internal/sync/concurrent_tick_test.go:49 +0x6d2
  testing.tRunner()
      /usr/local/go/src/testing/testing.go:2036 +0x21c
  testing.(*T).Run.gowrap1()
      /usr/local/go/src/testing/testing.go:2101 +0x38

Goroutine 11 (running) created at:
  qilian-patrol/internal/sync.TestConcurrentTicksReportEverySyncedRecord()
      /app/internal/sync/concurrent_tick_test.go:49 +0x6d2
  testing.tRunner()
      /usr/local/go/src/testing/testing.go:2036 +0x21c
  testing.(*T).Run.gowrap1()
      /usr/local/go/src/testing/testing.go:2101 +0x38
==================
==================
WARNING: DATA RACE
Write at 0x00c000164360 by goroutine 11:
  qilian-patrol/internal/sync.(*Syncer).reconcileOffline()
      /app/internal/sync/syncer.go:110 +0x590
  qilian-patrol/internal/sync.(*Syncer).TickOnce()
      /app/internal/sync/syncer.go:76 +0x35
  qilian-patrol/internal/sync.TestConcurrentTicksReportEverySyncedRecord.func1()
      /app/internal/sync/concurrent_tick_test.go:52 +0x99

Previous read at 0x00c000164360 by goroutine 15:
  qilian-patrol/internal/sync.(*Syncer).SyncedCount()
      /app/internal/sync/syncer.go:49 +0xa4
  qilian-patrol/internal/sync.TestConcurrentTicksReportEverySyncedRecord.func2()
      /app/internal/sync/concurrent_tick_test.go:67 +0x9b

Goroutine 11 (running) created at:
  qilian-patrol/internal/sync.TestConcurrentTicksReportEverySyncedRecord()
      /app/internal/sync/concurrent_tick_test.go:49 +0x6d2
  testing.tRunner()
      /usr/local/go/src/testing/testing.go:2036 +0x21c
  testing.(*T).Run.gowrap1()
      /usr/local/go/src/testing/testing.go:2101 +0x38

Goroutine 15 (running) created at:
  qilian-patrol/internal/sync.TestConcurrentTicksReportEverySyncedRecord()
      /app/internal/sync/concurrent_tick_test.go:60 +0x932
  testing.tRunner()
      /usr/local/go/src/testing/testing.go:2036 +0x21c
  testing.(*T).Run.gowrap1()
      /usr/local/go/src/testing/testing.go:2101 +0x38
==================
    testing.go:1712: race detected during execution of test
--- FAIL: TestConcurrentTicksReportEverySyncedRecord (0.14s)
FAIL
FAIL	qilian-patrol/internal/sync	0.205s
FAIL

```

stderr：

```text
(empty)
```

### linux/arm64

- 容器内复现预期退出码：1
- 容器内复现实际退出码：1

stdout：

```text
$ go test -race ./internal/sync -run "^TestConcurrentTicksReportEverySyncedRecord$" -count=1 -v
=== RUN   TestConcurrentTicksReportEverySyncedRecord
==================
WARNING: DATA RACE
Write at 0x00c00007e3a0 by goroutine 9:
  qilian-patrol/internal/sync.(*Syncer).monitorSLA()
      /app/internal/sync/syncer.go:83 +0x90
  qilian-patrol/internal/sync.(*Syncer).TickOnce()
      /app/internal/sync/syncer.go:75 +0x28
  qilian-patrol/internal/sync.TestConcurrentTicksReportEverySyncedRecord.func1()
      /app/internal/sync/concurrent_tick_test.go:52 +0x88

Previous write at 0x00c00007e3a0 by goroutine 11:
  qilian-patrol/internal/sync.(*Syncer).monitorSLA()
      /app/internal/sync/syncer.go:83 +0x90
  qilian-patrol/internal/sync.(*Syncer).TickOnce()
      /app/internal/sync/syncer.go:75 +0x28
  qilian-patrol/internal/sync.TestConcurrentTicksReportEverySyncedRecord.func1()
      /app/internal/sync/concurrent_tick_test.go:52 +0x88

Goroutine 9 (running) created at:
  qilian-patrol/internal/sync.TestConcurrentTicksReportEverySyncedRecord()
      /app/internal/sync/concurrent_tick_test.go:49 +0x4a8
  testing.tRunner()
      /usr/local/go/src/testing/testing.go:2036 +0x164
  testing.(*T).Run.gowrap1()
      /usr/local/go/src/testing/testing.go:2101 +0x34

Goroutine 11 (running) created at:
  qilian-patrol/internal/sync.TestConcurrentTicksReportEverySyncedRecord()
      /app/internal/sync/concurrent_tick_test.go:49 +0x4a8
  testing.tRunner()
      /usr/local/go/src/testing/testing.go:2036 +0x164
  testing.(*T).Run.gowrap1()
      /usr/local/go/src/testing/testing.go:2101 +0x34
==================
==================
WARNING: DATA RACE
Write at 0x00c00007e3b8 by goroutine 9:
  qilian-patrol/internal/sync.(*Syncer).reconcileOffline()
      /app/internal/sync/syncer.go:96 +0x94
  qilian-patrol/internal/sync.(*Syncer).TickOnce()
      /app/internal/sync/syncer.go:76 +0x34
  qilian-patrol/internal/sync.TestConcurrentTicksReportEverySyncedRecord.func1()
      /app/internal/sync/concurrent_tick_test.go:52 +0x88

Previous write at 0x00c00007e3b8 by goroutine 11:
  qilian-patrol/internal/sync.(*Syncer).reconcileOffline()
      /app/internal/sync/syncer.go:96 +0x94
  qilian-patrol/internal/sync.(*Syncer).TickOnce()
      /app/internal/sync/syncer.go:76 +0x34
  qilian-patrol/internal/sync.TestConcurrentTicksReportEverySyncedRecord.func1()
      /app/internal/sync/concurrent_tick_test.go:52 +0x88

Goroutine 9 (running) created at:
  qilian-patrol/internal/sync.TestConcurrentTicksReportEverySyncedRecord()
      /app/internal/sync/concurrent_tick_test.go:49 +0x4a8
  testing.tRunner()
      /usr/local/go/src/testing/testing.go:2036 +0x164
  testing.(*T).Run.gowrap1()
      /usr/local/go/src/testing/testing.go:2101 +0x34

Goroutine 11 (running) created at:
  qilian-patrol/internal/sync.TestConcurrentTicksReportEverySyncedRecord()
      /app/internal/sync/concurrent_tick_test.go:49 +0x4a8
  testing.tRunner()
      /usr/local/go/src/testing/testing.go:2036 +0x164
  testing.(*T).Run.gowrap1()
      /usr/local/go/src/testing/testing.go:2101 +0x34
==================
==================
WARNING: DATA RACE
Write at 0x00c00007e3d0 by goroutine 10:
  qilian-patrol/internal/sync.(*Syncer).reconcileOffline()
      /app/internal/sync/syncer.go:110 +0x328
  qilian-patrol/internal/sync.(*Syncer).TickOnce()
      /app/internal/sync/syncer.go:76 +0x34
  qilian-patrol/internal/sync.TestConcurrentTicksReportEverySyncedRecord.func1()
      /app/internal/sync/concurrent_tick_test.go:52 +0x88

Previous read at 0x00c00007e3d0 by goroutine 15:
  qilian-patrol/internal/sync.(*Syncer).SyncedCount()
      /app/internal/sync/syncer.go:49 +0x94
  qilian-patrol/internal/sync.TestConcurrentTicksReportEverySyncedRecord.func2()
      /app/internal/sync/concurrent_tick_test.go:67 +0x90

Goroutine 10 (running) created at:
  qilian-patrol/internal/sync.TestConcurrentTicksReportEverySyncedRecord()
      /app/internal/sync/concurrent_tick_test.go:49 +0x4a8
  testing.tRunner()
      /usr/local/go/src/testing/testing.go:2036 +0x164
  testing.(*T).Run.gowrap1()
      /usr/local/go/src/testing/testing.go:2101 +0x34

Goroutine 15 (running) created at:
  qilian-patrol/internal/sync.TestConcurrentTicksReportEverySyncedRecord()
      /app/internal/sync/concurrent_tick_test.go:60 +0x670
  testing.tRunner()
      /usr/local/go/src/testing/testing.go:2036 +0x164
  testing.(*T).Run.gowrap1()
      /usr/local/go/src/testing/testing.go:2101 +0x34
==================
    testing.go:1712: race detected during execution of test
--- FAIL: TestConcurrentTicksReportEverySyncedRecord (0.08s)
FAIL
FAIL	qilian-patrol/internal/sync	0.086s
FAIL

```

stderr：

```text
(empty)
```

## 通过条件

定向测试通过：go test -race ./internal/sync -run '^TestConcurrentTicksReportEverySyncedRecord$' -count=1 -v（CGO_ENABLED=1）
全量回归 go test -race -timeout=300s -count=1 ./... 通过，go build ./...、go vet ./... 与 gofmt -l . 干净
并发触发补传时 race detector 无告警；240 条缓存记录全部进入 synced；对外 SyncedCount() 不小于 240；既有离线恢复、幂等与 SLA 监控测试保持通过
