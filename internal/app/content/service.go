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
