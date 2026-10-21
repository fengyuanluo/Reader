package contentapp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"reader/internal/domain/book"
	"reader/internal/domain/source"
)

var (
	ErrExportTocEmpty      = errors.New("toc_empty")
	ErrExportFileSource    = errors.New("file source does not support chapter export")
	ErrExportWriterMissing = errors.New("export writer is required")
)

type ExportStats struct {
	ChapterCount        int
	ContentChapterCount int
	Bytes               int64
}

type AppService struct {
	Books    book.BookRepository
	Chapters book.ChapterRepository
	Sources  source.BookSourceRepository
	Content  *Service
}

func NewAppService(books book.BookRepository, chapters book.ChapterRepository, sources source.BookSourceRepository, content *Service) *AppService {
	return &AppService{Books: books, Chapters: chapters, Sources: sources, Content: content}
}

// GetContentFromChapter 直接用内存中的书与章节对象抓取正文，不查询章节表：
// 供 debug 的 content_url 模式复用（Legado Debug.kt 在该模式下同样直接构造 BookChapter 且不落库）。
func (s *AppService) GetContentFromChapter(ctx context.Context, sourceID string, b book.Book, chapter book.BookChapter) (Result, error) {
	src, err := s.Sources.FindByID(ctx, sourceID)
	if err != nil {
		return Result{}, err
	}
	nextChapterURL := ""
	if next, err := s.Chapters.FindByIndex(ctx, book.IDFromURL(b.BookURL), chapter.Index+1); err == nil && next != nil {
		nextChapterURL = next.URL
	}
	return s.Content.GetContent(ctx, Input{Source: src, Book: b, Chapter: chapter, NextChapterURL: nextChapterURL, SkipCache: true})
}

// GetTransientContent 用内存中的 source/book/chapter 抓正文，既不读也不写正文缓存与章节表；
// 供书源校验、调试与 fetch 探测复用（对应 Legado WebBook.getContent(needSave=false)）。
func (s *AppService) GetTransientContent(ctx context.Context, src *source.BookSource, b book.Book, chapter book.BookChapter, nextChapterURL string) (Result, error) {
	return s.Content.GetContent(ctx, Input{Source: src, Book: b, Chapter: chapter, NextChapterURL: nextChapterURL, SkipCache: true, CacheLookupDone: true})
}

// processContent 应用用户替换规则、重新分段与简繁转换；未装配内容服务时返回原文，
// 使仅依赖缓存正文的调用方（如 TXT 导出）在无净化链时仍可用。
func (s *AppService) processContent(ctx context.Context, b book.Book, chapter book.BookChapter, content string) (string, error) {
	if s.Content == nil {
		return content, nil
	}
	return s.Content.ProcessContent(ctx, b, chapter, content)
}

func (s *AppService) GetChapterContent(ctx context.Context, id string, index int) (Result, *book.Book, error) {
	b, err := s.Books.FindByID(ctx, id)
	if err != nil {
		return Result{}, nil, err
	}
	bookID := book.IDFromURL(b.BookURL)
	chapter, err := s.Chapters.FindByIndex(ctx, bookID, index)
	if err != nil {
		return Result{}, nil, err
	}
	if cached, ok, err := s.Chapters.GetContent(ctx, bookID, index); err != nil {
		return Result{}, nil, err
	} else if ok {
		// 缓存保存的是净化前正文，命中缓存也要应用用户替换规则（对齐 Legado 阅读时净化）。
		content, err := s.processContent(ctx, *b, *chapter, cached)
		if err != nil {
			return Result{}, nil, err
		}
		return Result{Content: content, Chapter: *chapter}, b, nil
	}
	src, err := s.Sources.FindByURL(ctx, b.Origin)
	if err != nil {
		return Result{}, nil, err
	}
	// 对齐 Legado BookContent.kt:52-56：末章时 nextChapterUrl 回退到第 0 章 URL，供依赖它的正文规则使用。
	nextChapterURL := ""
	if next, err := s.Chapters.FindByIndex(ctx, bookID, index+1); err == nil && next != nil {
		nextChapterURL = next.URL
	} else if first, err := s.Chapters.FindByIndex(ctx, bookID, 0); err == nil && first != nil {
		nextChapterURL = first.URL
	}
	result, err := s.Content.GetContent(ctx, Input{Source: src, Book: *b, Chapter: *chapter, NextChapterURL: nextChapterURL})
	if err != nil {
		return Result{}, nil, err
	}
	return result, b, nil
}

// GetChapterContentWith 用调用方已加载的 source/book/chapter 完成一次“缓存优先”的正文获取，
// 不访问 books/chapters 表（下载与探测热路径专用）。nextChapterURL 为空表示无下一章。
// 缓存命中时应用用户替换规则，未命中时抓取并写入缓存，与 GetChapterContent 语义一致。
func (s *AppService) GetChapterContentWith(ctx context.Context, src *source.BookSource, b book.Book, chapter book.BookChapter, nextChapterURL string) (Result, error) {
	bookID := book.IDFromURL(b.BookURL)
	if cached, ok, err := s.Chapters.GetContent(ctx, bookID, chapter.Index); err != nil {
		return Result{}, err
	} else if ok {
		content, err := s.processContent(ctx, b, chapter, cached)
		if err != nil {
			return Result{}, err
		}
		return Result{Content: content, Chapter: chapter}, nil
	}
	return s.Content.GetContent(ctx, Input{Source: src, Book: b, Chapter: chapter, NextChapterURL: nextChapterURL, CacheLookupDone: true})
}

// ExportTXT writes a complete, ordered book export to w. The caller owns the
// output lifecycle so it can discard partial data when a chapter fails.
func (s *AppService) ExportTXT(ctx context.Context, id string, w io.Writer) (ExportStats, error) {
	if w == nil {
		return ExportStats{}, ErrExportWriterMissing
	}
	b, err := s.Books.FindByID(ctx, id)
	if err != nil {
		return ExportStats{}, err
	}
	if b.Type == 3 {
		return ExportStats{}, ErrExportFileSource
	}
	chapters, err := s.Chapters.ListByBook(ctx, book.IDFromURL(b.BookURL))
	if err != nil {
		return ExportStats{}, err
	}
	if len(chapters) == 0 {
		return ExportStats{}, ErrExportTocEmpty
	}

	counted := &countingWriter{writer: w}
	if err := writeExportText(counted, b.Name); err != nil {
		return ExportStats{}, fmt.Errorf("write book title: %w", err)
	}
	if author := strings.TrimSpace(b.Author); author != "" {
		if err := writeExportText(counted, "作者："+author); err != nil {
			return ExportStats{}, fmt.Errorf("write book author: %w", err)
		}
	}
	if intro := strings.TrimSpace(b.Intro); intro != "" {
		if err := writeExportText(counted, "简介："+intro); err != nil {
			return ExportStats{}, fmt.Errorf("write book intro: %w", err)
		}
	}
	if err := writeExportText(counted, ""); err != nil {
		return ExportStats{}, fmt.Errorf("write book separator: %w", err)
	}

	stats := ExportStats{ChapterCount: len(chapters)}
	// S13-04：循环内复用已加载的 b/chapter（GetChapterContentWith 只查缓存表），
	// source 仅在遇到未缓存章节时按需查询一次，避免数千章的书触发 N+1 查询。
	exportBookID := book.IDFromURL(b.BookURL)
	var src *source.BookSource
	var srcErr error
	for _, chapter := range chapters {
		if chapter == nil {
			return ExportStats{}, errors.New("nil chapter in table of contents")
		}
		if err := ctx.Err(); err != nil {
			return ExportStats{}, err
		}
		if chapter.IsVolume {
			if err := writeExportText(counted, chapter.Title); err != nil {
				return ExportStats{}, fmt.Errorf("write volume %d: %w", chapter.Index, err)
			}
			if err := writeExportText(counted, ""); err != nil {
				return ExportStats{}, fmt.Errorf("write volume separator %d: %w", chapter.Index, err)
			}
			continue
		}

		var nextChapterURL string
		if next, err := s.Chapters.FindByIndex(ctx, exportBookID, chapter.Index+1); err == nil && next != nil {
			nextChapterURL = next.URL
		} else if first, err := s.Chapters.FindByIndex(ctx, exportBookID, 0); err == nil && first != nil {
			nextChapterURL = first.URL
		}
		if src == nil && srcErr == nil {
			if _, cached, err := s.Chapters.GetContent(ctx, exportBookID, chapter.Index); err != nil {
				return ExportStats{}, err
			} else if !cached {
				// S13-04：source 仅在需要联网抓取（缓存未命中）时查询一次并复用；
				// 缓存齐全的书零次 source 查询。
				src, srcErr = s.Sources.FindByURL(ctx, b.Origin)
				if srcErr != nil {
					return ExportStats{}, fmt.Errorf("chapter %d (%s): %w", chapter.Index, chapter.Title, srcErr)
				}
			}
		}
		result, err := s.GetChapterContentWith(ctx, src, *b, *chapter, nextChapterURL)
		if err != nil {
			return ExportStats{}, fmt.Errorf("chapter %d (%s): %w", chapter.Index, chapter.Title, err)
		}
		content := strings.Trim(normalizeExportText(result.Content), "\n")
		if strings.TrimSpace(content) == "" {
			return ExportStats{}, fmt.Errorf("chapter %d (%s): %w", chapter.Index, chapter.Title, ErrContentEmpty)
		}
		title := strings.TrimSpace(result.Chapter.Title)
		if title == "" {
			title = strings.TrimSpace(chapter.Title)
		}
		if title != "" {
			if err := writeExportText(counted, title); err != nil {
				return ExportStats{}, fmt.Errorf("write chapter title %d: %w", chapter.Index, err)
			}
		}
		if err := writeExportText(counted, content); err != nil {
			return ExportStats{}, fmt.Errorf("write chapter %d: %w", chapter.Index, err)
		}
		if err := writeExportText(counted, ""); err != nil {
			return ExportStats{}, fmt.Errorf("write chapter separator %d: %w", chapter.Index, err)
		}
		stats.ContentChapterCount++
	}
	stats.Bytes = counted.bytes
	return stats, nil
}

type countingWriter struct {
	writer io.Writer
	bytes  int64
}

func (w *countingWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	w.bytes += int64(n)
	return n, err
}

func writeExportText(w io.Writer, text string) error {
	text = strings.TrimRight(normalizeExportText(text), "\n")
	_, err := io.WriteString(w, text+"\n")
	return err
}

func normalizeExportText(text string) string {
	return strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
}
