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

func (s *AppService) GetChapterContent(ctx context.Context, id string, index int) (Result, *book.Book, error) {
	b, err := s.Books.FindByID(ctx, id)
	if err != nil {
		return Result{}, nil, err
	}
	chapter, err := s.Chapters.FindByIndex(ctx, book.IDFromURL(b.BookURL), index)
	if err != nil {
		return Result{}, nil, err
	}
	if cached, ok, err := s.Chapters.GetContent(ctx, book.IDFromURL(b.BookURL), index); err != nil {
		return Result{}, nil, err
	} else if ok {
		return Result{Content: cached, Chapter: *chapter}, b, nil
	}
	src, err := s.Sources.FindByURL(ctx, b.Origin)
	if err != nil {
		return Result{}, nil, err
	}
	nextChapterURL := ""
	if next, err := s.Chapters.FindByIndex(ctx, book.IDFromURL(b.BookURL), index+1); err == nil && next != nil {
		nextChapterURL = next.URL
	}
	result, err := s.Content.GetContent(ctx, Input{Source: src, Book: *b, Chapter: *chapter, NextChapterURL: nextChapterURL})
	if err != nil {
		return Result{}, nil, err
	}
	return result, b, nil
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

		result, _, err := s.GetChapterContent(ctx, id, chapter.Index)
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
