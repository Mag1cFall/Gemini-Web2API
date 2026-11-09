package requestdb

import (
	"context"
	"database/sql"
	"time"
)

// StoredSession 是一个会话标识对应的网页会话状态、所属账号与到期时间
type StoredSession struct {
	Key           string
	CID           string
	RID           string
	RCID          string
	Token         string
	ModelMode     int
	Turns         int
	Account       string
	ContextTokens int
	ExpiresAt     time.Time
}

// SaveSessions 排队写入或替换会话，与同一提交窗口内的账本记录合并为一个事务，不等待落盘
func (store *Store) SaveSessions(sessions []StoredSession) {
	store.enqueue(write{sessions: sessions})
}

// saveSession 在事务中写入或替换一个会话
func saveSession(tx *sql.Tx, session StoredSession) error {
	_, err := tx.Exec(`INSERT INTO sessions(key,cid,rid,rcid,token,model_mode,turns,account,context_tokens,expires_at) VALUES(?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(key) DO UPDATE SET cid=excluded.cid, rid=excluded.rid, rcid=excluded.rcid, token=excluded.token, model_mode=excluded.model_mode,
turns=excluded.turns, account=excluded.account, context_tokens=excluded.context_tokens, expires_at=excluded.expires_at`,
		session.Key, session.CID, session.RID, session.RCID, session.Token, session.ModelMode, session.Turns, session.Account,
		session.ContextTokens, session.ExpiresAt.UnixMilli())
	return err
}

// Sessions 删除已过期的会话并返回其余会话
func (store *Store) Sessions(ctx context.Context, now time.Time) ([]StoredSession, error) {
	store.flush()
	if err := store.PruneSessions(ctx, now); err != nil {
		return nil, err
	}
	rows, err := store.reads.QueryContext(ctx, `SELECT key,cid,rid,rcid,token,model_mode,turns,account,context_tokens,expires_at FROM sessions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sessions []StoredSession
	for rows.Next() {
		var session StoredSession
		var expires int64
		if err := rows.Scan(&session.Key, &session.CID, &session.RID, &session.RCID, &session.Token, &session.ModelMode, &session.Turns,
			&session.Account, &session.ContextTokens, &expires); err != nil {
			return nil, err
		}
		session.ExpiresAt = time.UnixMilli(expires)
		sessions = append(sessions, session)
	}
	return sessions, rows.Err()
}

// PruneSessions 删除到期时间早于 now 的会话
func (store *Store) PruneSessions(ctx context.Context, now time.Time) error {
	_, err := store.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ?`, now.UnixMilli())
	return err
}
