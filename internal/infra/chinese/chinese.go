// Package chinese 提供进程级共享的繁简转换（OpenCC），供脚本运行时与正文替换复用。
package chinese

import (
	"sync"

	"github.com/longbridgeapp/opencc"
)

// converter 以 sync.Once 懒加载 OpenCC 词典；词典只读，Convert 可并发调用。
type converter struct {
	mode string
	once sync.Once
	cc   *opencc.OpenCC
	err  error
}

func (c *converter) convert(value string) (string, error) {
	c.once.Do(func() {
		c.cc, c.err = opencc.New(c.mode)
	})
	if c.err != nil {
		return value, c.err
	}
	return c.cc.Convert(value)
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
