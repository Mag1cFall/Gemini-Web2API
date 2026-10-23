package requestdb

import (
	"context"
	"time"
)

// StoredCooldown 是一个账号或其某个模型的冷却，Model 为空表示整个账号
type StoredCooldown struct {
	Account string
	Model   string
	Until   time.Time
	Reason  string
}

// SaveCooldowns 在一个事务中用 cooldowns 替换已保存的全部冷却
func (store *Store) SaveCooldowns(ctx context.Context, cooldowns []StoredCooldown) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM cooldowns`); err != nil {
		_ = tx.Rollback()
		return err
	}
	for _, cooldown := range cooldowns {
		if _, err := tx.ExecContext(ctx, `INSERT INTO cooldowns(account,model,until,reason) VALUES(?,?,?,?)`,
			cooldown.Account, cooldown.Model, cooldown.Until.UnixMilli(), cooldown.Reason); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// Cooldowns 删除在 now 之前结束的冷却并返回其余冷却
func (store *Store) Cooldowns(ctx context.Context, now time.Time) ([]StoredCooldown, error) {
	if _, err := store.db.ExecContext(ctx, `DELETE FROM cooldowns WHERE until < ?`, now.UnixMilli()); err != nil {
		return nil, err
	}
	rows, err := store.db.QueryContext(ctx, `SELECT account,model,until,reason FROM cooldowns`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cooldowns []StoredCooldown
	for rows.Next() {
		var cooldown StoredCooldown
		var until int64
		if err := rows.Scan(&cooldown.Account, &cooldown.Model, &until, &cooldown.Reason); err != nil {
			return nil, err
		}
		cooldown.Until = time.UnixMilli(until)
		cooldowns = append(cooldowns, cooldown)
	}
	return cooldowns, rows.Err()
}
