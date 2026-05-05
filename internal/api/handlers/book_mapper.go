package handlers

import (
	"reader/internal/api/generated"
	"reader/internal/domain/book"
)

func bookFromDTO(dto generated.Book) book.Book {
	b := book.Book{
		BookURL:    dto.BookUrl,
		TocURL:     stringValue(dto.TocUrl),
		Origin:     dto.Origin,
		OriginName: dto.OriginName,
		Name:       dto.Name,
		Author:     dto.Author,
		Type:       dto.Type,
		Kind:       stringValue(dto.Kind),
		CoverURL:   stringValue(dto.CoverUrl),
		Intro:      stringValue(dto.Intro),
		WordCount:  stringValue(dto.WordCount),
		Variable:   stringValue(dto.Variable),
	}
	if dto.DownloadUrls != nil {
		b.DownloadURLs = append([]string(nil), (*dto.DownloadUrls)...)
	}
	if dto.LatestChapterTitle != nil {
		b.LatestChapterTitle = *dto.LatestChapterTitle
	}
	if dto.DurChapterTitle != nil {
		b.DurChapterTitle = *dto.DurChapterTitle
	}
	if dto.DurChapterIndex != nil {
		b.DurChapterIndex = *dto.DurChapterIndex
	}
	if dto.DurChapterPos != nil {
		b.DurChapterPos = *dto.DurChapterPos
	}
	if dto.DurChapterTime != nil {
		b.DurChapterTime = *dto.DurChapterTime
	}
	if dto.OriginOrder != nil {
		b.OriginOrder = *dto.OriginOrder
	}
	return b
}

func mapBook(b *book.Book) generated.Book {
	dto := generated.Book{
		BookUrl:            b.BookURL,
		TocUrl:             stringPtr(b.TocURL),
		Origin:             b.Origin,
		OriginName:         b.OriginName,
		Name:               b.Name,
		Author:             b.Author,
		Type:               b.Type,
		Kind:               stringPtr(b.Kind),
		CoverUrl:           stringPtr(b.CoverURL),
		Intro:              stringPtr(b.Intro),
		LatestChapterTitle: stringPtr(b.LatestChapterTitle),
		DurChapterTitle:    stringPtr(b.DurChapterTitle),
		DurChapterIndex:    intPtr(b.DurChapterIndex),
		DurChapterPos:      intPtr(b.DurChapterPos),
		DurChapterTime:     int64Ptr(b.DurChapterTime),
		WordCount:          stringPtr(b.WordCount),
		OriginOrder:        intPtr(b.OriginOrder),
		Variable:           stringPtr(b.Variable),
	}
	if len(b.DownloadURLs) > 0 {
		urls := append([]string(nil), b.DownloadURLs...)
		dto.DownloadUrls = &urls
	}
	return dto
}

func mapBookPtr(b *book.Book) *generated.Book {
	if b == nil {
		return nil
	}
	dto := mapBook(b)
	return &dto
}

func mapBooks(items []*book.Book) []generated.Book {
	out := make([]generated.Book, 0, len(items))
	for _, item := range items {
		out = append(out, mapBook(item))
	}
	return out
}

func mapChapter(chapter *book.BookChapter) generated.Chapter {
	return generated.Chapter{
		Url:         chapter.URL,
		Title:       chapter.Title,
		IsVolume:    boolPtr(chapter.IsVolume),
		BaseUrl:     stringPtr(chapter.BaseURL),
		BookUrl:     chapter.BookURL,
		Index:       chapter.Index,
		IsVip:       boolPtr(chapter.IsVIP),
		IsPay:       boolPtr(chapter.IsPay),
		ResourceUrl: stringPtr(chapter.ResourceURL),
		Tag:         stringPtr(chapter.Tag),
		WordCount:   stringPtr(chapter.WordCount),
		Variable:    stringPtr(chapter.Variable),
	}
}

func mapChapters(items []*book.BookChapter) []generated.Chapter {
	out := make([]generated.Chapter, 0, len(items))
	for _, item := range items {
		out = append(out, mapChapter(item))
	}
	return out
}

func progressFromDTO(dto generated.BookProgress) book.BookProgress {
	progress := book.BookProgress{}
	if dto.DurChapterTitle != nil {
		progress.DurChapterTitle = *dto.DurChapterTitle
	}
	if dto.DurChapterIndex != nil {
		progress.DurChapterIndex = *dto.DurChapterIndex
	}
	if dto.DurChapterPos != nil {
		progress.DurChapterPos = *dto.DurChapterPos
	}
	if dto.DurChapterTime != nil {
		progress.DurChapterTime = *dto.DurChapterTime
	}
	return progress
}

func boolPtr(value bool) *bool {
	return &value
}
