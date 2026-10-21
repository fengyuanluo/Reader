package book

import "context"

type BookFilter struct {
	Page     int
	PageSize int
}

// BookProgress 是阅读进度；name/author 是 Legado 用来校验进度所属书籍身份的字段。
type BookProgress struct {
	Name            string
	Author          string
	DurChapterTitle string
	DurChapterIndex int
	DurChapterPos   int
	DurChapterTime  int64
}

// TocStats 是目录刷新（RefreshToc）后需要写回 books 表的元数据（S12-01/S12-03/S12-05）。
type TocStats struct {
	TotalChapterNum    int
	LastCheckTime      int64
	LastCheckCount     int
	LatestChapterTime  int64
	LatestChapterTitle string
	Variable           string
	VariableChanged    bool
	DurChapterTitle    string
	DurChapterIndex    int
	DurChapterPos      int
	DurChapterTime     int64
	DurChapterChanged  bool
}

type BookRepository interface {
	Save(ctx context.Context, book *Book) error
	// UpdateInfo 只更新书籍信息刷新（RefreshInfo）需要写入的列，避免 RefreshInfo
	// 路径上的重复整行写覆盖阅读进度等无关列。
	UpdateInfo(ctx context.Context, book *Book) error
	// UpdateTocStats 只更新目录刷新（RefreshToc）相关列：Variable 仅在 VariableChanged
	// 时写入，dur_* 仅在 DurChapterChanged 时写入；行不存在时不创建（与 UpdateInfo 同语义）。
	UpdateTocStats(ctx context.Context, bookID string, stats TocStats) error
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
