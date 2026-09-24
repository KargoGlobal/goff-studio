package storage

import (
	"context"
	"errors"
	"time"
)

type File struct {
	Path    string
	Content []byte
	Version string
}

type Commit struct {
	SHA     string    `json:"sha"`
	Message string    `json:"message"`
	Author  string    `json:"author"`
	Email   string    `json:"email"`
	When    time.Time `json:"when"`
}

type Identity struct {
	Name    string
	Email   string
	Subject string
}

type Result struct {
	Version string
	Retried bool
}

type ChangeOp struct {
	Path        string
	Key         string
	BaseVersion string
	Message     string
	Apply       func(current []byte) ([]byte, error)
	Changed     func(current []byte) (bool, error)
	MaxAttempts int
}

type Capabilities struct {
	History     bool `json:"history"`
	Attribution bool `json:"attribution"`
	Review      bool `json:"review"`
}

type Backend interface {
	Name() string
	Capabilities() Capabilities

	ReadFile(ctx context.Context, path string) (*File, error)
	ListFiles(ctx context.Context, dir string) ([]string, error)
	ListDirectories(ctx context.Context, dir string) ([]string, error)

	Write(ctx context.Context, op ChangeOp, who Identity) (*Result, error)
	CreateFile(ctx context.Context, path string, content []byte, message string, who Identity) error

	History(ctx context.Context, path string, limit int) ([]Commit, error)
}

var (
	ErrConflict = errors.New("that flag was changed by someone else")
	ErrNotFound = errors.New("not found")
)

type Checker interface {
	Check(ctx context.Context) error
}

func Check(ctx context.Context, b Backend) error {
	if c, ok := b.(Checker); ok {
		return c.Check(ctx)
	}
	return nil
}
