package script

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"sync"
	"time"

	"github.com/dop251/goja"
)

// S04-03：goja（仓库锁定版本 v0.0.0-20260311135729-065cd970411c）未提供 Runtime.SetMemoryLimit
// 与周期性 runtime.MemStats 回调，无法对单个 VM 设置原生内存上限。改为进程级内存看门狗：
// 后台 goroutine 周期采样 runtime.ReadMemStats，进程堆持续超过预算时中断所有在执行的 goja VM，
// 脚本以内存预算超限语义错误结束；同时以 debug.SetMemoryLimit(1.5×预算) 作为 GC 软上限，
// 压低超限期间的堆增长速度。实测（8 并发 VM × "a".repeat(3e8)）：
// 无看门狗峰值堆 ~2.3GB；预算 + GC 软上限下脚本均被中断，正常脚本不受影响。
// 预算取 512MB：压测下进程瞬时堆峰值（前一轮 Eval 待 GC 垃圾 + race detector 开销）约
// 300–450MB，预算过低会误伤正常脚本；2GB 级内存炸弹仍被快速中断。
const (
	// defaultScriptMemoryBudget 是脚本执行可用的进程堆预算；持续超过即中断所有在执行的 VM。
	defaultScriptMemoryBudget = 512 << 20
	// memoryWatchdogInterval 是堆采样周期；100ms 足以在超时中断前发现内存炸弹，
	// 且远小于测试改 time.Local 的窗口，ReadMemStats 采样不与主测试 goroutine 争 CPU。
	memoryWatchdogInterval = 100 * time.Millisecond
	// memoryWatchdogOverSamples 是触发中断所需的连续超预算采样数（200ms 持续超压），
	// 避免进程瞬时堆峰值（前一轮 Eval 待 GC 垃圾）误伤正常脚本。
	memoryWatchdogOverSamples = 2
)

// errScriptOutOfMemory 是内存预算超限的语义错误；中断值经 wrapRunError 归类为 script_failed
// （与超时区分），错误消息含明确的内存预算说明。
var errScriptOutOfMemory = fmt.Errorf("%w: 脚本内存预算超限（process heap budget exceeded）", ErrScriptFailed)

type memoryWatchdog struct {
	mu       sync.Mutex
	live     map[*goja.Runtime]struct{}
	stop     chan struct{}
	stopOnce sync.Once
	// overCount 连续超预算的采样次数：连续 memoryWatchdogOverSamples 次都超预算才中断。
	overCount int
}

var scriptMemoryWatchdog = &memoryWatchdog{live: map[*goja.Runtime]struct{}{}, stop: make(chan struct{})}

var watchdogStartOnce sync.Once

// startMemoryWatchdog 以幂等方式启动进程级内存看门狗；多个 Runtime 共享一个 goroutine。
// 采样周期 100ms，与主 goroutine 无 CPU 争用（ReadMemStats 单次 100–300µs）。
func startMemoryWatchdog() {
	watchdogStartOnce.Do(func() {
		// GC 软上限为预算的 1.5 倍：让 GC 在看门狗触发前积极回收，超限期间堆增长被压平。
		debug.SetMemoryLimit(defaultScriptMemoryBudget + defaultScriptMemoryBudget/2)
		go scriptMemoryWatchdog.loop()
	})
}

// loop 周期采样进程堆；持续超预算时中断所有在执行的 VM。
// 不读取 time.Local（测试改 Local 会与看门狗 goroutine 产生 DATA RACE）：
// 仅通过 Ticker channel 接收信号，不调用 time.Now/Since。
func (w *memoryWatchdog) loop() {
	var stats runtime.MemStats
	ticker := time.NewTicker(memoryWatchdogInterval)
	defer ticker.Stop()
	for {
		select {
		case <-w.stop:
			return
		case <-ticker.C:
		}
		runtime.ReadMemStats(&stats)
		if stats.HeapAlloc > defaultScriptMemoryBudget {
			w.overCount++
		} else {
			w.overCount = 0
		}
		if w.overCount >= memoryWatchdogOverSamples {
			w.mu.Lock()
			for vm := range w.live {
				vm.Interrupt(errScriptOutOfMemory)
			}
			w.mu.Unlock()
		}
	}
}

// trackVM 在 Eval 开始时把 VM 加入看门狗观察集，结束时移除；unregister 幂等。
func (w *memoryWatchdog) trackVM(vm *goja.Runtime) (unregister func()) {
	w.mu.Lock()
	w.live[vm] = struct{}{}
	w.mu.Unlock()
	return func() {
		w.mu.Lock()
		delete(w.live, vm)
		w.mu.Unlock()
	}
}
