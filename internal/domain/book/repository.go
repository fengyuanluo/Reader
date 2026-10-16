package book

import "context"

type BookFilter struct {
	Page     int
	PageSize int
}

type BookProgress struct {
	DurChapterTitle string
	DurChapterIndex int
	DurChapterPos   int
	DurChapterTime  int64
}

type BookRepository interface {
	Save(ctx context.Context, book *Book) error
	FindByID(ctx context.Context, id string) (*Book, error)
	FindByURL(ctx context.Context, bookURL string) (*Book, error)
	List(ctx context.Context, filter BookFilter) ([]*Book, int, error)
	Delete(ctx context.Context, id string) error
	SaveProgress(ctx context.Context, id string, progress BookProgress) error
}

type ChapterRepository interface {
	ReplaceByBook(ctx context.Context, bookID string, chapters []*BookChapter) error
	ListByBook(ctx context.Context, bookID string) ([]*BookChapter, error)
	FindByIndex(ctx context.Context, bookID string, index int) (*BookChapter, error)
	// SaveTitle 用正文规则解析出的标题更新章节标题（对齐 Legado BookContent.kt:66-69 写回 bookChapter.title）。
	SaveTitle(ctx context.Context, bookID string, chapterIndex int, title string) error
	SaveContent(ctx context.Context, bookID string, chapterIndex int, content string) error
	GetContent(ctx context.Context, bookID string, chapterIndex int) (string, bool, error)
}
