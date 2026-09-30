package yandex

import (
	"strings"
	"time"
)

type Resource struct {
	Type     string    `json:"type"`
	Size     int64     `json:"size"`
	SHA256   string    `json:"sha256"`
	Created  time.Time `json:"created"`
	Modified time.Time `json:"modified"`
	ID       string    `json:"resource_id"`
}

func SameResource(a, b *Resource) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}

	return a.Type == b.Type && a.Size == b.Size && a.SHA256 == b.SHA256 && a.ID == b.ID && a.Created.Equal(b.Created) && a.Modified.Equal(b.Modified)
}

func Matches(r *Resource, hash string, size int64) bool {
	return r != nil && r.Type == "file" && r.Size == size && strings.EqualFold(r.SHA256, hash)
}
