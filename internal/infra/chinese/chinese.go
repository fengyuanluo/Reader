// Package chinese 提供进程级共享的繁简转换（OpenCC），供脚本运行时与正文替换复用。
package chinese

import (
	"log/slog"
	"sync"

	"github.com/longbridgeapp/opencc"
)

// converter 懒加载 OpenCC 词典；词典只读，Convert 可并发调用。
// 词典加载失败不再永久缓存：加载互斥锁保护重置逻辑，瞬时原因（词典文件暂不可读、
// 内存不足）恢复后下次调用会重试；转换期间的 Convert 调用返回原文（与 Legado 语义一致）。
type converter struct {
	mode   string
	initMu sync.Mutex
	cc     *opencc.OpenCC
	loaded bool
}

func (c *converter) ensure() (*opencc.OpenCC, error) {
	c.initMu.Lock()
	defer c.initMu.Unlock()
	if c.loaded {
		return c.cc, nil
	}
	cc, err := opencc.New(c.mode)
	if err != nil {
		// 失败不缓存 err：下次调用重试；每次失败打警告日志（此前静默吞错，繁简转换永久失效无日志）。
		slog.Warn("opencc dictionary load failed", "mode", c.mode, "error", err)
		return nil, err
	}
	c.cc, c.loaded = cc, true
	return c.cc, nil
}

func (c *converter) convert(value string) (string, error) {
	cc, err := c.ensure()
	if err != nil {
		return value, err
	}
	return cc.Convert(value)
}

var (
	t2s = &converter{mode: "t2s"}
	s2t = &converter{mode: "s2t"}
)

// T2S 把繁体转换为简体；词典加载或转换失败时返回原文。
func T2S(value string) string {
	return convertOrOriginal(t2s, value)
}

// S2T 把简体转换为繁体；词典加载或转换失败时返回原文。
func S2T(value string) string {
	return convertOrOriginal(s2t, value)
}

func convertOrOriginal(c *converter, value string) string {
	if value == "" {
		return ""
	}
	converted, err := c.convert(value)
	if err != nil {
		return value
	}
	return converted
}
