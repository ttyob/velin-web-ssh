package sessionbridge

import (
	"context"
	"errors"
	"strings"
	"time"
)

// AccessToken is for trusted in-process adapters; browser credentials never substitute for its scope.
func (m *Module) AccessToken(userID, id string) (Token, error) {
	token, err := scanToken(m.store.DB.QueryRow(`SELECT `+tokenColumns+` FROM session_bridge_tokens WHERE id=? AND user_id=?`, id, userID))
	if err != nil {
		return Token{}, ErrUnauthorized
	}
	if err = m.revalidate(token, Read, nil); err != nil {
		return Token{}, err
	}
	return token, nil
}

func (m *Module) CheckSession(ctx context.Context, token Token, id, permission string) (Session, error) {
	session, err := m.session(ctx, token, id)
	if err != nil {
		return Session{}, err
	}
	if err = m.revalidate(token, permission, &session); err != nil {
		return Session{}, err
	}
	return session, nil
}

type InputArgs = inputArgs
type ReadResult = readResult
type MutationResult = mutationResult
type CreateArgs = createArgs
type RestoreArgs = restoreArgs
type RenameArgs = renameArgs
type TerminateArgs = terminateArgs

func (m *Module) ListSessions(ctx context.Context, token Token) ([]Session, error) {
	if err := m.revalidate(token, Read, nil); err != nil {
		return nil, err
	}
	return m.listSessions(ctx, token)
}
func (m *Module) ListHosts(ctx context.Context, token Token) ([]Host, error) {
	if err := m.revalidate(token, Read, nil); err != nil {
		return nil, err
	}
	return m.listHosts(ctx, token)
}
func (m *Module) ReadSession(ctx context.Context, token Token, id string, lines int) (ReadResult, error) {
	return m.readSession(ctx, token, readArgs{SessionID: id, MaxLines: lines})
}

// SessionCursor reads metadata only; notification polling does not run tmux
// capture commands or render a terminal on every tick.
func (m *Module) SessionCursor(ctx context.Context, token Token, id string) (Snapshot, error) {
	if _, err := m.CheckSession(ctx, token, id, Read); err != nil {
		return Snapshot{}, err
	}
	return m.backend.Snapshot(ctx, token.UserID, id, 0)
}

func (m *Module) SendInput(ctx context.Context, token Token, args inputArgs) (mutationResult, error) {
	if err := m.revalidate(token, Input, nil); err != nil {
		return mutationResult{}, err
	}
	session, err := m.session(ctx, token, args.SessionID)
	if err != nil {
		return mutationResult{}, err
	}
	data, err := inputBytes(args)
	if err != nil {
		return mutationResult{}, err
	}
	return m.mutation(ctx, token, "input", args.RequestID, args, func() mutationResult {
		if err := m.revalidate(token, Input, &session); err != nil {
			return failed(err)
		}
		n, err := m.backend.WriteInput(ctx, token.UserID, session.ID, data)
		if err != nil {
			if errors.Is(err, ErrInputRejected) {
				return mutationResult{Status: "rejected", Message: err.Error()}
			}
			return mutationResult{Status: "uncertain", Bytes: n, Message: "终端写入未确认，请先读取会话再决定下一步；勿自动补发。"}
		}
		return mutationResult{Status: "written", Bytes: n, Message: "输入已写入终端；请读取会话输出确认执行结果。"}
	})
}

func (m *Module) CreateSession(ctx context.Context, token Token, args createArgs) (mutationResult, error) {
	if err := m.revalidate(token, Create, nil); err != nil {
		return mutationResult{}, err
	}
	if !contains(token.HostIDs, args.HostID) {
		return mutationResult{}, errors.New("主机未授权创建会话")
	}
	args.Name = strings.TrimSpace(args.Name)
	if args.Name == "" || len([]rune(args.Name)) > 120 {
		return mutationResult{}, errors.New("会话名称须为 1～120 个字符")
	}
	if args.Mode == "" {
		args.Mode = "tmux"
	}
	if args.Mode != "tmux" && args.Mode != "normal" {
		return mutationResult{}, errors.New("mode 须为 tmux 或 normal")
	}
	return m.mutation(ctx, token, "create", args.RequestID, args, func() mutationResult {
		if err := m.revalidate(token, Create, &Session{HostID: args.HostID}); err != nil {
			return failed(err)
		}
		session, err := m.backend.Create(ctx, token.UserID, args.HostID, args.Name, args.Mode)
		if err != nil {
			return failed(err)
		}
		return mutationResult{Status: "accepted", Session: &session}
	})
}

func (m *Module) RestoreSession(ctx context.Context, token Token, args restoreArgs) (mutationResult, error) {
	if err := m.revalidate(token, Create, nil); err != nil {
		return mutationResult{}, err
	}
	session, err := m.session(ctx, token, args.SessionID)
	if err != nil {
		return mutationResult{}, err
	}
	return m.mutation(ctx, token, "restore", args.RequestID, args, func() mutationResult {
		if err := m.revalidate(token, Create, &session); err != nil {
			return failed(err)
		}
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		value, err := m.backend.Restore(ctx, token.UserID, session.ID)
		if err != nil {
			return failed(err)
		}
		return mutationResult{Status: "restored", Session: &value}
	})
}

func (m *Module) RenameSession(ctx context.Context, token Token, args renameArgs) (Session, error) {
	if err := m.revalidate(token, Manage, nil); err != nil {
		return Session{}, err
	}
	session, err := m.session(ctx, token, args.SessionID)
	if err != nil {
		return Session{}, err
	}
	args.Name = strings.TrimSpace(args.Name)
	if args.Name == "" || len([]rune(args.Name)) > 120 {
		return Session{}, errors.New("会话名称须为 1～120 个字符")
	}
	if err := m.revalidate(token, Manage, &session); err != nil {
		return Session{}, err
	}
	value, err := m.backend.Rename(ctx, token.UserID, session.ID, args.Name)
	if err == nil {
		_ = m.store.Audit(token.UserID, "session_bridge.rename", "terminal_session", session.ID, "", map[string]string{"tokenID": token.ID})
	}
	return value, err
}

func (m *Module) TerminateSession(ctx context.Context, token Token, args terminateArgs) (mutationResult, error) {
	if err := m.revalidate(token, Manage, nil); err != nil {
		return mutationResult{}, err
	}
	session, err := m.session(ctx, token, args.SessionID)
	if err != nil {
		return mutationResult{}, err
	}
	if !args.Confirm {
		return mutationResult{}, errors.New("终止会话需要 confirm=true")
	}
	return m.mutation(ctx, token, "terminate", args.RequestID, args, func() mutationResult {
		if err := m.revalidate(token, Manage, &session); err != nil {
			return failed(err)
		}
		value, err := m.backend.Terminate(ctx, token.UserID, session.ID)
		if err != nil {
			return failed(err)
		}
		return mutationResult{Status: "terminated", Termination: &value}
	})
}
