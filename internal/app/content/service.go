package contentapp

import (
	"context"

	"reader/internal/domain/book"
	"reader/internal/domain/source"
)

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
