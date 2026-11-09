package requestdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// incrementalVacuum 是 PRAGMA auto_vacuum 表示增量收缩的值
const incrementalVacuum = 2

// applySchema 为已有表补上 schema 定义而缺少的列，执行 schema，再删除 schema 没有定义的表、索引、视图与触发器
func applySchema(db *sql.DB) error {
	defined, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return err
	}
	defer defined.Close()
	defined.SetMaxOpenConns(1)
	if _, err := defined.Exec(schema); err != nil {
		return err
	}
	want, err := schemaObjects(defined)
	if err != nil {
		return err
	}
	have, err := schemaObjects(db)
	if err != nil {
		return err
	}
	for name, kind := range want {
		if kind == "table" && have[name] == "table" {
			if err := addMissingColumns(defined, db, name); err != nil {
				return err
			}
		}
	}
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	for name, kind := range have {
		if _, ok := want[name]; !ok {
			if _, err := db.Exec(`DROP ` + strings.ToUpper(kind) + ` IF EXISTS ` + quoteIdentifier(name)); err != nil {
				return err
			}
		}
	}
	return nil
}

// schemaObjects 返回库中除 SQLite 内部对象外的表、索引、视图与触发器的名称与类型
func schemaObjects(db *sql.DB) (map[string]string, error) {
	rows, err := db.Query(`SELECT name, type FROM sqlite_master WHERE type IN ('table', 'index', 'view', 'trigger') AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	objects := make(map[string]string)
	for rows.Next() {
		var name, kind string
		if err := rows.Scan(&name, &kind); err != nil {
			return nil, err
		}
		objects[name] = kind
	}
	return objects, rows.Err()
}

// addMissingColumns 按 defined 中同名表的列定义为 db 中的表追加缺少的列
func addMissingColumns(defined *sql.DB, db *sql.DB, table string) error {
	existing := make(map[string]bool)
	rows, err := db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return err
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return errors.Join(err, rows.Close())
		}
		existing[name] = true
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	rows, err = defined.Query(`SELECT name, type, "notnull", dflt_value FROM pragma_table_info(?)`, table)
	if err != nil {
		return err
	}
	var additions []string
	for rows.Next() {
		var name, kind string
		var notNull bool
		var fallback sql.NullString
		if err := rows.Scan(&name, &kind, &notNull, &fallback); err != nil {
			return errors.Join(err, rows.Close())
		}
		if existing[name] {
			continue
		}
		column := quoteIdentifier(name) + " " + kind
		if notNull {
			column += " NOT NULL"
		}
		if fallback.Valid {
			column += " DEFAULT " + fallback.String
		}
		additions = append(additions, `ALTER TABLE `+quoteIdentifier(table)+` ADD COLUMN `+column)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, addition := range additions {
		if _, err := db.Exec(addition); err != nil {
			return err
		}
	}
	return nil
}

// quoteIdentifier 返回双引号括起的 SQL 标识符
func quoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// useIncrementalVacuum 把新库与不是增量收缩的已有库设为增量收缩并整理一次，此后删除记录留下的空闲页可由 reclaim 交还
func useIncrementalVacuum(db *sql.DB) error {
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var mode int
	if err := conn.QueryRowContext(ctx, `PRAGMA auto_vacuum`).Scan(&mode); err != nil {
		return fmt.Errorf("读取收缩模式: %w", err)
	}
	if mode == incrementalVacuum {
		return nil
	}
	if _, err := conn.ExecContext(ctx, `PRAGMA auto_vacuum = INCREMENTAL`); err != nil {
		return fmt.Errorf("设置收缩模式: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `VACUUM`); err != nil {
		return fmt.Errorf("整理数据库: %w", err)
	}
	return nil
}

// walTruncateFrames 是定时回收截短 WAL 文件的帧数下限，约 16 MB
const walTruncateFrames = 4096

// reclaim 把空闲页交还文件系统并做不等待读事务的被动检查点，WAL 达到 walTruncateFrames 且已全部写回时截短文件；incremental_vacuum 每交还一页返回一行，读完全部行才交还全部空闲页
func reclaim(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `PRAGMA incremental_vacuum`)
	if err != nil {
		return fmt.Errorf("交还空闲页: %w", err)
	}
	for rows.Next() {
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return fmt.Errorf("交还空闲页: %w", err)
	}
	var busy, frames, checkpointed int
	if err := db.QueryRowContext(ctx, `PRAGMA wal_checkpoint(PASSIVE)`).Scan(&busy, &frames, &checkpointed); err != nil {
		return fmt.Errorf("写回 WAL: %w", err)
	}
	if busy != 0 || frames < walTruncateFrames || checkpointed != frames {
		return nil
	}
	if _, err := db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return fmt.Errorf("截短 WAL: %w", err)
	}
	return nil
}
