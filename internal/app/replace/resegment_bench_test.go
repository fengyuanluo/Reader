package replaceapp

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"reader/internal/domain/replace"
)

func benchContext() context.Context { return context.Background() }

// BenchmarkReSegmentLargeChapter 是大文本基准：500KB、2000 段的长章节，
// 旧实现 shouldBreakMergedParagraphBuilder 每段整串拷贝为 O(P·n)（S14-02）。
func BenchmarkReSegmentLargeChapter(b *testing.B) {
	var sb strings.Builder
	sb.WriteString("第一章")
	for i := range 2000 {
		sb.WriteString("\n这是第")
		sb.WriteString(strconv.Itoa(i))
		sb.WriteString("段正文内容，包含若干句子。对话如下：“你好。”药老回答：“不错。”两人继续前行。")
	}
	content := sb.String()
	b.SetBytes(int64(len(content)))
	for b.Loop() {
		reSegment(content, "第一章")
	}
}

// BenchmarkReSegmentDenseSentenceEnd 是句末符号密集的长段基准：200KB、无引号、
// 句末符号上千，旧实现 deterministicReSegmentScore 每符号整段重哈希为 O(m²)（S14-03）。
func BenchmarkReSegmentDenseSentenceEnd(b *testing.B) {
	sentence := "这一段没有引号，只有密集的句末符号，用于压测强制切分路径。"
	var sb strings.Builder
	for len(sb.String()) < 200*1024 {
		sb.WriteString(sentence)
	}
	content := sb.String()
	b.SetBytes(int64(len(content)))
	for b.Loop() {
		reSegment(content, "")
	}
}

// BenchmarkForceReSegmentSplitHotPath 直接压测 forceReSegmentSplit：
// 同一 (text, offset) 下 midCount 递增，每个句末符号都取分，验证基础哈希缓存生效。
func BenchmarkForceReSegmentSplitHotPath(b *testing.B) {
	var sb strings.Builder
	for range 2000 {
		sb.WriteString("短句。又一短句，带逗号。")
	}
	text := sb.String()
	b.SetBytes(int64(len(text)))
	for b.Loop() {
		forceReSegmentSplit(text, 0, 3, 3, 2)
	}
}

// BenchmarkReplaceRulesManyChapters 压测 100 条规则 × 1000 章的 replaceText 路径：
// 旧实现 Validate 每章每条重新 regexp.Compile 绕过共享 LRU（S14-12）。
func BenchmarkReplaceRulesManyChapters(b *testing.B) {
	rules := make([]replace.ReplaceRule, 100)
	for i := range rules {
		rules[i] = replace.ReplaceRule{
			Pattern: "广告片段" + strconv.Itoa(i%10), Replacement: "", IsRegex: false, IsEnabled: true,
		}
	}
	chapter := strings.Repeat("正文内容，广告片段0，广告片段1。", 50)
	for b.Loop() {
		for _, rule := range rules {
			_, _ = replaceText(benchContext(), rule, chapter)
		}
	}
}
