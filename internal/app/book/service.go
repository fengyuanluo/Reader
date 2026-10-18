package bookapp

import (
	"context"
	"errors"
	"fmt"

	"reader/internal/domain/book"
	"reader/internal/domain/source"
)

// ErrProgressBookIdentity 表示请求体里的 name/author 与目标书籍不一致（或缺失），
// 由 handler 映射为 400；spec 把二者标为必填，服务端据此校验进度归属。
var ErrProgressBookIdentity = errors.New("progress name/author does not match the book")

type Service struct {
	Books    book.BookRepository
	Chapters book.ChapterRepository
	Sources  source.BookSourceRepository
	Info     *InfoService
	Toc      *TocService
}

func NewService(books book.BookRepository, chapters book.ChapterRepository, sources source.BookSourceRepository, info *InfoService, toc *TocService) *Service {
	return &Service{Books: books, Chapters: chapters, Sources: sources, Info: info, Toc: toc}
}

func (s *Service) Save(ctx context.Context, b *book.Book) (*book.Book, error) {
	if err := s.Books.Save(ctx, b); err != nil {
		return nil, err
	}
	// Save 的 upsert 语义保证 book_url 不变，直接按 URL 反查 book_id 返回，省去一次读库。
	return s.Books.FindByURL(ctx, b.BookURL)
}

func (s *Service) List(ctx context.Context, filter book.BookFilter) ([]*book.Book, int, error) {
	return s.Books.List(ctx, filter)
}

func (s *Service) Get(ctx context.Context, id string) (*book.Book, error) {
	return s.Books.FindByID(ctx, id)
}

func (s *Service) Delete(ctx context.Context, id string) error {
	return s.Books.Delete(ctx, id)
}

func (s *Service) SaveProgress(ctx context.Context, id string, progress book.BookProgress) (*book.Book, error) {
	b, err := s.Books.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if progress.Name == "" || progress.Author == "" {
		return nil, fmt.Errorf("%w: name and author are required", ErrProgressBookIdentity)
	}
	if progress.Name != b.Name || progress.Author != b.Author {
		return nil, fmt.Errorf("%w: book is %q/%q", ErrProgressBookIdentity, b.Name, b.Author)
	}
	if err := s.Books.SaveProgress(ctx, id, progress); err != nil {
		return nil, err
	}
	return s.Books.FindByID(ctx, id)
}

// FetchInfo 只抓取并解析详情，不读也不写 books 表（书源校验 / 调试 / fetch 探测专用，
// 对应 Legado CheckSourceService 与 Debug 的 needSave=false）。
func (s *Service) FetchInfo(ctx context.Context, src *source.BookSource, b book.Book) (book.Book, error) {
	return s.Info.RefreshInfo(ctx, InfoInput{Source: src, Book: b, InfoHTML: b.InfoHTML, CanRename: true, SkipSave: true})
}

// FetchToc 只抓取并解析目录，不读也不写 chapters 表。
func (s *Service) FetchToc(ctx context.Context, src *source.BookSource, b book.Book) ([]*book.BookChapter, error) {
	return s.Toc.RefreshToc(ctx, TocInput{Source: src, Book: b, TocHTML: b.TocHTML, SkipSave: true})
}

func (s *Service) RefreshInfo(ctx context.Context, id string) (book.Book, error) {
	b, err := s.Books.FindByID(ctx, id)
	if err != nil {
		return book.Book{}, err
	}
	src, err := s.Sources.FindByURL(ctx, b.Origin)
	if err != nil {
		return book.Book{}, err
	}
	// 复用已加载的书与 source，InfoService 内部只写信息列，不再重复整行写。
	return s.Info.RefreshInfo(ctx, InfoInput{Source: src, Book: *b, InfoHTML: b.InfoHTML, CanRename: true})
}

func (s *Service) RefreshToc(ctx context.Context, id string) ([]*book.BookChapter, *book.Book, error) {
	b, err := s.Books.FindByID(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	src, err := s.Sources.FindByURL(ctx, b.Origin)
	if err != nil {
		return nil, nil, err
	}
	chapters, err := s.Toc.RefreshToc(ctx, TocInput{Source: src, Book: *b, TocHTML: b.TocHTML})
	if err != nil {
		return nil, nil, err
	}
	return chapters, b, nil
}

func (s *Service) ListChapters(ctx context.Context, id string) ([]*book.BookChapter, *book.Book, error) {
	b, err := s.Books.FindByID(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	chapters, err := s.Chapters.ListByBook(ctx, book.IDFromURL(b.BookURL))
	if err != nil {
		return nil, nil, err
	}
	return chapters, b, nil
}
