package requestdb

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// StoredFile 是文件接口保存的文件元数据，内容位于数据目录的 files/<ID>
type StoredFile struct {
	ID        string
	CreatedAt time.Time
	// ExpiresAt 为零值时文件不过期
	ExpiresAt time.Time
	Filename  string
	MIMEType  string
	Bytes     int64
	Purpose   string
	// SHA256 为内容 SHA-256 的十六进制
	SHA256 string
	// Source 为 uploaded（客户端上传）或 generated（服务生成）
	Source string
}

// FileQuery 选择文件列表的用途、顺序、游标与数量
type FileQuery struct {
	Purpose string
	// Ascending 为真时按创建时间从旧到新
	Ascending bool
	// After 为上一页最后一个文件 ID
	After string
	Limit int
}

// FilesDir 返回文件内容目录，位于请求账本所在目录的 files
func (store *Store) FilesDir() (string, error) {
	var path string
	if err := store.db.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path); err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(path), "files"), nil
}

// filePath 返回文件内容路径，ID 只能含字母、数字、连字符与下划线
func (store *Store) filePath(id string) (string, error) {
	if id == "" || strings.Trim(id, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_") != "" {
		return "", ErrNotFound
	}
	dir, err := store.FilesDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, id), nil
}

// CreateFile 写入文件内容并登记元数据，返回含字节数与 SHA-256 的记录
func (store *Store) CreateFile(ctx context.Context, file StoredFile, content io.Reader) (StoredFile, error) {
	path, err := store.filePath(file.ID)
	if err != nil {
		return StoredFile{}, fmt.Errorf("文件 ID %q 无效", file.ID)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return StoredFile{}, err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), file.ID+".*.part")
	if err != nil {
		return StoredFile{}, err
	}
	digest := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(temporary, digest), content)
	closeErr := temporary.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		_ = os.Remove(temporary.Name())
		return StoredFile{}, err
	}
	file.Bytes, file.SHA256 = size, hex.EncodeToString(digest.Sum(nil))
	if file.CreatedAt.IsZero() {
		file.CreatedAt = time.Now()
	}
	var expires int64
	if !file.ExpiresAt.IsZero() {
		expires = file.ExpiresAt.UnixMilli()
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO files(id,created_at,expires_at,filename,mime_type,bytes,purpose,sha256,source) VALUES(?,?,?,?,?,?,?,?,?)`,
		file.ID, file.CreatedAt.UnixMilli(), expires, file.Filename, file.MIMEType, file.Bytes, file.Purpose, file.SHA256, file.Source); err != nil {
		_ = os.Remove(temporary.Name())
		return StoredFile{}, err
	}
	if err := os.Rename(temporary.Name(), path); err != nil {
		_ = os.Remove(temporary.Name())
		_, _ = store.db.ExecContext(ctx, `DELETE FROM files WHERE id=?`, file.ID)
		return StoredFile{}, err
	}
	return file, nil
}

// UpdateFile 修改文件的用途与过期时间
func (store *Store) UpdateFile(ctx context.Context, file StoredFile) error {
	var expires int64
	if !file.ExpiresAt.IsZero() {
		expires = file.ExpiresAt.UnixMilli()
	}
	_, err := store.db.ExecContext(ctx, `UPDATE files SET purpose=?, expires_at=? WHERE id=?`, file.Purpose, expires, file.ID)
	return err
}

// File 返回未过期文件的元数据，不存在时返回 ErrNotFound
func (store *Store) File(ctx context.Context, id string) (StoredFile, error) {
	store.purgeExpiredFiles(ctx)
	rows, err := store.db.QueryContext(ctx, `SELECT `+fileColumns+` FROM files WHERE id=?`, id)
	if err != nil {
		return StoredFile{}, err
	}
	files, err := scanFiles(rows)
	if err != nil {
		return StoredFile{}, err
	}
	if len(files) == 0 {
		return StoredFile{}, ErrNotFound
	}
	return files[0], nil
}

// OpenFile 返回文件元数据与内容，调用方负责关闭内容
func (store *Store) OpenFile(ctx context.Context, id string) (StoredFile, *os.File, error) {
	file, err := store.File(ctx, id)
	if err != nil {
		return StoredFile{}, nil, err
	}
	path, err := store.filePath(id)
	if err != nil {
		return StoredFile{}, nil, err
	}
	content, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return StoredFile{}, nil, ErrNotFound
	}
	return file, content, err
}

// ReadFile 返回文件元数据与全部内容
func (store *Store) ReadFile(ctx context.Context, id string) (StoredFile, []byte, error) {
	file, content, err := store.OpenFile(ctx, id)
	if err != nil {
		return StoredFile{}, nil, err
	}
	defer content.Close()
	data, err := io.ReadAll(content)
	return file, data, err
}

// Files 按查询返回一页未过期文件与是否还有下一页
func (store *Store) Files(ctx context.Context, query FileQuery) ([]StoredFile, bool, error) {
	store.purgeExpiredFiles(ctx)
	where, args := []string{"1=1"}, []any{}
	if query.Purpose != "" {
		where, args = append(where, "purpose=?"), append(args, query.Purpose)
	}
	comparison, order := "<", "DESC"
	if query.Ascending {
		comparison, order = ">", "ASC"
	}
	if query.After != "" {
		var created int64
		err := store.db.QueryRowContext(ctx, `SELECT created_at FROM files WHERE id=?`, query.After).Scan(&created)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, ErrNotFound
		}
		if err != nil {
			return nil, false, err
		}
		where = append(where, fmt.Sprintf("(created_at %s ? OR (created_at = ? AND id %s ?))", comparison, comparison))
		args = append(args, created, created, query.After)
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 10000
	}
	args = append(args, limit+1)
	rows, err := store.db.QueryContext(ctx, fmt.Sprintf(`SELECT %s FROM files WHERE %s ORDER BY created_at %s, id %s LIMIT ?`, fileColumns, strings.Join(where, " AND "), order, order), args...)
	if err != nil {
		return nil, false, err
	}
	files, err := scanFiles(rows)
	if err != nil {
		return nil, false, err
	}
	if len(files) > limit {
		return files[:limit], true, nil
	}
	return files, false, nil
}

// DeleteFile 删除文件元数据与内容，返回是否存在
func (store *Store) DeleteFile(ctx context.Context, id string) (bool, error) {
	result, err := store.db.ExecContext(ctx, `DELETE FROM files WHERE id=?`, id)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected == 0 {
		return false, err
	}
	if path, pathErr := store.filePath(id); pathErr == nil {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return true, err
		}
	}
	return true, nil
}

// fileColumns 是文件表按 scanFiles 顺序读取的列
const fileColumns = `id,created_at,expires_at,filename,mime_type,bytes,purpose,sha256,source`

// scanFiles 读取文件行并关闭结果集
func scanFiles(rows *sql.Rows) ([]StoredFile, error) {
	defer rows.Close()
	var files []StoredFile
	for rows.Next() {
		var file StoredFile
		var created, expires int64
		if err := rows.Scan(&file.ID, &created, &expires, &file.Filename, &file.MIMEType, &file.Bytes, &file.Purpose, &file.SHA256, &file.Source); err != nil {
			return nil, err
		}
		file.CreatedAt = time.UnixMilli(created)
		if expires > 0 {
			file.ExpiresAt = time.UnixMilli(expires)
		}
		files = append(files, file)
	}
	return files, rows.Err()
}

// purgeExpiredFiles 删除已过期的文件
func (store *Store) purgeExpiredFiles(ctx context.Context) {
	rows, err := store.db.QueryContext(ctx, `SELECT id FROM files WHERE expires_at > 0 AND expires_at < ?`, time.Now().UnixMilli())
	if err != nil {
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	_ = rows.Close()
	for _, id := range ids {
		_, _ = store.DeleteFile(ctx, id)
	}
}
