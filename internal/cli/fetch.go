package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	bookapp "reader/internal/app/book"
	searchapp "reader/internal/app/search"
	"reader/internal/bootstrap"
	"reader/internal/domain/book"
	"reader/internal/domain/source"
)

// fetchCandidate 是搜索结果经书名/作者筛选后的候选条目。
type fetchCandidate struct {
	item   book.SearchBook
	source *source.BookSource
}

// probeResult 是单个候选源的探测结果。
type probeResult struct {
	candidate fetchCandidate
	bookID    string
	chapters  []*book.BookChapter
	// book 为详情补全后的书（FetchInfo 返回值），下载阶段直接使用，不再重建。
	book book.Book
	ok   bool
	// nonVolumeCount 为非卷章节数 C；contentLen 为首章正文 rune 数 L。
	nonVolumeCount int
	contentLen     int
	elapsed        time.Duration
	err            string
}

// fetch 参数上限：--probe/--workers/--search-workers 越界报用法错（S16-03 防自我 DOS），
// 其余数值参数同样给出上限，越界退出码 1。
const (
	maxProbeCount    = 32
	maxWorkers       = 64
	maxSearchWorkers = 128
	maxFetchSources  = 16
)

// runFetch 实现 `reader fetch <书名>`：多源搜索 → 候选筛选 → 并发探测 →
// 选最佳源 → 并发下载 → 导出 TXT。
func runFetch(env *cmdEnv, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("fetch", flag.ContinueOnError)
	author := fs.String("author", "", "filter candidates by author (substring, normalized)")
	group := fs.String("group", "", "search sources in this group")
	searchWorkers := fs.Int("search-workers", 32, "multi-source search concurrency")
	searchTimeout := fs.Duration("search-timeout", 30*time.Second, "multi-source search timeout")
	probeCount := fs.Int("probe", 5, "number of candidates to probe")
	workers := fs.Int("workers", 4, "concurrent chapter downloads")
	maxSources := fs.Int("max-sources", 3, "max sources to try before giving up")
	exportPath := fs.String("export", "", "TXT export path (default downloads/<name>_<author>.txt)")
	asJSON := fs.Bool("json", false, "output JSON")
	if code, done := ParseFlags(fs, "fetch <name> [flags]", args, stdout, stderr); done {
		return code
	}
	// 全部参数校验在打开运行时之前完成，保证用法错误不建库不跑迁移。
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "error: fetch requires exactly one <name> argument")
		return 1
	}
	if *searchWorkers < 1 || *searchWorkers > maxSearchWorkers {
		fmt.Fprintf(stderr, "error: --search-workers must be between 1 and %d, got %d\n", maxSearchWorkers, *searchWorkers)
		return 1
	}
	if *probeCount < 1 || *probeCount > maxProbeCount {
		fmt.Fprintf(stderr, "error: --probe must be between 1 and %d, got %d\n", maxProbeCount, *probeCount)
		return 1
	}
	if *workers < 1 || *workers > maxWorkers {
		fmt.Fprintf(stderr, "error: --workers must be between 1 and %d, got %d\n", maxWorkers, *workers)
		return 1
	}
	if *maxSources < 1 || *maxSources > maxFetchSources {
		fmt.Fprintf(stderr, "error: --max-sources must be between 1 and %d, got %d\n", maxFetchSources, *maxSources)
		return 1
	}
	if *searchTimeout <= 0 {
		fmt.Fprintf(stderr, "error: --search-timeout must be > 0, got %s\n", *searchTimeout)
		return 1
	}

	name := fs.Arg(0)

	// --search-workers 在 config.Load 后覆盖 READER_GLOBAL_CONCURRENCY，保证设置真正生效。
	env.globalConcurrency = *searchWorkers
	rt, code := env.runtime(stderr)
	if code != 0 {
		return code
	}
	ctx := env.ctx

	searchCtx, cancel := context.WithTimeout(ctx, *searchTimeout)
	defer cancel()
	result, err := rt.Searcher.Search(searchCtx, searchapp.MultiSearchInput{
		Keyword:     name,
		Page:        1,
		Precision:   true,
		Concurrency: *searchWorkers,
		Group:       *group,
	})
	if err != nil {
		fmt.Fprintf(stderr, "error: search: %v\n", err)
		return 2
	}
	for _, w := range result.Warnings {
		fmt.Fprintf(stderr, "warn: %s: %s\n", w.BookSourceName, w.Message)
	}

	// sources 按 BookSourceURL 反查 BookSource，复用 search 的写法。
	totalOrigins := map[string]bool{}
	sources := map[string]*source.BookSource{}
	for _, sb := range result.All {
		totalOrigins[sb.Origin] = true
		if _, ok := sources[sb.Origin]; ok {
			continue
		}
		if src, err := rt.SourceRepo.FindByURL(ctx, sb.Origin); err == nil && src != nil {
			sources[sb.Origin] = src
		}
	}

	candidates := selectCandidates(result.All, name, *author, sources)
	fmt.Fprintf(stderr, "search: resolved %d/%d origins, %d results, %d candidates\n",
		len(sources), len(totalOrigins), len(result.All), len(candidates))
	if len(candidates) == 0 {
		fmt.Fprintf(stderr, "error: no usable source for %s\n", name)
		return 2
	}

	// 并发探测前 probeCount 个候选（并发数等于候选数）。
	probes := probeCandidates(ctx, rt, candidates, *probeCount, stderr)
	ranked := rankProbes(probes)
	if len(ranked) == 0 {
		fmt.Fprintf(stderr, "error: no usable source for %s\n", name)
		return 2
	}

	// 按排名依次尝试下载，最多 maxSources 个源；失败率超过 5% 换下一个源。
	var (
		adoptedBookID string
		adoptedProbe  probeResult
		adoptedSect   []string
		adoptedSum    downloadSummary
		best          *probeResult
		bestSect      []string
		bestSum       downloadSummary
	)
	for i, probe := range ranked {
		if i >= *maxSources {
			break
		}
		probeBook := probe.book
		sections, summary := downloadChapters(ctx, rt, &probeBook, probe.bookID, probe.chapters, 0, len(probe.chapters)-1, *workers, stderr)
		threshold := probe.nonVolumeCount * 5 / 100
		if len(summary.Failed) <= threshold {
			adoptedBookID, adoptedProbe, adoptedSect, adoptedSum = probe.bookID, probe, sections, summary
			break
		}
		// 换源：清掉被淘汰源的章节正文缓存，防止孤儿缓存无限膨胀（S16-01）；
		// 采纳源的缓存保留。ChapterRepo 未提供删除能力时跳过（downgrade 为警告）。
		if cleaner, ok := rt.ChapterRepo.(interface {
			DeleteContentByOwner(context.Context, string) (int64, error)
		}); ok {
			if _, err := cleaner.DeleteContentByOwner(ctx, probe.bookID); err != nil {
				fmt.Fprintf(stderr, "warn: cleanup cache for %s: %v\n", probe.bookID, err)
			}
		}
		fmt.Fprintf(stderr, "warn: %s failed %d chapters, switching source\n", probe.candidate.source.BookSourceName, len(summary.Failed))
		if best == nil || len(summary.Failed) < len(bestSum.Failed) {
			best, bestSect, bestSum = &ranked[i], sections, summary
		}
	}
	if adoptedBookID == "" {
		if best == nil {
			// ranked 非空但一个源都没试（理论上 maxSources >= 1 不会发生）。
			fmt.Fprintf(stderr, "error: no usable source for %s\n", name)
			return 2
		}
		adoptedBookID, adoptedProbe, adoptedSect, adoptedSum = best.bookID, *best, bestSect, bestSum
	}

	if ctx.Err() != nil {
		fmt.Fprintln(stderr, "error: interrupted, export skipped")
		return 2
	}

	b := adoptedProbe.book
	exitCode := 0
	if len(adoptedSum.Failed) > 0 {
		exitCode = 2
	}
	exported := ""
	switch {
	case len(adoptedSect) == 0:
		// 无任何正文时不导出，与 download 的行为一致。
		fmt.Fprintln(stderr, "warn: no chapter content, export skipped")
	default:
		path := *exportPath
		if path == "" {
			path = filepath.Join("downloads", sanitizeFilename(b.Name)+"_"+sanitizeFilename(b.Author)+".txt")
		}
		if err := exportTXT(path, b.Name, b.Author, adoptedSect); err != nil {
			fmt.Fprintf(stderr, "error: export: %v\n", err)
			return 2
		}
		exported = path
	}

	if exitCode != 0 {
		fmt.Fprintf(stderr, "error: %d chapters failed\n", len(adoptedSum.Failed))
	}
	if *asJSON {
		if adoptedSum.Failed == nil {
			// JSON 输出 "failed": [] 而非 null（S16-04）。
			adoptedSum.Failed = []downloadFailureJSON{}
		}
		type probeJSON struct {
			OriginName string `json:"originName"`
			Chapters   int    `json:"chapters"`
			ContentLen int    `json:"contentLen"`
			ElapsedMs  int64  `json:"elapsedMs"`
			Error      string `json:"error,omitempty"`
		}
		probeList := make([]probeJSON, 0, len(probes))
		for _, probe := range probes {
			probeList = append(probeList, probeJSON{
				OriginName: probe.candidate.source.BookSourceName,
				Chapters:   probe.nonVolumeCount,
				ContentLen: probe.contentLen,
				ElapsedMs:  probe.elapsed.Milliseconds(),
				Error:      probe.err,
			})
		}
		out := struct {
			BookID     string                `json:"bookId"`
			Name       string                `json:"name"`
			Author     string                `json:"author"`
			SourceID   string                `json:"sourceId"`
			OriginName string                `json:"originName"`
			Chapters   int                   `json:"chapters"`
			Downloaded int                   `json:"downloaded"`
			Cached     int                   `json:"cached"`
			Failed     []downloadFailureJSON `json:"failed"`
			Exported   string                `json:"exported"`
			Probes     []probeJSON           `json:"probes"`
		}{
			BookID:     adoptedBookID,
			Name:       b.Name,
			Author:     b.Author,
			SourceID:   adoptedProbe.candidate.source.SourceID,
			OriginName: adoptedProbe.candidate.source.BookSourceName,
			Chapters:   adoptedProbe.nonVolumeCount,
			Downloaded: adoptedSum.Downloaded,
			Cached:     adoptedSum.Cached,
			Failed:     adoptedSum.Failed,
			Exported:   exported,
			Probes:     probeList,
		}
		data, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "error: encode json: %v\n", err)
			return 2
		}
		fmt.Fprintln(stdout, string(data))
	} else {
		fmt.Fprintf(stdout, "fetched\t%s\t%s\t%s\t%s\tchapters=%d\tfailed=%d\t%s\n",
			adoptedBookID, b.Name, b.Author, adoptedProbe.candidate.source.BookSourceName,
			adoptedProbe.nonVolumeCount, len(adoptedSum.Failed), exported)
	}
	return exitCode
}

// sanitizeFilename 把文件名成分里的路径分隔符替换成下划线。
func sanitizeFilename(s string) string {
	return strings.NewReplacer("/", "_", "\\", "_").Replace(s)
}

// norm 去掉所有 Unicode 空白并转小写，用于书名/作者的宽松匹配。
func norm(s string) string {
	return strings.ToLower(strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s))
}

// selectCandidates 对未合并的搜索条目做书名/作者筛选与选源排序：
// 1) 书名精确匹配，无命中时退化为包含匹配；
// 2) 指定 --author 时按作者包含筛选，否则按多源投票选出出现最多的作者；
// 3) 同一 origin 只保留第一条，origin 查不到 BookSource 的丢弃；
// 4) 稳定排序：有最新章节的优先 → Weight 降序 → RespondTime 升序。
func selectCandidates(items []book.SearchBook, name, author string, sources map[string]*source.BookSource) []fetchCandidate {
	normName := norm(name)
	kept := make([]book.SearchBook, 0, len(items))
	for _, item := range items {
		if norm(item.Name) == normName {
			kept = append(kept, item)
		}
	}
	if len(kept) == 0 {
		for _, item := range items {
			if strings.Contains(norm(item.Name), normName) {
				kept = append(kept, item)
			}
		}
	}
	if trimmed := strings.TrimSpace(author); trimmed != "" {
		normAuthor := norm(trimmed)
		filtered := kept[:0:0]
		for _, item := range kept {
			if strings.Contains(norm(item.Author), normAuthor) {
				filtered = append(filtered, item)
			}
		}
		kept = filtered
	} else if len(kept) > 0 {
		// 多源投票选原作者，避免选到同名的同人作品；平票取最早出现的作者。
		counts := map[string]int{}
		for _, item := range kept {
			counts[norm(item.Author)]++
		}
		bestAuthor, bestCount := "", 0
		for _, item := range kept {
			key := norm(item.Author)
			if n := counts[key]; n > bestCount {
				bestAuthor, bestCount = key, n
			}
		}
		filtered := kept[:0:0]
		for _, item := range kept {
			if norm(item.Author) == bestAuthor {
				filtered = append(filtered, item)
			}
		}
		kept = filtered
	}

	candidates := make([]fetchCandidate, 0, len(kept))
	seenOrigin := map[string]bool{}
	for _, item := range kept {
		if seenOrigin[item.Origin] {
			continue
		}
		src, ok := sources[item.Origin]
		if !ok || src == nil {
			continue
		}
		seenOrigin[item.Origin] = true
		candidates = append(candidates, fetchCandidate{item: item, source: src})
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		// 有最新章节的优先。
		if (a.item.LatestChapterTitle != "") != (b.item.LatestChapterTitle != "") {
			return a.item.LatestChapterTitle != ""
		}
		if a.source.Weight != b.source.Weight {
			return a.source.Weight > b.source.Weight
		}
		return a.source.RespondTime < b.source.RespondTime
	})
	return candidates
}

// probeCandidates 并发探测前 n 个候选（并发数等于候选数）。
func probeCandidates(ctx context.Context, rt *bootstrap.Runtime, candidates []fetchCandidate, n int, stderr io.Writer) []probeResult {
	if n > len(candidates) {
		n = len(candidates)
	}
	probes := make([]probeResult, n)
	var (
		wg sync.WaitGroup
		// 进度行在 mutex 内输出，避免并发探测时交错写 stderr。
		mu sync.Mutex
	)
	for i := range n {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			probes[i] = probeOne(ctx, rt, candidates[i])
			probe := probes[i]
			mu.Lock()
			defer mu.Unlock()
			if probe.err == "" {
				fmt.Fprintf(stderr, "probe: %s chapters=%d len=%d %.1fs\n",
					probe.candidate.source.BookSourceName, probe.nonVolumeCount, probe.contentLen, probe.elapsed.Seconds())
			} else {
				fmt.Fprintf(stderr, "probe: %s error=%s %.1fs\n",
					probe.candidate.source.BookSourceName, probe.err, probe.elapsed.Seconds())
			}
		}()
	}
	wg.Wait()
	return probes
}

// probeOne 对单个候选源执行 FetchInfo → FetchToc → 首个非卷章节正文，全程不写书架。
func probeOne(ctx context.Context, rt *bootstrap.Runtime, candidate fetchCandidate) probeResult {
	start := time.Now()
	probe := probeResult{candidate: candidate}
	item := candidate.item
	bookID := book.IDFromURL(item.BookURL)
	probe.bookID = bookID
	fail := func(format string, args ...any) probeResult {
		probe.err = fmt.Sprintf(format, args...)
		probe.elapsed = time.Since(start)
		return probe
	}
	src := candidate.source
	info, err := rt.Books.FetchInfo(ctx, src, item.ToBook())
	if err != nil {
		return fail("refresh info: %v", err)
	}
	chapters, err := rt.Books.FetchToc(ctx, src, info)
	if err != nil {
		return fail("refresh toc: %v", err)
	}
	probe.chapters = chapters
	probe.book = info
	for _, chapter := range chapters {
		if !chapter.IsVolume {
			probe.nonVolumeCount++
		}
	}
	if probe.nonVolumeCount == 0 {
		return fail("no chapters")
	}
	chapter, next, _ := bookapp.FirstContentChapter(chapters)
	result, err := rt.Content.GetTransientContent(ctx, src, info, chapter, next)
	if err != nil {
		return fail("content: %v", err)
	}
	probe.contentLen = len([]rune(result.Content))
	if probe.contentLen < 200 {
		return fail("first chapter too short: %d runes", probe.contentLen)
	}
	probe.ok = true
	probe.elapsed = time.Since(start)
	return probe
}

// rankProbes 在合格者中选源：C >= 0.95*maxC 进第一梯队并按耗时升序，
// 其余按章节数降序接在后面；不合格者在返回值中置后（调用方只取前面的）。
func rankProbes(probes []probeResult) []probeResult {
	usable := make([]probeResult, 0, len(probes))
	for _, probe := range probes {
		if probe.ok {
			usable = append(usable, probe)
		}
	}
	if len(usable) == 0 {
		return nil
	}
	maxC := 0
	for _, probe := range usable {
		if probe.nonVolumeCount > maxC {
			maxC = probe.nonVolumeCount
		}
	}
	firstTier := make([]probeResult, 0, len(usable))
	rest := make([]probeResult, 0, len(usable))
	for _, probe := range usable {
		if float64(probe.nonVolumeCount) >= 0.95*float64(maxC) {
			firstTier = append(firstTier, probe)
		} else {
			rest = append(rest, probe)
		}
	}
	sort.SliceStable(firstTier, func(i, j int) bool {
		return firstTier[i].elapsed < firstTier[j].elapsed
	})
	sort.SliceStable(rest, func(i, j int) bool {
		return rest[i].nonVolumeCount > rest[j].nonVolumeCount
	})
	return append(firstTier, rest...)
}
