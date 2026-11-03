// Package sessionbridge exposes explicitly authorized terminal sessions as MCP
// tools. It owns its tables and transport; the host supplies a terminal adapter.
package sessionbridge

import (
	"context"
	"errors"
	"time"
)

const Endpoint = "/mcp/sessions"

var ErrInputRejected = errors.New("terminal input rejected before write")

const (
	Read   = "read"
	Input  = "input"
	Create = "create"
	Manage = "manage"
)

type Token struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Hint        string     `json:"hint"`
	Permissions []string   `json:"permissions"`
	SessionIDs  []string   `json:"sessionIDs"`
	HostIDs     []string   `json:"hostIDs"`
	Enabled     bool       `json:"enabled"`
	ExpiresAt   *time.Time `json:"expiresAt"`
	LastUsedAt  *time.Time `json:"lastUsedAt"`
	CreatedAt   time.Time  `json:"createdAt"`
	UserID      string     `json:"-"`
}

type TokenInput struct {
	Name        string     `json:"name"`
	Permissions []string   `json:"permissions"`
	SessionIDs  []string   `json:"sessionIDs"`
	HostIDs     []string   `json:"hostIDs"`
	Enabled     bool       `json:"enabled"`
	ExpiresAt   *time.Time `json:"expiresAt"`
}

func (t Token) Allows(permission string) bool {
	for _, p := range t.Permissions {
		if p == permission {
			return true
		}
	}
	return false
}

func contains(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func (t Token) AllowsSession(s Session) bool {
	return contains(t.SessionIDs, s.ID) || contains(t.HostIDs, s.HostID)
}

type Session struct {
	ID        string `json:"id"`
	HostID    string `json:"hostID"`
	HostName  string `json:"hostName"`
	Name      string `json:"name"`
	Mode      string `json:"mode"`
	Status    string `json:"status"`
	LastError string `json:"lastError,omitempty"`
	Attached  bool   `json:"attached"`
}

type Host struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Address string `json:"address"`
}

type Cursor struct {
	StreamID string `json:"streamID"`
	Offset   uint64 `json:"offset"`
}

// Snapshot is immutable and bounded. Raw contains terminal bytes, never model
// messages. Capture, when present, is a real tmux pane capture without styles.
type Snapshot struct {
	Cursor
	Raw          []byte
	Capture      *string
	Truncated    bool
	Rows, Cols   int
	LastOutputAt time.Time
	Source       string
}

type Termination struct {
	OK               bool   `json:"ok"`
	RemoteTerminated bool   `json:"remoteTerminated"`
	Warning          string `json:"warning,omitempty"`
}

// Backend methods must enforce user ownership independently of token scopes.
// Snapshot and WriteInput never acquire or resize the browser's terminal.
type Backend interface {
	Sessions(context.Context, string) ([]Session, error)
	Hosts(context.Context, string) ([]Host, error)
	Snapshot(context.Context, string, string, int) (Snapshot, error)
	WriteInput(context.Context, string, string, []byte) (int, error)
	Create(context.Context, string, string, string, string) (Session, error)
	Restore(context.Context, string, string) (Session, error)
	Rename(context.Context, string, string, string) (Session, error)
	Terminate(context.Context, string, string) (Termination, error)
}
