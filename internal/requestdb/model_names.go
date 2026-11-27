package requestdb

import (
	"encoding/json"

	"github.com/Mag1cFall/Gemini-Web2API/internal/gemini"
)

// LoadModelNames 读取带模型 hash 的公开模型 ID 改名记录与各账号最近一次读取的目录
func (store *Store) LoadModelNames() (map[string]gemini.ModelRename, map[string]gemini.AccountCatalog, error) {
	renames := make(map[string]gemini.ModelRename)
	rows, err := store.reads.Query(`SELECT old,current,hash FROM model_renames WHERE hash != ''`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var old, current, hash string
		if err := rows.Scan(&old, &current, &hash); err != nil {
			return nil, nil, err
		}
		renames[old] = gemini.ModelRename{Current: current, Hash: hash}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	catalogs := make(map[string]gemini.AccountCatalog)
	catalogRows, err := store.reads.Query(`SELECT account,catalog FROM model_catalogs`)
	if err != nil {
		return nil, nil, err
	}
	defer catalogRows.Close()
	for catalogRows.Next() {
		var account string
		var encoded []byte
		var catalog gemini.AccountCatalog
		if err := catalogRows.Scan(&account, &encoded); err != nil {
			return nil, nil, err
		}
		if err := json.Unmarshal(encoded, &catalog); err != nil {
			return nil, nil, err
		}
		catalogs[account] = catalog
	}
	return renames, catalogs, catalogRows.Err()
}

// SaveModelNames 在一个事务中保存账号最近一次读取的目录并以 renames 替换全部改名记录
func (store *Store) SaveModelNames(accountID string, catalog gemini.AccountCatalog, renames map[string]gemini.ModelRename) error {
	encoded, err := json.Marshal(catalog)
	if err != nil {
		return err
	}
	tx, err := store.db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO model_catalogs(account,catalog) VALUES(?,?) ON CONFLICT(account) DO UPDATE SET catalog=excluded.catalog`, accountID, encoded); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.Exec(`DELETE FROM model_renames`); err != nil {
		_ = tx.Rollback()
		return err
	}
	for old, rename := range renames {
		if rename.Current == "" || rename.Hash == "" {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO model_renames(old,current,hash) VALUES(?,?,?)`, old, rename.Current, rename.Hash); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}
